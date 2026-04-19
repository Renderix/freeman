package conv

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Renderix/freeman/internal/agent"
	"github.com/Renderix/freeman/internal/audio"
	"github.com/Renderix/freeman/internal/audio/wakeword"
	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/tools"
)

// Transcriber is the subset of stt.Transcriber the conv layer needs.
type Transcriber interface {
	Utterances() <-chan string
	Mute()
	Unmute()
	Stop()
}

// Speaker is the subset of playback.Speaker the conv layer needs.
// Speak queues a sentence's samples without blocking on playback; Flush
// waits for the sink to drain (end-of-turn); Cancel clears pending samples
// for barge-in. This three-method split is what enables gapless inter-
// sentence playback — see speaker.go for the rationale.
type Speaker interface {
	Speak(ctx context.Context, text string) error
	Flush(ctx context.Context) error
	Cancel()
}

// Deps are the runtime dependencies injected into a Session.
type Deps struct {
	Transcriber    Transcriber
	Speaker        Speaker
	Muter          audio.Muter // gates VAD+STT during a speaking batch
	WakewordEvents <-chan wakeword.KeywordKind
	SpeechOnsets   <-chan struct{}
	TaskManager    *TaskManager
	ChatAgent      agent.ChatAgent
	ModelResolver  func(hint string) string
	// Tools is the registry of MD-defined tools available to the chat
	// LLM. Hardcoded TaskManager tools (start_task etc.) take precedence;
	// anything else falls through to the registry.
	Tools          *tools.Registry

	Persona  config.PersonaConfig // persona config drives name, greeting, rules, wakeword
	Model    string               // e.g. "claude-haiku-4-5"
	RepoRoot string               // used to read project context
	Logger   *slog.Logger
}

// Session is the conv-layer event loop. It routes between mic,
// ChatAgent, TaskManager, and speaker.
type Session struct {
	deps Deps
	log  *slog.Logger

	awake  bool
	nextID atomic.Uint64

	turnMu        sync.Mutex
	currentTurnID string
	turnCanceled  bool
	// fillerSpoken is true once a tool-call filler has been emitted for
	// the current turn. Chained tool calls within the same turn skip the
	// filler to avoid "Searching the web. Pulling that up." back-to-back.
	fillerSpoken  bool

	cancelSpeak   func()
	speakDone     chan struct{}
	speakRequests chan string

	// speaking is true from the first sentence of an assistant batch until
	// the sink has been flushed and the muter unmuted. Holding the muter
	// across the whole batch prevents self-echo during the gapless inter-
	// sentence playback introduced by the pipelined Speaker.
	speaking    bool
	flushing    bool
	cancelFlush func()
	flushDone   chan flushResult

	convBusy          bool
	pendingText       *string
	pendingTaskUpdate *taskUpdatePayload
	turnEnded         chan struct{}

	assistantBuf *assistantBuffer
}

type flushResult struct {
	err      error
	canceled bool
}

type taskUpdatePayload struct {
	turnID    string
	taskState agent.TaskStateSnapshot
}

// NewSession prepares a Session. The ChatAgent is initialized during
// Run(), not here.
func NewSession(_ context.Context, deps Deps) (*Session, error) {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}

	if deps.Muter == nil {
		deps.Muter = &audio.NoopMuter{}
	}

	s := &Session{
		deps:          deps,
		log:           log,
		speakDone:     make(chan struct{}, 1),
		speakRequests: make(chan string, 16),
		turnEnded:     make(chan struct{}, 1),
		flushDone:     make(chan flushResult, 1),
		assistantBuf:  &assistantBuffer{},
	}
	return s, nil
}

func (s *Session) Close() error {
	return s.deps.ChatAgent.Close()
}

func (s *Session) nextRequestID() string {
	return strconv.FormatUint(s.nextID.Add(1), 10)
}

