package call

import (
	"context"
	"fmt"

	"github.com/Renderix/freeman/internal/sidecar"
)

// SessionDeps are the port implementations injected into a Session.
type SessionDeps struct {
	Transcriber Transcriber
	Speaker     Speaker
	PM          PM
	Hotkey      Hotkey
	Sidecar     *sidecar.Client
}

// Session wires a Machine to its ports and runs the event loop.
type Session struct {
	deps    SessionDeps
	machine *Machine
	// internal channel for PM results so they interleave with external events.
	pmResults chan Event
}

// NewSession constructs a Session.
func NewSession(deps SessionDeps) *Session {
	return &Session{
		deps:      deps,
		machine:   NewMachine(),
		pmResults: make(chan Event, 4),
	}
}

// Run blocks until ctx is canceled, processing events and effects.
func (s *Session) Run(ctx context.Context) error {
	utterances := s.deps.Transcriber.Utterances()
	hotkeys := s.deps.Hotkey.Events()
	sidecarEvents := s.deps.Sidecar.Events()

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
			s.handleEvent(ctx, UserUtterance{Text: text})
		case msg, ok := <-sidecarEvents:
			if !ok {
				sidecarEvents = nil
				continue
			}
			s.handleSidecarMessage(ctx, msg)
		case ev := <-s.pmResults:
			s.handleEvent(ctx, ev)
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
		_ = s.deps.Speaker.Speak(ctx, eff.Text)
	case CallPMIntakeEffect:
		in := eff.Input
		go func() {
			res, err := s.deps.PM.Intake(ctx, in)
			if err != nil {
				s.pmResults <- SidecarError{Message: fmt.Sprintf("pm intake: %v", err)}
				return
			}
			s.pmResults <- res
		}()
	case CallPMRouteEffect:
		in := eff.Input
		id := eff.ID
		go func() {
			res, err := s.deps.PM.Route(ctx, in)
			if err != nil {
				s.pmResults <- SidecarError{Message: fmt.Sprintf("pm route: %v", err)}
				return
			}
			res.ID = id
			s.pmResults <- res
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
