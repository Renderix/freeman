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

	"github.com/Renderix/freeman/internal/agent"
)

// Transcriber is the subset of stt.Transcriber the conv layer needs.
type Transcriber interface {
	Utterances() <-chan string
	Stop()
}

// Speaker is the subset of playback.Speaker the conv layer needs.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// Hotkey is the subset of hotkey.Hotkey the conv layer needs.
type Hotkey interface {
	Events() <-chan struct{}
	Stop()
}

// Deps are the runtime dependencies injected into a Session.
type Deps struct {
	Transcriber   Transcriber
	Speaker       Speaker
	Hotkey        Hotkey
	SpeechOnsets  <-chan struct{}
	TaskManager   *TaskManager
	ChatAgent     agent.ChatAgent
	ModelResolver func(hint string) string

	SystemPrompt string // chat system prompt (see DefaultSystemPrompt)
	Model        string // e.g. "claude-haiku-4-5"
	RepoRoot     string // used to read project context
	Logger       *slog.Logger
}

// DefaultSystemPrompt is the voice-tuned chat system prompt used when
// Deps.SystemPrompt is empty.
const DefaultSystemPrompt = `You are Freeman, a voice assistant on a phone call with the user.

ABSOLUTE RULES:
- Replies are spoken aloud by text-to-speech. Never use markdown, asterisks, bullets, code fences, or line breaks.
- Keep responses to one or two casual spoken sentences unless the user asks for detail.
- The user can interrupt you mid-sentence. If they do, respond to the new thing without apologising.

WHAT YOU CAN DO:
- Chat about general topics using your knowledge.
- Answer questions about this specific project using the project context provided below. If the project context doesn't have what you need, say so honestly — do not make things up.
- Spawn a background task by calling the start_task tool. Use this whenever the user asks you to do anything that touches the codebase — building, fixing, investigating, checking git status, reading files, running commands, or answering questions about the code. This is your only way to interact with the repo. Only skip it for pure chat that needs no codebase access.
- Forward the user's answer to a running task by calling reply_to_task. The task is asking when the background task state shows needs_input.
- Cancel a task with cancel_task only when the user explicitly says to stop.

ON BACKGROUND TASKS:
- Each user turn includes a [background task: …] line at the top describing the task's current state. If the state has new information (the task finished, failed, or needs an answer), weave it naturally into your reply — don't ignore it. Do not mention the bracketed line literally.
- Tasks run in parallel with the conversation; don't wait for them.
`

// Session is the conv-layer event loop. It routes between mic,
// ChatAgent, TaskManager, and speaker.
type Session struct {
	deps Deps
	log  *slog.Logger

	nextID atomic.Uint64

	turnMu        sync.Mutex
	currentTurnID string
	turnCanceled  bool

	cancelSpeak   func()
	speakDone     chan struct{}
	speakRequests chan string

	convBusy          bool
	pendingText       *string
	pendingTaskUpdate *taskUpdatePayload
	turnEnded         chan struct{}

	assistantBuf *assistantBuffer
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
	if deps.SystemPrompt == "" {
		deps.SystemPrompt = DefaultSystemPrompt
	}

	s := &Session{
		deps:          deps,
		log:           log,
		speakDone:     make(chan struct{}, 1),
		speakRequests: make(chan string, 16),
		turnEnded:     make(chan struct{}, 1),
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
	projectCtx := Read(s.deps.RepoRoot)
	if err := s.deps.ChatAgent.Init(ctx, agent.ChatConfig{
		SystemPrompt:   s.deps.SystemPrompt,
		ProjectContext: projectCtx,
		Model:          s.deps.Model,
	}); err != nil {
		return fmt.Errorf("chat agent init: %w", err)
	}

	utterances := s.deps.Transcriber.Utterances()
	hotkeys := s.deps.Hotkey.Events()
	taskEvents := s.deps.TaskManager.Events()
	chatEvents := s.deps.ChatAgent.Events()
	speechOnsets := s.deps.SpeechOnsets
	if speechOnsets == nil {
		speechOnsets = make(chan struct{})
	}

	currentTaskState := s.deps.TaskManager.Status()

	greet := func() {
		if s.convBusy {
			s.log.Info("conv busy — ignoring hotkey")
			return
		}
		for {
			select {
			case _, ok := <-utterances:
				if !ok {
					utterances = nil
				}
				continue
			default:
			}
			break
		}
		s.dispatchUserSay("<call started>", currentTaskState)
	}

	for {
		var speakCh <-chan string
		if s.cancelSpeak == nil {
			speakCh = s.speakRequests
		}

		select {
		case <-ctx.Done():
			return nil

		case <-hotkeys:
			s.log.Info("hotkey pressed")
			greet()

		case text, ok := <-utterances:
			if !ok {
				utterances = nil
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
			}

		case text := <-speakCh:
			s.startSpeak(text)

		case <-s.speakDone:
			s.cancelSpeak = nil

		case <-speechOnsets:
			if s.cancelSpeak != nil {
				s.log.Info("barge-in")
				s.cancelSpeak()
				s.cancelSpeak = nil
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

var sentenceTerminators = ".!?"

type assistantBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *assistantBuffer) appendAndFlush(chunk string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(chunk)
	full := b.buf.String()

	var out []string
	last := 0
	for i := 0; i < len(full); i++ {
		c := full[i]
		if strings.IndexByte(sentenceTerminators, c) >= 0 {
			seg := strings.TrimSpace(full[last : i+1])
			if seg != "" {
				out = append(out, seg)
			}
			last = i + 1
		}
	}
	if last > 0 {
		rem := full[last:]
		b.buf.Reset()
		b.buf.WriteString(rem)
	}
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
