package conv

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	Transcriber  Transcriber
	Speaker      Speaker
	Hotkey       Hotkey
	SpeechOnsets <-chan struct{}
	TaskManager  *TaskManager

	RepoRoot     string // used to locate sidecar/conv-sidecar.ts
	Model        string // e.g. "claude-haiku-4-5"
	SystemPrompt string // chat system prompt (see DefaultSystemPrompt)
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
- Spawn a background coding task by calling the start_task tool. Use this when the user clearly asks you to build, fix, refactor, or implement something concrete. Do not use it for questions or chat.
- Forward the user's answer to a running task by calling reply_to_task. The task is asking when the background task state shows needs_input.
- Cancel a task with cancel_task only when the user explicitly says to stop.

ON BACKGROUND TASKS:
- Each user turn includes a [background task: …] line at the top describing the task's current state. If the state has new information (the task finished, failed, or needs an answer), weave it naturally into your reply — don't ignore it. Do not mention the bracketed line literally.
- Tasks run in parallel with the conversation; don't wait for them.
`

// Session is the conv-layer event loop. It owns the conv-sidecar
// subprocess and routes between mic, sidecar, taskmgr, and speaker.
type Session struct {
	deps Deps
	log  *slog.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu      sync.Mutex
	pending map[string]chan inboundMsg
	closed  bool

	nextID atomic.Uint64

	// The current in-flight assistant turn id. Receiving an
	// assistant_say with an id != currentTurnID is dropped.
	turnMu        sync.Mutex
	currentTurnID string
	turnCanceled  bool

	// speak goroutine state, owned by Run.
	cancelSpeak  func()
	speakDone    chan struct{}
	speakRequests chan string

	// one assistantBuffer per session; reset on each turn start.
	assistantBuf *assistantBuffer

	wg sync.WaitGroup
}

// NewSession spawns conv-sidecar and prepares the Session. Run() must
// be called to drive the event loop.
func NewSession(ctx context.Context, deps Deps) (*Session, error) {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	if deps.SystemPrompt == "" {
		deps.SystemPrompt = DefaultSystemPrompt
	}
	if deps.Model == "" {
		deps.Model = "claude-haiku-4-5"
	}

	scriptPath := filepath.Join(deps.RepoRoot, "sidecar", "conv-sidecar.ts")
	cmd := exec.CommandContext(ctx, "bun", "run", scriptPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("conv stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("conv stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("conv start: %w", err)
	}

	s := &Session{
		deps:          deps,
		log:           log,
		cmd:           cmd,
		stdin:         stdin,
		stdout:        stdout,
		pending:       make(map[string]chan inboundMsg),
		speakDone:     make(chan struct{}, 1),
		speakRequests: make(chan string, 16),
		assistantBuf:  &assistantBuffer{},
	}

	s.wg.Add(1)
	go s.readLoop()

	// Send init and wait for ready.
	projectCtx := Read(deps.RepoRoot)
	readyCh := s.registerWait("__ready__")
	if err := s.send(initMsg{
		Type:           "init",
		SystemPrompt:   deps.SystemPrompt,
		ProjectContext: projectCtx,
		Model:          deps.Model,
	}); err != nil {
		s.Close()
		return nil, fmt.Errorf("conv send init: %w", err)
	}
	select {
	case <-readyCh:
		// good
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}

	return s, nil
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	_ = s.send(shutdownMsg{Type: "shutdown"})
	_ = s.stdin.Close()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	if rc, ok := any(s.stdout).(io.Closer); ok {
		_ = rc.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Session) send(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("conv: closed")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = s.stdin.Write(b)
	return err
}

func (s *Session) nextRequestID() string {
	return strconv.FormatUint(s.nextID.Add(1), 10)
}

func (s *Session) registerWait(id string) chan inboundMsg {
	ch := make(chan inboundMsg, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	return ch
}

// readLoop demultiplexes messages from conv-sidecar.
func (s *Session) readLoop() {
	defer s.wg.Done()
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg inboundMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			s.log.Error("conv: bad json from sidecar", "err", err)
			continue
		}
		switch msg.Type {
		case "ready":
			s.mu.Lock()
			ch, ok := s.pending["__ready__"]
			if ok {
				delete(s.pending, "__ready__")
			}
			s.mu.Unlock()
			if ok {
				ch <- msg
			}
		case "assistant_say":
			s.handleAssistantSay(msg)
		case "tool_call":
			s.handleToolCall(msg)
		case "turn_end":
			s.handleTurnEnd(msg)
		case "error":
			s.log.Error("conv sidecar error", "msg", msg.Message)
		}
	}
}

// Run drives the call until ctx is canceled. Spawns goroutines for
// streaming TTS and background task event delivery.
func (s *Session) Run(ctx context.Context) error {
	utterances := s.deps.Transcriber.Utterances()
	hotkeys := s.deps.Hotkey.Events()
	taskEvents := s.deps.TaskManager.Events()
	speechOnsets := s.deps.SpeechOnsets
	if speechOnsets == nil {
		speechOnsets = make(chan struct{})
	}

	// Latest task state observed via TaskManager.Events; used when assembling
	// each user_say so the LLM sees current state in its prompt.
	currentTaskState := s.deps.TaskManager.Status()

	// On hotkey, send a synthetic seed user_say so the LLM produces a greeting.
	greet := func() {
		s.dispatchUserSay("<call started>", currentTaskState)
	}

	for {
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
			s.dispatchUserSay(text, currentTaskState)

		case ev, ok := <-taskEvents:
			if !ok {
				taskEvents = nil
				continue
			}
			currentTaskState = ev.State
			s.log.Info("task state", "kind", ev.State.Kind.String())

		case text := <-s.speakRequests:
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

func (s *Session) dispatchUserSay(text string, state TaskState) {
	id := s.nextRequestID()
	s.setTurn(id)
	payload := userSayMsg{
		Type:      "user_say",
		ID:        id,
		Text:      text,
		TaskState: taskStateToPayload(state),
	}
	if err := s.send(payload); err != nil {
		s.log.Error("conv send user_say", "err", err)
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

// assistantBuf accumulates streaming text; we flush on sentence boundary
// or turn end. Owned by handleAssistantSay/handleTurnEnd which run on
// the readLoop goroutine.
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

func (s *Session) handleAssistantSay(msg inboundMsg) {
	if !s.turnActive(msg.ID) {
		return
	}
	sentences := s.assistantBuf.appendAndFlush(msg.Text)
	for _, sent := range sentences {
		s.speakSentence(sent)
	}
}

func (s *Session) handleTurnEnd(msg inboundMsg) {
	if !s.turnActive(msg.ID) {
		s.assistantBuf.drain()
		return
	}
	if rest := s.assistantBuf.drain(); rest != "" {
		s.speakSentence(rest)
	}
}

// speakSentence enqueues text onto speakRequests for the Run goroutine
// to process. If the buffer is full the sentence is dropped with a warning.
func (s *Session) speakSentence(text string) {
	select {
	case s.speakRequests <- text:
	default:
		s.log.Warn("speakRequests buffer full, dropping sentence", "text", text)
	}
}

// startSpeak is called exclusively from the Run goroutine. It cancels any
// in-flight speech, then launches a new Speak goroutine for text.
// cancelSpeak is only ever written here, so reads in the same goroutine
// (speakDone and speechOnsets cases) are race-free.
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

func (s *Session) handleToolCall(msg inboundMsg) {
	go func() {
		result := s.runTool(msg.Name, msg.Args)
		raw, _ := json.Marshal(result)
		_ = s.send(toolResultMsg{
			Type:   "tool_result",
			CallID: msg.CallID,
			Result: raw,
		})
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
		return taskStateToPayload(st)

	default:
		return toolErr{Ok: false, Error: "unknown tool: " + name}
	}
}
