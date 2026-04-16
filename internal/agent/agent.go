package agent

import (
	"context"
	"encoding/json"
)

// ChatEvent is a discriminated event from a chat agent back to the session.
type ChatEvent struct {
	Type   string          // "assistant_say", "tool_call", "turn_end", "error"
	ID     string          // turn correlation ID
	Text   string          // for assistant_say
	CallID string          // for tool_call
	Name   string          // tool name for tool_call
	Args   json.RawMessage // tool args for tool_call
	Error  string          // for error
}

// ChatConfig holds init-time configuration for a chat agent.
type ChatConfig struct {
	SystemPrompt   string
	ProjectContext string
	Model          string
}

// ChatAgent is a long-lived chat session that the voice loop drives.
type ChatAgent interface {
	Init(ctx context.Context, cfg ChatConfig) error
	Say(turnID, text string, taskState TaskStateSnapshot) error
	TaskUpdate(turnID string, taskState TaskStateSnapshot) error
	ToolResult(callID string, result json.RawMessage) error
	Events() <-chan ChatEvent
	Close() error
}

// TaskStateSnapshot is the agent-facing view of background task state.
type TaskStateSnapshot struct {
	State       string // "none", "running", "needs_input", "done", "failed"
	Question    string
	Summary     string
	Message     string
	ActivityLog []ToolActivity
}

// ToolActivity is a concise record of a single tool execution.
type ToolActivity struct {
	Tool    string `json:"tool"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Ok      bool   `json:"ok"`
}

// TaskEvent is emitted by a running task agent.
type TaskEvent struct {
	Type     string // "running", "needs_input", "done", "failed", "activity"
	Question string
	AskID    string
	Summary  string
	Message  string
	Activity *ToolActivity // non-nil for "activity" type events
}

// Objective is a structured task spec passed to a TaskAgent.
type Objective struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	ModelHint          string   `json:"model_hint"`
}

// TaskAgent runs a single coding task to completion.
type TaskAgent interface {
	Start(ctx context.Context, obj Objective) error
	Reply(askID, answer string) error
	Cancel() error
	Events() <-chan TaskEvent
	Close() error
}

// TaskAgentFactory creates a new TaskAgent for each task invocation.
type TaskAgentFactory interface {
	NewTaskAgent() TaskAgent
}
