package call

// Event is a sealed interface; only types in this file implement it.
type Event interface{ isEvent() }

// HotkeyPress means the user pressed the call hotkey.
type HotkeyPress struct{}

func (HotkeyPress) isEvent() {}

// UserUtterance is a finalized transcript from the user.
type UserUtterance struct {
	Text            string
	InterruptedText string // non-empty when user barged in during Freeman's speech
}

func (UserUtterance) isEvent() {}

// PMIntakeResult is the PM's response during intake.
// If NeedsMore is true, Question holds the follow-up to speak.
// Otherwise Objective holds the completed spec.
type PMIntakeResult struct {
	NeedsMore bool
	Question  string
	Objective *Objective
}

func (PMIntakeResult) isEvent() {}

// PMRouteResult is the PM's decision for an ask_user question.
// Exactly one of AnswerInline or SpokenQuestion is non-empty.
type PMRouteResult struct {
	ID             string
	AnswerInline   string
	SpokenQuestion string
}

func (PMRouteResult) isEvent() {}

// SidecarAssistantText is intermediate Claude output (logged, not spoken).
type SidecarAssistantText struct {
	Text string
}

func (SidecarAssistantText) isEvent() {}

// SidecarAskUser is Claude calling the ask_user tool.
type SidecarAskUser struct {
	ID       string
	Question string
}

func (SidecarAskUser) isEvent() {}

// SidecarDone is the sidecar reporting clean completion.
type SidecarDone struct {
	Summary string
}

func (SidecarDone) isEvent() {}

// SidecarError is the sidecar reporting an error.
type SidecarError struct {
	Message string
}

func (SidecarError) isEvent() {}
