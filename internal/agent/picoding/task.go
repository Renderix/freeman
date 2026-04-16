package picoding

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/Renderix/freeman/internal/agent"
	"github.com/Renderix/freeman/internal/sidecar"
)

// TaskAgent runs a single coding task via pi-coding-agent (sidecar.ts).
type TaskAgent struct {
	repoRoot string

	mu     sync.Mutex
	client *sidecar.Client
	cancel context.CancelFunc
	events chan agent.TaskEvent
	wg     sync.WaitGroup
}

func NewTaskAgent(repoRoot string) *TaskAgent {
	return &TaskAgent{
		repoRoot: repoRoot,
		events:   make(chan agent.TaskEvent, 8),
	}
}

func (a *TaskAgent) Start(ctx context.Context, obj agent.Objective) error {
	scriptPath := filepath.Join(a.repoRoot, "sidecar", "sidecar.ts")
	taskCtx, cancel := context.WithCancel(ctx)
	client, err := sidecar.Spawn(taskCtx, "bun", "run", scriptPath)
	if err != nil {
		cancel()
		return fmt.Errorf("spawn task sidecar: %w", err)
	}

	a.mu.Lock()
	a.client = client
	a.cancel = cancel
	a.mu.Unlock()

	if err := client.Send(sidecar.StartMsg{
		Type: sidecar.MsgTypeStart,
		Objective: sidecar.ObjectivePayload{
			Goal:               obj.Goal,
			AcceptanceCriteria: obj.AcceptanceCriteria,
			Constraints:        obj.Constraints,
			Notes:              obj.Notes,
			Model:              obj.ModelHint,
		},
	}); err != nil {
		a.shutdown()
		return err
	}

	a.wg.Add(1)
	go a.readLoop(client)
	return nil
}

func (a *TaskAgent) Reply(askID, answer string) error {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil {
		return fmt.Errorf("no task client")
	}
	return client.Send(sidecar.AskUserReplyMsg{
		Type:   sidecar.MsgTypeAskUserReply,
		ID:     askID,
		Answer: answer,
	})
}

func (a *TaskAgent) Cancel() error {
	a.shutdown()
	return nil
}

func (a *TaskAgent) Events() <-chan agent.TaskEvent { return a.events }

func (a *TaskAgent) Close() error {
	a.shutdown()
	a.wg.Wait()
	return nil
}

func (a *TaskAgent) shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.client != nil {
		_ = a.client.Close()
		a.client = nil
	}
}

func (a *TaskAgent) readLoop(client *sidecar.Client) {
	defer a.wg.Done()
	defer close(a.events)
	for msg := range client.Events() {
		var ev agent.TaskEvent
		switch v := msg.(type) {
		case sidecar.ToolActivityMsg:
			ev = agent.TaskEvent{
				Type: "activity",
				Activity: &agent.ToolActivity{
					Tool:    v.Tool,
					Path:    v.Path,
					Command: v.Command,
					Ok:      v.Ok,
				},
			}
		case sidecar.AskUserMsg:
			ev = agent.TaskEvent{Type: "needs_input", Question: v.Question, AskID: v.ID}
		case sidecar.DoneMsg:
			ev = agent.TaskEvent{Type: "done", Summary: v.Summary}
		case sidecar.ErrorMsg:
			ev = agent.TaskEvent{Type: "failed", Message: v.Message}
		case sidecar.AssistantTextMsg:
			continue
		default:
			continue
		}
		select {
		case a.events <- ev:
		default:
		}
	}
}
