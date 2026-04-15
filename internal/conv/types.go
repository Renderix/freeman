// Package conv hosts the conversational call layer that replaces the
// narrow Plan 3 intake state machine. A long-lived ConvSession runs the
// chat LLM; a TaskManager owns at most one background coding task; the
// Go side here is a thin router between mic, conv-sidecar, task sidecar,
// and speaker.
package conv

// Objective is a structured task spec the chat LLM hands to TaskManager
// when it calls the start_task tool. Mirrors what the existing
// sidecar.Client expects via sidecar.ObjectivePayload.
type Objective struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	ModelHint          string   `json:"model_hint"` // "sonnet" or "opus"
	SpokenSummary      string   `json:"spoken_summary"`
}

// TaskStateKind is the lifecycle of the single background task tracked
// by TaskManager. The zero value is TaskStateNone.
type TaskStateKind int

const (
	TaskStateNone TaskStateKind = iota
	TaskStateRunning
	TaskStateNeedsInput
	TaskStateDone
	TaskStateFailed
)

func (k TaskStateKind) String() string {
	switch k {
	case TaskStateRunning:
		return "running"
	case TaskStateNeedsInput:
		return "needs_input"
	case TaskStateDone:
		return "done"
	case TaskStateFailed:
		return "failed"
	default:
		return "none"
	}
}

// TaskState is a snapshot of the current background task's state.
// Unused fields for the current Kind are left at their zero value.
type TaskState struct {
	Kind     TaskStateKind
	Question string // valid when Kind == TaskStateNeedsInput
	Summary  string // valid when Kind == TaskStateDone
	Message  string // valid when Kind == TaskStateFailed
	// AskUserID is the sidecar-issued correlation id for the current
	// needs_input question. Used by Reply() to forward the answer.
	AskUserID string
}
