package call

// State is the current phase of a call session.
type State int

const (
	StateIdle State = iota
	StateIntake
	StateAwaitingConfirm
	StateWorking
	StateEscalating
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateIntake:
		return "intake"
	case StateAwaitingConfirm:
		return "awaiting_confirm"
	case StateWorking:
		return "working"
	case StateEscalating:
		return "escalating"
	default:
		return "unknown"
	}
}
