package conv

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Renderix/freeman/internal/agent"
	"github.com/Renderix/freeman/internal/audio"
	"github.com/Renderix/freeman/internal/audio/wakeword"
	"github.com/Renderix/freeman/internal/config"
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
	if err := s.deps.ChatAgent.Init(ctx, agent.ChatConfig{
		SystemPrompt:   systemPrompt,
		ProjectContext: projectCtx,
		Model:          s.deps.Model,
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
	s.turnMu.Unlock()
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
// sentence is complete, start synthesizing." Anything after the last
// newline is held until the next chunk or until drain() runs at turn end.
type assistantBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *assistantBuffer) appendAndFlush(chunk string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(chunk)
	full := b.buf.String()

	idx := strings.LastIndexByte(full, '\n')
	if idx < 0 {
		return nil
	}

	var out []string
	for _, line := range strings.Split(full[:idx], "\n") {
		if seg := strings.TrimSpace(line); seg != "" {
			out = append(out, seg)
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
	return out
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
		result := s.runTool(ev.Name, ev.Args)
		raw, _ := json.Marshal(result)
		_ = s.deps.ChatAgent.ToolResult(ev.CallID, raw)
	}()
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
		return toolErr{Ok: false, Error: "unknown tool: " + name}
	}
}
