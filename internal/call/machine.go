package call

import "strings"

// Machine holds call session state and computes transitions + effects.
// Not thread-safe; the session goroutine owns it.
type Machine struct {
	state            State
	transcript       []string
	objective        *Objective
	pendingAskUserID string
}

// NewMachine returns a fresh machine in StateIdle.
func NewMachine() *Machine {
	return &Machine{state: StateIdle, transcript: []string{}}
}

// State returns the current state.
func (m *Machine) State() State { return m.state }

// Handle advances the machine based on an event and returns effects to run.
func (m *Machine) Handle(e Event) []Effect {
	switch m.state {
	case StateIdle:
		return m.handleIdle(e)
	case StateIntake:
		return m.handleIntake(e)
	case StateAwaitingConfirm:
		return m.handleAwaitingConfirm(e)
	case StateWorking:
		return m.handleWorking(e)
	case StateEscalating:
		return m.handleEscalating(e)
	}
	return []Effect{}
}

func (m *Machine) handleIdle(e Event) []Effect {
	if _, ok := e.(HotkeyPress); ok {
		m.state = StateIntake
		m.transcript = []string{}
		m.objective = nil
		m.pendingAskUserID = ""
		return []Effect{SpeakEffect{Text: "hi. what are we building?"}}
	}
	return []Effect{}
}

func (m *Machine) handleIntake(e Event) []Effect {
	switch ev := e.(type) {
	case UserUtterance:
		m.transcript = append(m.transcript, ev.Text)
		return []Effect{CallPMIntakeEffect{Input: IntakeInput{
			Transcript: append([]string{}, m.transcript...),
			Latest:     ev.Text,
		}}}
	case PMIntakeResult:
		if ev.NeedsMore {
			m.transcript = append(m.transcript, ev.Question)
			return []Effect{SpeakEffect{Text: ev.Question}}
		}
		if ev.Objective == nil {
			return []Effect{}
		}
		m.objective = ev.Objective
		m.state = StateAwaitingConfirm
		text := ev.Objective.SpokenSummary + " should i start?"
		m.transcript = append(m.transcript, text)
		return []Effect{SpeakEffect{Text: text}}
	}
	return []Effect{}
}

func (m *Machine) handleAwaitingConfirm(e Event) []Effect {
	ev, ok := e.(UserUtterance)
	if !ok {
		return []Effect{}
	}
	m.transcript = append(m.transcript, ev.Text)
	if isAffirmative(ev.Text) && m.objective != nil {
		m.state = StateWorking
		return []Effect{
			SendSidecarStartEffect{Objective: *m.objective},
			SpeakEffect{Text: "starting now."},
		}
	}
	// Not affirmative — treat as continued intake.
	m.state = StateIntake
	return []Effect{CallPMIntakeEffect{Input: IntakeInput{
		Transcript: append([]string{}, m.transcript...),
		Latest:     ev.Text,
	}}}
}

func (m *Machine) handleWorking(e Event) []Effect {
	switch ev := e.(type) {
	case SidecarAssistantText:
		return []Effect{}
	case SidecarAskUser:
		if m.objective == nil {
			return []Effect{}
		}
		m.pendingAskUserID = ev.ID
		return []Effect{CallPMRouteEffect{
			ID: ev.ID,
			Input: RouteInput{
				Objective:  *m.objective,
				Transcript: append([]string{}, m.transcript...),
				Question:   ev.Question,
			},
		}}
	case PMRouteResult:
		if ev.AnswerInline != "" {
			m.pendingAskUserID = ""
			return []Effect{SendSidecarReplyEffect{ID: ev.ID, Answer: ev.AnswerInline}}
		}
		if ev.SpokenQuestion != "" {
			m.state = StateEscalating
			return []Effect{SpeakEffect{Text: ev.SpokenQuestion}}
		}
		return []Effect{}
	case SidecarDone:
		m.state = StateIdle
		return []Effect{SpeakEffect{Text: "done. " + ev.Summary}}
	case SidecarError:
		m.state = StateIdle
		return []Effect{SpeakEffect{Text: "error from worker: " + ev.Message}}
	}
	return []Effect{}
}

func (m *Machine) handleEscalating(e Event) []Effect {
	ev, ok := e.(UserUtterance)
	if !ok {
		return []Effect{}
	}
	m.transcript = append(m.transcript, ev.Text)
	m.state = StateWorking
	id := m.pendingAskUserID
	m.pendingAskUserID = ""
	return []Effect{SendSidecarReplyEffect{ID: id, Answer: ev.Text}}
}

func isAffirmative(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "yes", "yeah", "yep", "go", "just go", "ship it", "do it", "start", "sure":
		return true
	}
	return false
}
