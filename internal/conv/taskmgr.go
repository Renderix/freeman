package conv

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Renderix/freeman/internal/agent"
)

// TaskEvent is emitted whenever the tracked task's state changes.
// Subscribers receive a snapshot of the new state.
type TaskEvent struct {
	State TaskState
}

// TaskManager owns at most one background coding task at a time.
type TaskManager struct {
	factory agent.TaskAgentFactory
	log     *slog.Logger

	mu     sync.Mutex
	state  TaskState
	agent  agent.TaskAgent
	cancel context.CancelFunc

	events chan TaskEvent

	wg sync.WaitGroup
}

// NewTaskManager constructs a manager that will use the given factory
// to create task agents. No agent is started until Start() is called.
func NewTaskManager(factory agent.TaskAgentFactory, log *slog.Logger) *TaskManager {
	return &TaskManager{
		factory: factory,
		log:     log,
		events:  make(chan TaskEvent, 8),
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

// Start creates a new task agent and dispatches the objective. Errors
// if a task is already in flight.
func (m *TaskManager) Start(ctx context.Context, obj Objective) error {
	m.mu.Lock()
	if m.state.Kind != TaskStateNone && m.state.Kind != TaskStateDone && m.state.Kind != TaskStateFailed {
		m.mu.Unlock()
		return fmt.Errorf("task already running")
	}
	m.state = TaskState{}

	ta := m.factory.NewTaskAgent()
	taskCtx, cancel := context.WithCancel(ctx)
	m.agent = ta
	m.cancel = cancel
	m.state = TaskState{Kind: TaskStateRunning}
	m.mu.Unlock()

	if err := ta.Start(taskCtx, obj.ToAgentObjective()); err != nil {
		m.mu.Lock()
		m.cancelLocked()
		m.mu.Unlock()
		m.transition(TaskState{Kind: TaskStateFailed, Message: fmt.Sprintf("start task: %v", err)})
		return err
	}

	m.transition(TaskState{Kind: TaskStateRunning})

	m.wg.Add(1)
	go m.readLoop(ta)
	return nil
}

// Reply forwards a user's spoken answer to the task agent's pending
// ask_user. Errors if no question is pending.
func (m *TaskManager) Reply(answer string) error {
	m.mu.Lock()
	if m.state.Kind != TaskStateNeedsInput {
		m.mu.Unlock()
		return fmt.Errorf("no question pending")
	}
	id := m.state.AskUserID
	ta := m.agent
	m.mu.Unlock()

	if ta == nil {
		return fmt.Errorf("no task agent")
	}
	if err := ta.Reply(id, answer); err != nil {
		return fmt.Errorf("reply: %w", err)
	}
	m.transition(TaskState{Kind: TaskStateRunning})
	return nil
}

// Cancel terminates any in-flight task. Safe to call when no task is running.
func (m *TaskManager) Cancel() error {
	m.mu.Lock()
	if m.agent == nil {
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

func (m *TaskManager) cancelLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.agent != nil {
		_ = m.agent.Close()
		m.agent = nil
	}
}

func (m *TaskManager) transition(s TaskState) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
	select {
	case m.events <- TaskEvent{State: s}:
	default:
	}
}

const maxActivityEntries = 50

func (m *TaskManager) transitionKeepActivity(s TaskState) {
	m.mu.Lock()
	s.ActivityLog = m.state.ActivityLog
	m.state = s
	m.mu.Unlock()
	m.log.Info("task transition", "kind", s.Kind.String(), "activity_count", len(s.ActivityLog))
	select {
	case m.events <- TaskEvent{State: s}:
	default:
	}
}

func (m *TaskManager) readLoop(ta agent.TaskAgent) {
	defer m.wg.Done()
	for ev := range ta.Events() {
		switch ev.Type {
		case "activity":
			if ev.Activity != nil {
				m.log.Info("tool activity", "tool", ev.Activity.Tool, "path", ev.Activity.Path, "command", ev.Activity.Command, "ok", ev.Activity.Ok)
				m.mu.Lock()
				m.state.ActivityLog = append(m.state.ActivityLog, *ev.Activity)
				if len(m.state.ActivityLog) > maxActivityEntries {
					m.state.ActivityLog = m.state.ActivityLog[len(m.state.ActivityLog)-maxActivityEntries:]
				}
				m.mu.Unlock()
			}
		case "needs_input":
			m.transitionKeepActivity(TaskState{
				Kind:      TaskStateNeedsInput,
				Question:  ev.Question,
				AskUserID: ev.AskID,
			})
		case "done":
			m.transitionKeepActivity(TaskState{Kind: TaskStateDone, Summary: ev.Summary})
		case "failed":
			m.transitionKeepActivity(TaskState{Kind: TaskStateFailed, Message: ev.Message})
		}
	}
}
