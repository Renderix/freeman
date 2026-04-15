package conv

import "encoding/json"

// taskStatePayload is the JSON shape sent to conv-sidecar inside
// user_say.task_state. It mirrors TaskStateKind but as discriminated
// JSON so the TS side can switch on .state cleanly.
type taskStatePayload struct {
	State    string `json:"state"`
	Question string `json:"question,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Message  string `json:"message,omitempty"`
}

func taskStateToPayload(s TaskState) taskStatePayload {
	switch s.Kind {
	case TaskStateRunning:
		return taskStatePayload{State: "running"}
	case TaskStateNeedsInput:
		return taskStatePayload{State: "needs_input", Question: s.Question}
	case TaskStateDone:
		return taskStatePayload{State: "done", Summary: s.Summary}
	case TaskStateFailed:
		return taskStatePayload{State: "failed", Message: s.Message}
	default:
		return taskStatePayload{State: "none"}
	}
}

// Outbound (Go → conv-sidecar)

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

type shutdownMsg struct {
	Type string `json:"type"`
}

// Inbound (conv-sidecar → Go)

type inboundMsg struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Text    string          `json:"text,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	Message string          `json:"message,omitempty"`
}
