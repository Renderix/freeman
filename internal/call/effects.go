package call

// Effect is a sealed interface; only types in this file implement it.
// The machine emits effects; the session executes them.
type Effect interface{ isEffect() }

// SpeakEffect tells the session to speak the given text via Speaker.
type SpeakEffect struct {
	Text string
}

func (SpeakEffect) isEffect() {}

// CallPMIntakeEffect tells the session to invoke PM.Intake asynchronously.
type CallPMIntakeEffect struct {
	Input IntakeInput
}

func (CallPMIntakeEffect) isEffect() {}

// CallPMRouteEffect tells the session to invoke PM.Route asynchronously.
type CallPMRouteEffect struct {
	ID    string
	Input RouteInput
}

func (CallPMRouteEffect) isEffect() {}

// SendSidecarStartEffect tells the session to dispatch the objective
// to the sidecar as a start message.
type SendSidecarStartEffect struct {
	Objective Objective
}

func (SendSidecarStartEffect) isEffect() {}

// SendSidecarReplyEffect tells the session to send an ask_user reply
// to the sidecar.
type SendSidecarReplyEffect struct {
	ID     string
	Answer string
}

func (SendSidecarReplyEffect) isEffect() {}
