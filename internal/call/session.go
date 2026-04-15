package call

import (
	"context"
	"fmt"

	"github.com/Renderix/freeman/internal/sidecar"
)

// SessionDeps are the port implementations injected into a Session.
type SessionDeps struct {
	Transcriber  Transcriber
	Speaker      Speaker
	PM           PM
	Hotkey       Hotkey
	Sidecar      *sidecar.Client
	SpeechOnsets <-chan struct{} // from vad.VAD.SpeechOnsets(); nil disables barge-in
}

// Session wires a Machine to its ports and runs the event loop.
type Session struct {
	deps    SessionDeps
	machine *Machine
	// internal channel for PM results so they interleave with external events.
	pmResults chan Event

	// Async Speak state — read/written only from the Run goroutine; no mutex.
	cancelSpeak      func()        // non-nil when a Speak goroutine is in flight
	currentSpeakText string        // text being spoken; stored for interrupted-text context
	speakDone        chan struct{} // Speak goroutine sends here when it finishes
	interruptedText  string        // set on barge-in; attached to next UserUtterance
}

// NewSession constructs a Session.
func NewSession(deps SessionDeps) *Session {
	return &Session{
		deps:      deps,
		machine:   NewMachine(),
		pmResults: make(chan Event, 4),
		speakDone: make(chan struct{}, 1),
	}
}

// Run blocks until ctx is canceled, processing events and effects.
func (s *Session) Run(ctx context.Context) error {
	utterances := s.deps.Transcriber.Utterances()
	hotkeys := s.deps.Hotkey.Events()
	sidecarEvents := s.deps.Sidecar.Events()

	// Convert nil SpeechOnsets to a channel that never fires.
	speechOnsets := s.deps.SpeechOnsets
	if speechOnsets == nil {
		speechOnsets = make(chan struct{})
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-hotkeys:
			s.handleEvent(ctx, HotkeyPress{})

		case text, ok := <-utterances:
			if !ok {
				utterances = nil
				continue
			}
			ev := UserUtterance{Text: text, InterruptedText: s.interruptedText}
			s.interruptedText = ""
			s.handleEvent(ctx, ev)

		case msg, ok := <-sidecarEvents:
			if !ok {
				sidecarEvents = nil
				continue
			}
			s.handleSidecarMessage(ctx, msg)

		case ev := <-s.pmResults:
			s.handleEvent(ctx, ev)

		case <-s.speakDone:
			s.cancelSpeak = nil
			s.currentSpeakText = ""

		case <-speechOnsets:
			if s.cancelSpeak != nil {
				s.interruptedText = s.currentSpeakText
				s.cancelSpeak()
				s.cancelSpeak = nil
				s.currentSpeakText = ""
			}
		}
	}
}

func (s *Session) handleEvent(ctx context.Context, e Event) {
	effects := s.machine.Handle(e)
	for _, eff := range effects {
		s.runEffect(ctx, eff)
	}
}

func (s *Session) handleSidecarMessage(ctx context.Context, msg sidecar.Message) {
	switch m := msg.(type) {
	case sidecar.AssistantTextMsg:
		s.handleEvent(ctx, SidecarAssistantText{Text: m.Text})
	case sidecar.AskUserMsg:
		s.handleEvent(ctx, SidecarAskUser{ID: m.ID, Question: m.Question})
	case sidecar.DoneMsg:
		s.handleEvent(ctx, SidecarDone{Summary: m.Summary})
	case sidecar.ErrorMsg:
		s.handleEvent(ctx, SidecarError{Message: m.Message})
	}
}

func (s *Session) runEffect(ctx context.Context, e Effect) {
	switch eff := e.(type) {
	case SpeakEffect:
		ctx2, cancel := context.WithCancel(ctx)
		s.cancelSpeak = cancel
		s.currentSpeakText = eff.Text
		go func() {
			_ = s.deps.Speaker.Speak(ctx2, eff.Text)
			cancel() // idempotent: safe to call even if already canceled
			select {
			case s.speakDone <- struct{}{}:
			default:
			}
		}()

	case ResetPMEffect:
		s.deps.PM.Reset()
		s.interruptedText = ""

	case CallPMIntakeEffect:
		in := eff.Input
		go func() {
			var ev Event
			res, err := s.deps.PM.Intake(ctx, in)
			if err != nil {
				ev = SidecarError{Message: fmt.Sprintf("pm intake: %v", err)}
			} else {
				ev = res
			}
			select {
			case s.pmResults <- ev:
			case <-ctx.Done():
			}
		}()

	case CallPMRouteEffect:
		in := eff.Input
		id := eff.ID
		go func() {
			var ev Event
			res, err := s.deps.PM.Route(ctx, in)
			if err != nil {
				ev = SidecarError{Message: fmt.Sprintf("pm route: %v", err)}
			} else {
				res.ID = id
				ev = res
			}
			select {
			case s.pmResults <- ev:
			case <-ctx.Done():
			}
		}()

	case SendSidecarStartEffect:
		payload := sidecar.ObjectivePayload{
			Goal:               eff.Objective.Goal,
			AcceptanceCriteria: eff.Objective.AcceptanceCriteria,
			Constraints:        eff.Objective.Constraints,
			Notes:              eff.Objective.Notes,
			Model:              eff.Objective.ModelHint,
		}
		_ = s.deps.Sidecar.Send(sidecar.StartMsg{
			Type:      sidecar.MsgTypeStart,
			Objective: payload,
		})

	case SendSidecarReplyEffect:
		_ = s.deps.Sidecar.Send(sidecar.AskUserReplyMsg{
			Type:   sidecar.MsgTypeAskUserReply,
			ID:     eff.ID,
			Answer: eff.Answer,
		})
	}
}
