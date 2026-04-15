package pm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/Renderix/freeman/internal/call"
)

// Config configures the PiPM client.
type Config struct {
	// Command is the executable to spawn (e.g. "bun").
	Command string
	// Args are the arguments for the command (e.g. ["run", ".../pm-sidecar.ts"]).
	Args []string
	// Model is the Anthropic model id passed to pi-coding-agent
	// (e.g. "claude-haiku-4-5").
	Model string
	// ConfidenceThreshold upgrades low-confidence answer_inline to
	// escalate. Defaults to 0.8 when zero.
	ConfidenceThreshold float64
}

// PiPM implements call.PM by talking JSONL to a long-running
// pm-sidecar.ts subprocess running under pi-coding-agent's
// subscription auth.
type PiPM struct {
	cfg Config

	stdin   io.WriteCloser
	stdout  io.ReadCloser
	cmd     *exec.Cmd // nil in test (pipes injected)

	mu      sync.Mutex
	pending map[string]chan inboundMsg
	nextID  atomic.Uint64
	closed  bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// inboundMsg is a decoded response from the sidecar.
type inboundMsg struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	NeedsMore      bool            `json:"needs_more"`
	Question       string          `json:"question"`
	Objective      *piObjective    `json:"objective,omitempty"`
	AnswerInline   string          `json:"answer_inline,omitempty"`
	Confidence     float64         `json:"confidence,omitempty"`
	SpokenQuestion string          `json:"spoken_question,omitempty"`
	Message        string          `json:"message,omitempty"`
	Raw            json.RawMessage `json:"-"`
}

type piObjective struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	ModelHint          string   `json:"model_hint"`
	SpokenSummary      string   `json:"spoken_summary"`
}

// New spawns the pm-sidecar subprocess and returns a connected PiPM.
func New(ctx context.Context, cfg Config) (*PiPM, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("pm: Command is required")
	}
	if cfg.ConfidenceThreshold == 0 {
		cfg.ConfidenceThreshold = 0.8
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pm: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pm: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pm: start: %w", err)
	}

	p := newFromPipes(stdin, stdout, cfg)
	p.cmd = cmd
	return p, nil
}

// NewFromPipes wires a PiPM to pre-existing pipes (for tests).
func NewFromPipes(stdin io.WriteCloser, stdout io.ReadCloser, cfg Config) *PiPM {
	if cfg.ConfidenceThreshold == 0 {
		cfg.ConfidenceThreshold = 0.8
	}
	return newFromPipes(stdin, stdout, cfg)
}

func newFromPipes(stdin io.WriteCloser, stdout io.ReadCloser, cfg Config) *PiPM {
	ctx, cancel := context.WithCancel(context.Background())
	p := &PiPM{
		cfg:     cfg,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[string]chan inboundMsg),
		ctx:     ctx,
		cancel:  cancel,
	}
	p.wg.Add(1)
	go p.readLoop()
	return p
}

// Close tears down the subprocess.
func (p *PiPM) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.wg.Wait()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	p.cancel()
	_ = p.stdin.Close()
	if p.cmd != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	if rc, ok := any(p.stdout).(io.Closer); ok {
		_ = rc.Close()
	}
	p.wg.Wait()
	return nil
}

func (p *PiPM) readLoop() {
	defer p.wg.Done()
	scanner := bufio.NewScanner(p.stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg inboundMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		p.mu.Lock()
		ch, ok := p.pending[msg.ID]
		if ok {
			delete(p.pending, msg.ID)
		}
		p.mu.Unlock()
		if ok {
			select {
			case ch <- msg:
			case <-p.ctx.Done():
				return
			}
		}
	}
}

func (p *PiPM) send(v any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("pm: closed")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = p.stdin.Write(b)
	return err
}

func (p *PiPM) nextRequestID() string {
	return strconv.FormatUint(p.nextID.Add(1), 10)
}

func (p *PiPM) register(id string) chan inboundMsg {
	ch := make(chan inboundMsg, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	return ch
}

func (p *PiPM) unregister(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

// Intake implements call.PM.
func (p *PiPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error) {
	id := p.nextRequestID()
	ch := p.register(id)
	defer p.unregister(id)

	cmd := struct {
		Type            string `json:"type"`
		ID              string `json:"id"`
		UserText        string `json:"user_text"`
		InterruptedText string `json:"interrupted_text,omitempty"`
		Model           string `json:"model"`
	}{
		Type:            "intake",
		ID:              id,
		UserText:        in.Latest,
		InterruptedText: in.InterruptedText,
		Model:           p.cfg.Model,
	}
	if err := p.send(cmd); err != nil {
		return call.PMIntakeResult{}, fmt.Errorf("pm: send intake: %w", err)
	}

	select {
	case <-ctx.Done():
		return call.PMIntakeResult{}, ctx.Err()
	case <-p.ctx.Done():
		return call.PMIntakeResult{}, fmt.Errorf("pm: closed")
	case msg := <-ch:
		if msg.Type == "error" {
			return call.PMIntakeResult{}, fmt.Errorf("pm: %s", msg.Message)
		}
		if msg.NeedsMore {
			return call.PMIntakeResult{NeedsMore: true, Question: msg.Question}, nil
		}
		if msg.Objective == nil {
			return call.PMIntakeResult{NeedsMore: true, Question: "sorry, one moment."}, nil
		}
		return call.PMIntakeResult{
			NeedsMore: false,
			Objective: &call.Objective{
				Goal:               msg.Objective.Goal,
				AcceptanceCriteria: msg.Objective.AcceptanceCriteria,
				Constraints:        msg.Objective.Constraints,
				Notes:              msg.Objective.Notes,
				ModelHint:          msg.Objective.ModelHint,
				SpokenSummary:      msg.Objective.SpokenSummary,
			},
		}, nil
	}
}

// Route implements call.PM.
func (p *PiPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error) {
	id := p.nextRequestID()
	ch := p.register(id)
	defer p.unregister(id)

	cmd := struct {
		Type            string `json:"type"`
		ID              string `json:"id"`
		ObjectiveGoal   string `json:"objective_goal"`
		Question        string `json:"question"`
		InterruptedText string `json:"interrupted_text,omitempty"`
		Model           string `json:"model"`
	}{
		Type:            "route",
		ID:              id,
		ObjectiveGoal:   in.Objective.Goal,
		Question:        in.Question,
		InterruptedText: in.InterruptedText,
		Model:           p.cfg.Model,
	}
	if err := p.send(cmd); err != nil {
		// On transport error, escalate with the raw question.
		return call.PMRouteResult{SpokenQuestion: in.Question}, nil
	}

	select {
	case <-ctx.Done():
		return call.PMRouteResult{}, ctx.Err()
	case <-p.ctx.Done():
		return call.PMRouteResult{SpokenQuestion: in.Question}, nil
	case msg := <-ch:
		if msg.Type == "error" {
			return call.PMRouteResult{SpokenQuestion: in.Question}, nil
		}
		if msg.AnswerInline != "" {
			if msg.Confidence < p.cfg.ConfidenceThreshold {
				return call.PMRouteResult{SpokenQuestion: in.Question}, nil
			}
			return call.PMRouteResult{AnswerInline: msg.AnswerInline}, nil
		}
		if msg.SpokenQuestion != "" {
			return call.PMRouteResult{SpokenQuestion: msg.SpokenQuestion}, nil
		}
		return call.PMRouteResult{SpokenQuestion: in.Question}, nil
	}
}

// Reset implements call.PM.
func (p *PiPM) Reset() {
	_ = p.send(map[string]any{"type": "reset"})
}
