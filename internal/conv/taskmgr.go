package conv

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/Renderix/freeman/internal/sidecar"
)

// TaskEvent is emitted whenever the tracked task's state changes.
// Subscribers receive a snapshot of the new state.
type TaskEvent struct {
	State TaskState
}

// TaskManager owns at most one background pi-coding-agent task at a
// time. It wraps a sidecar.Client over sidecar/sidecar.ts.
type TaskManager struct {
	repoRoot string

	mu     sync.Mutex
	state  TaskState
	client *sidecar.Client
	cancel context.CancelFunc

	events chan TaskEvent

	wg sync.WaitGroup
}

// NewTaskManager constructs a manager that will spawn the task sidecar
// from the given repo root. No subprocess is started until Start() is
// called.
func NewTaskManager(repoRoot string) *TaskManager {
	return &TaskManager{
		repoRoot: repoRoot,
		events:   make(chan TaskEvent, 8),
	}
}

// Events returns the channel of state transitions. Capacity is small;
// consumers should drain promptly.
func (m *TaskManager) Events() <-chan TaskEvent { return m.events }

// Status returns the current task state snapshot.
func (m *TaskManager) Status() TaskState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Start spawns the task sidecar and dispatches the objective. Errors
// if a task is already in flight.
func (m *TaskManager) Start(ctx context.Context, obj Objective) error {
	m.mu.Lock()
	if m.state.Kind != TaskStateNone && m.state.Kind != TaskStateDone && m.state.Kind != TaskStateFailed {
		m.mu.Unlock()
		return fmt.Errorf("task already running")
	}
	// Reset stale done/failed state before launching a new one.
	m.state = TaskState{}

	scriptPath := filepath.Join(m.repoRoot, "sidecar", "sidecar.ts")
	taskCtx, cancel := context.WithCancel(ctx)
	client, err := sidecar.Spawn(taskCtx, "bun", "run", scriptPath)
	if err != nil {
		cancel()
		m.mu.Unlock()
		return fmt.Errorf("spawn task sidecar: %w", err)
	}
	m.client = client
	m.cancel = cancel
	m.state = TaskState{Kind: TaskStateRunning}
	m.mu.Unlock()

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
		m.mu.Lock()
		m.cancelLocked()
		m.mu.Unlock()
		m.transition(TaskState{Kind: TaskStateFailed, Message: fmt.Sprintf("send start: %v", err)})
		return err
	}

	m.transition(TaskState{Kind: TaskStateRunning})

	m.wg.Add(1)
	go m.readLoop(client)
	return nil
}

// Reply forwards a user's spoken answer to the task sidecar's pending
// ask_user. Errors if no question is pending.
func (m *TaskManager) Reply(answer string) error {
	m.mu.Lock()
	if m.state.Kind != TaskStateNeedsInput {
		m.mu.Unlock()
		return fmt.Errorf("no question pending")
	}
	id := m.state.AskUserID
	client := m.client
	m.mu.Unlock()

	if client == nil {
		return fmt.Errorf("no task client")
	}
	if err := client.Send(sidecar.AskUserReplyMsg{
		Type:   sidecar.MsgTypeAskUserReply,
		ID:     id,
		Answer: answer,
	}); err != nil {
		return fmt.Errorf("send ask_user_reply: %w", err)
	}
	// Optimistically transition back to running; the sidecar will eventually
	// confirm via subsequent events.
	m.transition(TaskState{Kind: TaskStateRunning})
	return nil
}

// Cancel terminates any in-flight task. Safe to call when no task is running.
func (m *TaskManager) Cancel() error {
	m.mu.Lock()
	if m.client == nil {
		m.mu.Unlock()
		return nil
	}
	m.cancelLocked()
	m.mu.Unlock()
	m.transition(TaskState{Kind: TaskStateFailed, Message: "canceled"})
	return nil
}

// Close shuts down the manager. Idempotent.
func (m *TaskManager) Close() error {
	m.Cancel()
	m.wg.Wait()
	return nil
}

// cancelLocked must be called with m.mu held.
func (m *TaskManager) cancelLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
}

func (m *TaskManager) transition(s TaskState) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
	select {
	case m.events <- TaskEvent{State: s}:
	default:
		// Drop if consumer is slow; latest state is always available via Status().
	}
}

func (m *TaskManager) readLoop(client *sidecar.Client) {
	defer m.wg.Done()
	for msg := range client.Events() {
		switch v := msg.(type) {
		case sidecar.AssistantTextMsg:
			// Intermediate task narration; ignored in v1.
			_ = v
		case sidecar.AskUserMsg:
			m.transition(TaskState{
				Kind:      TaskStateNeedsInput,
				Question:  v.Question,
				AskUserID: v.ID,
			})
		case sidecar.DoneMsg:
			m.transition(TaskState{Kind: TaskStateDone, Summary: v.Summary})
		case sidecar.ErrorMsg:
			m.transition(TaskState{Kind: TaskStateFailed, Message: v.Message})
		}
	}
}
