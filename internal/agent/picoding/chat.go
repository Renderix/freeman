package picoding

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/Renderix/freeman/internal/agent"
	"github.com/Renderix/freeman/internal/sidecar"
)

// ChatAgent is a long-lived chat session backed by conv-sidecar.ts.
type ChatAgent struct {
	repoRoot string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu     sync.Mutex
	closed bool
	events chan agent.ChatEvent
	wg     sync.WaitGroup
}

func NewChatAgent(repoRoot string) *ChatAgent {
	return &ChatAgent{
		repoRoot: repoRoot,
		events:   make(chan agent.ChatEvent, 32),
	}
}

func (a *ChatAgent) Init(ctx context.Context, cfg agent.ChatConfig) error {
	scriptPath := filepath.Join(a.repoRoot, "sidecar", "conv-sidecar.ts")
	a.cmd = exec.CommandContext(ctx, "bun", "run", scriptPath)
	var err error
	a.stdin, err = a.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("conv stdin: %w", err)
	}
	a.stdout, err = a.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("conv stdout: %w", err)
	}
	// Route conversation-sidecar stderr through a prefix writer so its lines
	// are labelled "[sidecar:conv]" and don't appear as bare, untagged text
	// mixed in with Freeman's own structured slog output.
	a.cmd.Stderr = sidecar.NewLinePrefixWriter(os.Stderr, "[sidecar:conv]")
	if err := a.cmd.Start(); err != nil {
		return fmt.Errorf("conv start: %w", err)
	}

	readyCh := make(chan struct{}, 1)
	a.wg.Add(1)
	go a.readLoop(readyCh)

	if err := a.send(initMsg{
		Type:           "init",
		SystemPrompt:   cfg.SystemPrompt,
		ProjectContext: cfg.ProjectContext,
		Model:          cfg.Model,
	}); err != nil {
		a.Close()
		return fmt.Errorf("conv send init: %w", err)
	}

	select {
	case <-readyCh:
		return nil
	case <-ctx.Done():
		a.Close()
		return ctx.Err()
	}
}

func (a *ChatAgent) Say(turnID, text string, taskState agent.TaskStateSnapshot) error {
	return a.send(userSayMsg{
		Type:      "user_say",
		ID:        turnID,
		Text:      text,
		TaskState: snapshotToPayload(taskState),
	})
}

func (a *ChatAgent) TaskUpdate(turnID string, taskState agent.TaskStateSnapshot) error {
	return a.send(taskUpdateMsg{
		Type:      "task_update",
		ID:        turnID,
		TaskState: snapshotToPayload(taskState),
	})
}

func (a *ChatAgent) ToolResult(callID string, result json.RawMessage) error {
	return a.send(toolResultMsg{
		Type:   "tool_result",
		CallID: callID,
		Result: result,
	})
}

func (a *ChatAgent) Events() <-chan agent.ChatEvent { return a.events }

func (a *ChatAgent) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		a.wg.Wait()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	_ = a.send(shutdownMsg{Type: "shutdown"})
	_ = a.stdin.Close()
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
		_ = a.cmd.Wait()
	}
	if rc, ok := any(a.stdout).(io.Closer); ok {
		_ = rc.Close()
	}
	a.wg.Wait()
	return nil
}

func (a *ChatAgent) send(v any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fmt.Errorf("conv: closed")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = a.stdin.Write(b)
	return err
}

func (a *ChatAgent) readLoop(readyCh chan<- struct{}) {
	defer a.wg.Done()
	defer close(a.events)
	scanner := bufio.NewScanner(a.stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	readySignaled := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg inboundMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "ready":
			if !readySignaled {
				readySignaled = true
				close(readyCh)
			}
		case "assistant_say", "tool_call", "turn_end", "error":
			ev := agent.ChatEvent{
				Type:   msg.Type,
				ID:     msg.ID,
				Text:   msg.Text,
				CallID: msg.CallID,
				Name:   msg.Name,
				Args:   msg.Args,
				Error:  msg.Message,
			}
			select {
			case a.events <- ev:
			default:
			}
		}
	}
}

// JSONL protocol types (implementation detail of pi-coding-agent adapter).

type taskStatePayload struct {
	State       string               `json:"state"`
	Question    string               `json:"question,omitempty"`
	Summary     string               `json:"summary,omitempty"`
	Message     string               `json:"message,omitempty"`
	ActivityLog []agent.ToolActivity `json:"activity_log,omitempty"`
}

func snapshotToPayload(s agent.TaskStateSnapshot) taskStatePayload {
	return taskStatePayload{
		State:       s.State,
		Question:    s.Question,
		Summary:     s.Summary,
		Message:     s.Message,
		ActivityLog: s.ActivityLog,
	}
}

type initMsg struct {
	Type           string `json:"type"`
	SystemPrompt   string `json:"system_prompt"`
	ProjectContext string `json:"project_context"`
	Model          string `json:"model"`
}

type userSayMsg struct {
	Type      string           `json:"type"`
	ID        string           `json:"id"`
	Text      string           `json:"text"`
	TaskState taskStatePayload `json:"task_state"`
}

type toolResultMsg struct {
	Type   string          `json:"type"`
	CallID string          `json:"call_id"`
	Result json.RawMessage `json:"result"`
}

type taskUpdateMsg struct {
	Type      string           `json:"type"`
	ID        string           `json:"id"`
	TaskState taskStatePayload `json:"task_state"`
}

type shutdownMsg struct {
	Type string `json:"type"`
}

type inboundMsg struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Text    string          `json:"text,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	Message string          `json:"message,omitempty"`
}