// Run drives the call until ctx is canceled.
func (s *Session) Run(ctx context.Context) error {
	systemPrompt := BuildSystemPrompt(s.deps.Persona)

	projectCtx := Read(s.deps.RepoRoot)
	var toolSpecs []agent.ToolSpec
	if s.deps.Tools != nil {
		for _, t := range s.deps.Tools.Specs() {
			toolSpecs = append(toolSpecs, agent.ToolSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
	}
	if err := s.deps.ChatAgent.Init(ctx, agent.ChatConfig{
		SystemPrompt:   systemPrompt,
		ProjectContext: projectCtx,
		Model:          s.deps.Model,
		Tools:          toolSpecs,
	}); err != nil {
		return fmt.Errorf("chat agent init: %w", err)
	}

	utterances := s.deps.Transcriber.Utterances()
	wakeEvents := s.deps.WakewordEvents
	taskEvents := s.deps.TaskManager.Events()
	chatEvents := s.deps.ChatAgent.Events()
	speechOnsets := s.deps.SpeechOnsets
	if speechOnsets == nil {
		speechOnsets = make(chan struct{})
	}

	currentTaskState := s.deps.TaskManager.Status()

	greet := func() {
		if s.deps.Persona.Greeting != "" {
			s.speakSentence(s.deps.Persona.Greeting)
		}
	}

	for {
		var speakCh <-chan string
		if s.cancelSpeak == nil {
			speakCh = s.speakRequests
		}

		select {
		case <-ctx.Done():
			return nil

		case kind := <-wakeEvents:
			switch kind {
			case wakeword.KeywordWake:
				s.log.Info("wake word detected")
				if !s.awake {
					s.awake = true
					s.deps.Transcriber.Unmute()
					greet()
				}
			case wakeword.KeywordMute:
				s.log.Info("mute word detected")
				s.abortSpeakBatch()
				s.awake = false
				s.convBusy = false
				s.deps.Transcriber.Mute()
				s.log.Info("muted — waiting for wake word")
			case wakeword.KeywordStop:
				s.log.Info("stop word detected — shutting down")
				s.abortSpeakBatch()
				// Play the configured farewell before tearing down so the
				// user hears an acknowledgement of the stop keyword rather
				// than the process vanishing silently. Speak synchronously
				// via Speak + Flush — the queued-pipeline logic is overkill
				// for a single one-shot utterance during shutdown.
				if farewell := strings.TrimSpace(s.deps.Persona.Farewell); farewell != "" {
					s.log.Info("speaking farewell", "text", farewell)
					bye, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					if err := s.deps.Speaker.Speak(bye, farewell); err != nil {
						s.log.Error("farewell speak", "err", err)
					} else if err := s.deps.Speaker.Flush(bye); err != nil {
						s.log.Error("farewell flush", "err", err)
					}
					cancel()
				}
				s.deps.TaskManager.Cancel()
				s.deps.ChatAgent.Close()
				return nil
			}

		case text, ok := <-utterances:
			if !ok {
				utterances = nil
				continue
			}
			if !s.awake {
				continue
			}
			s.log.Info("heard", "text", text)
			if s.convBusy {
				s.log.Info("conv busy — buffering", "text", text)
				t := text
				s.pendingText = &t
				continue
			}
			s.dispatchUserSay(text, currentTaskState)

		case ev, ok := <-taskEvents:
			if !ok {
				taskEvents = nil
				continue
			}
			currentTaskState = ev.State
			s.log.Info("task state", "kind", ev.State.Kind.String())
			if ev.State.Kind == TaskStateDone || ev.State.Kind == TaskStateFailed || ev.State.Kind == TaskStateNeedsInput {
				snap := taskStateToSnapshot(ev.State)
				if s.convBusy {
					id := s.nextRequestID()
					s.pendingTaskUpdate = &taskUpdatePayload{turnID: id, taskState: snap}
				} else {
					s.dispatchTaskUpdate(snap)
				}
			}

		case ev, ok := <-chatEvents:
			if !ok {
				chatEvents = nil
				continue
			}
			switch ev.Type {
			case "assistant_say":
				s.handleAssistantSay(ev)
			case "tool_call":
				s.handleToolCall(ev)
			case "turn_end":
				s.handleTurnEnd(ev)
			case "error":
				s.log.Error("chat agent error", "msg", ev.Error)
			}

		case <-s.turnEnded:
			s.convBusy = false
			if s.pendingTaskUpdate != nil {
				p := s.pendingTaskUpdate
				s.pendingTaskUpdate = nil
				s.dispatchTaskUpdate(p.taskState)
			} else if s.pendingText != nil {
				text := *s.pendingText
				s.pendingText = nil
				s.log.Info("conv free — dispatching pending", "text", text)
				s.dispatchUserSay(text, currentTaskState)
			} else {
				// Assistant turn has fully ended and no new turn is queued.
				// If the speak batch is also idle, this is the moment to
				// flush the sink and unmute the mic.
				s.maybeFinalizeSpeakBatch()
			}

		case text := <-speakCh:
			// If a flush is mid-flight and a new sentence arrives, abort
			// the flush and keep the muter held — we're still speaking.
			if s.flushing {
				s.cancelFlush()
			}
			if !s.speaking {
				s.deps.Muter.Mute()
				s.speaking = true
			}
			s.startSpeak(text)

		case <-s.speakDone:
			s.cancelSpeak = nil
			s.maybeFinalizeSpeakBatch()

		case res := <-s.flushDone:
			s.flushing = false
			s.cancelFlush = nil
			if res.canceled {
				// New sentence arrived mid-flush; batch continues and the
				// muter stays held. A later speakDone will retry finalize.
				continue
			}
			if s.speaking {
				s.deps.Muter.Unmute()
				s.speaking = false
			}

		case <-speechOnsets:
			if s.speaking {
				s.log.Info("barge-in")
				s.abortSpeakBatch()
				s.markTurnCanceled()
			}
		}
	}
}

func (s *Session) dispatchTaskUpdate(snap agent.TaskStateSnapshot) {
	s.convBusy = true
	id := s.nextRequestID()
	s.setTurn(id)
	if err := s.deps.ChatAgent.TaskUpdate(id, snap); err != nil {
		s.convBusy = false
		s.log.Error("chat agent task_update", "err", err)
	}
}

func (s *Session) dispatchUserSay(text string, state TaskState) {
	s.convBusy = true
	s.pendingText = nil
	id := s.nextRequestID()
	s.setTurn(id)
	snap := taskStateToSnapshot(state)
	if err := s.deps.ChatAgent.Say(id, text, snap); err != nil {
		s.convBusy = false
		s.log.Error("chat agent say", "err", err)
	}
}

func taskStateToSnapshot(s TaskState) agent.TaskStateSnapshot {
	return agent.TaskStateSnapshot{
		State:       s.Kind.String(),
		Question:    s.Question,
		Summary:     s.Summary,
		Message:     s.Message,
		ActivityLog: s.ActivityLog,
	}
}

func (s *Session) setTurn(id string) {
	s.turnMu.Lock()
	s.currentTurnID = id
	s.turnCanceled = false
	s.fillerSpoken = false
	s.turnMu.Unlock()
}

// claimFillerSlot atomically reports whether the caller is the first to
// want to speak a filler on the current turn. Returns true exactly once
// per turn.
func (s *Session) claimFillerSlot() bool {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.fillerSpoken {
		return false
	}
	s.fillerSpoken = true
	return true
}

func (s *Session) markTurnCanceled() {
	s.turnMu.Lock()
	s.turnCanceled = true
	s.turnMu.Unlock()
}

func (s *Session) turnActive(id string) bool {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return id == s.currentTurnID && !s.turnCanceled
}

// assistantBuffer accumulates streamed assistant text and splits it into
// spoken sentences on newline boundaries. The system prompt instructs the
// model to put each sentence on its own line, so a newline means "this
// sentence is complete, start synthesizing."
//
// To cut first-sentence latency, the very first utterance of each turn is
// allowed to flush early at a clause break (",", ";", ":") once the buffer
// is long enough to sound natural. Subsequent sentences fall through to
// the newline path so rhythm stays intact.
type assistantBuffer struct {
	mu              sync.Mutex
	buf             strings.Builder
	firstEmitted    bool
}

// earlyFlushMinChars is the minimum prefix length before a clause break is
// considered for early flushing. Avoids emitting tiny fragments like "Sure,".
const earlyFlushMinChars = 30

// earlyFlushMaxChars caps how far we scan for a clause break. Past this we
// wait for a full sentence rather than dice a long clause.
const earlyFlushMaxChars = 90

func (b *assistantBuffer) appendAndFlush(chunk string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(chunk)
	full := b.buf.String()

	var out []string

	if !b.firstEmitted {
		if cut := findEarlyClauseBreak(full); cut > 0 {
			if seg := strings.TrimSpace(full[:cut]); seg != "" {
				out = append(out, seg)
				b.firstEmitted = true
			}
			b.buf.Reset()
			b.buf.WriteString(full[cut:])
			full = b.buf.String()
		}
	}

	idx := strings.LastIndexByte(full, '\n')
	if idx < 0 {
		return out
	}
	for _, line := range strings.Split(full[:idx], "\n") {
		if seg := strings.TrimSpace(line); seg != "" {
			out = append(out, seg)
			b.firstEmitted = true
		}
	}
	rem := full[idx+1:]
	b.buf.Reset()
	b.buf.WriteString(rem)
	return out
}

func (b *assistantBuffer) drain() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := strings.TrimSpace(b.buf.String())
	b.buf.Reset()
	b.firstEmitted = false
	return out
}

// findEarlyClauseBreak returns the byte index just after the first clause
// break (comma / semicolon / colon followed by whitespace) within the
// earlyFlush bounds, or -1 if none yet. Returning >0 means the caller
// should flush everything up to (but not including) that index.
func findEarlyClauseBreak(s string) int {
	if len(s) < earlyFlushMinChars {
		return -1
	}
	limit := len(s)
	if limit > earlyFlushMaxChars {
		limit = earlyFlushMaxChars
	}
	for i := earlyFlushMinChars; i < limit-1; i++ {
		c := s[i]
		if c != ',' && c != ';' && c != ':' {
			continue
		}
		n := s[i+1]
		if n == ' ' || n == '\t' || n == '\n' {
			return i + 1
		}
	}
	return -1
}

func (s *Session) handleAssistantSay(ev agent.ChatEvent) {
	if !s.turnActive(ev.ID) {
		return
	}
	sentences := s.assistantBuf.appendAndFlush(ev.Text)
	for _, sent := range sentences {
		s.speakSentence(sent)
	}
}

func (s *Session) handleTurnEnd(ev agent.ChatEvent) {
	if !s.turnActive(ev.ID) {
		s.assistantBuf.drain()
	} else if rest := s.assistantBuf.drain(); rest != "" {
		s.speakSentence(rest)
	}
	select {
	case s.turnEnded <- struct{}{}:
	default:
	}
}

func (s *Session) speakSentence(text string) {
	select {
	case s.speakRequests <- text:
	default:
		s.log.Warn("speakRequests buffer full, dropping sentence", "text", text)
	}
}

func (s *Session) startSpeak(text string) {
	if s.cancelSpeak != nil {
		s.cancelSpeak()
	}
	s.log.Info("speaking", "text", text)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelSpeak = cancel
	go func() {
		_ = s.deps.Speaker.Speak(ctx, text)
		cancel()
		select {
		case s.speakDone <- struct{}{}:
		default:
		}
	}()
}

// maybeFinalizeSpeakBatch kicks off a sink drain + unmute if the assistant
// batch is quiescent: currently speaking, no active Speak goroutine, no
// queued sentences, no in-flight chat turn, and not already flushing.
// Called from both speakDone and turnEnded handlers since either event
// can be the one that makes the batch quiescent.
func (s *Session) maybeFinalizeSpeakBatch() {
	if !s.speaking || s.flushing {
		return
	}
	if s.cancelSpeak != nil {
		return
	}
	if len(s.speakRequests) > 0 {
		return
	}
	if s.convBusy {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFlush = cancel
	s.flushing = true
	go func() {
		err := s.deps.Speaker.Flush(ctx)
		canceled := ctx.Err() != nil
		s.flushDone <- flushResult{err: err, canceled: canceled}
	}()
}

// abortSpeakBatch immediately stops speaking: cancels any active Speak,
// clears queued sentences, empties the audio sink, cancels any in-flight
// flush, and unmutes the mic. Used for barge-in, mute, and stop.
func (s *Session) abortSpeakBatch() {
	if s.cancelSpeak != nil {
		s.cancelSpeak()
		s.cancelSpeak = nil
	}
	if s.flushing {
		s.cancelFlush()
		// flushDone will arrive later with canceled=true and is ignored
		// because s.speaking is cleared below.
	}
	s.deps.Speaker.Cancel()
	// Drain queued speak requests so the next batch starts clean.
	for {
		select {
		case <-s.speakRequests:
		default:
			goto drained
		}
	}
drained:
	if s.speaking {
		s.deps.Muter.Unmute()
		s.speaking = false
	}
}

func (s *Session) handleToolCall(ev agent.ChatEvent) {
	go func() {
		// Pre-announce slow MD tools so the user hears a filler instead
		// of dead air while the tool runs and the LLM re-decodes. Task
		// tools are skipped because the model already narrates those
		// ("Looking at the sidecar directory now.") before emitting the
		// start_task call.
		if phrase := toolFillerPhrase(ev.Name); phrase != "" && s.claimFillerSlot() {
			s.speakSentence(phrase)
		}
		result := s.runTool(ev.Name, ev.Args)
		raw, _ := json.Marshal(result)
		_ = s.deps.ChatAgent.ToolResult(ev.CallID, raw)
	}()
}

// toolFillerVariants is the pool of casual acknowledgements played at the
// start of a tool call. Keeping 3–4 short variants per tool stops the
// response from feeling canned over a long conversation.
var toolFillerVariants = map[string][]string{
	"web_search": {
		"One sec, let me check.",
		"Hold on, looking.",
		"Hmm, lemme see.",
		"Give me a second.",
	},
	"web_fetch": {
		"Hang on.",
		"One moment.",
		"Reading that.",
	},
	"read_file": {
		"Hold on.",
		"One sec.",
		"Let me look at that.",
	},
	"file_search": {
		"Lemme find it.",
		"Hold on, searching.",
		"One sec.",
	},
	"screenshot": {
		"Let me look.",
		"Checking your screen.",
		"One sec.",
	},
	"system_stats": {
		"Hold on.",
		"One sec.",
		"Let me check.",
	},
}

// toolFillerPhrase returns a casual acknowledgement for a tool call, or
// the empty string for fast tools and task tools (which the model already
// narrates itself).
func toolFillerPhrase(name string) string {
	variants, ok := toolFillerVariants[name]
	if !ok || len(variants) == 0 {
		return ""
	}
	return variants[rand.Intn(len(variants))]
}

type toolOk struct {
	Ok bool `json:"ok"`
}
type toolErr struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
}

func (s *Session) runTool(name string, args json.RawMessage) any {
	switch name {
	case "start_task":
		var obj Objective
		if err := json.Unmarshal(args, &obj); err != nil {
			return toolErr{Ok: false, Error: fmt.Sprintf("bad args: %v", err)}
		}
		if s.deps.ModelResolver != nil {
			obj.ModelHint = s.deps.ModelResolver(obj.ModelHint)
		}
		if err := s.deps.TaskManager.Start(context.Background(), obj); err != nil {
			return toolErr{Ok: false, Error: err.Error()}
		}
		return toolOk{Ok: true}

	case "reply_to_task":
		var a struct {
			Answer string `json:"answer"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return toolErr{Ok: false, Error: fmt.Sprintf("bad args: %v", err)}
		}
		if err := s.deps.TaskManager.Reply(a.Answer); err != nil {
			return toolErr{Ok: false, Error: err.Error()}
		}
		return toolOk{Ok: true}

	case "cancel_task":
		if err := s.deps.TaskManager.Cancel(); err != nil {
			return toolErr{Ok: false, Error: err.Error()}
		}
		return toolOk{Ok: true}

	case "task_status":
		st := s.deps.TaskManager.Status()
		snap := taskStateToSnapshot(st)
		return snap

	default:
		if s.deps.Tools != nil && s.deps.Tools.Has(name) {
			start := time.Now()
			s.log.Info("mdtool call", "name", name, "args", string(args))
			res := s.deps.Tools.Run(context.Background(), name, args)
			dur := time.Since(start).Milliseconds()
			if res.Ok {
				out := res.Output
				if len(out) > 200 {
					out = out[:200] + "…"
				}
				s.log.Info("mdtool result", "name", name, "ok", true, "duration_ms", dur, "output_preview", out)
			} else {
				s.log.Info("mdtool result", "name", name, "ok", false, "duration_ms", dur, "error", res.Error)
			}
			return res
		}
		return toolErr{Ok: false, Error: "unknown tool: " + name}
	}
}
