package call

import "testing"

func TestMachine_IdleToIntake(t *testing.T) {
	m := NewMachine()
	effects := m.Handle(HotkeyPress{})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d, want 1", len(effects))
	}
	if _, ok := effects[0].(SpeakEffect); !ok {
		t.Errorf("effects[0] = %T, want SpeakEffect", effects[0])
	}
}

func TestMachine_IntakeUtteranceCallsPM(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	effects := m.Handle(UserUtterance{Text: "build a feature flag"})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d, want 1", len(effects))
	}
	e, ok := effects[0].(CallPMIntakeEffect)
	if !ok {
		t.Fatalf("effects[0] = %T, want CallPMIntakeEffect", effects[0])
	}
	if e.Input.Latest != "build a feature flag" {
		t.Errorf("latest = %q", e.Input.Latest)
	}
	if len(e.Input.Transcript) != 1 || e.Input.Transcript[0] != "build a feature flag" {
		t.Errorf("transcript = %v", e.Input.Transcript)
	}
}

func TestMachine_IntakeNeedsMore(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build a feature flag"})
	effects := m.Handle(PMIntakeResult{NeedsMore: true, Question: "on or off by default?"})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok || s.Text != "on or off by default?" {
		t.Errorf("effect = %+v", effects[0])
	}
}

func TestMachine_IntakeCompleteToAwaitingConfirm(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build a feature flag"})
	obj := Objective{
		Goal:          "add feature flag",
		ModelHint:     "sonnet",
		SpokenSummary: "add a feature flag for checkout",
	}
	effects := m.Handle(PMIntakeResult{NeedsMore: false, Objective: &obj})
	if m.State() != StateAwaitingConfirm {
		t.Fatalf("state = %s, want awaiting_confirm", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if s.Text == "" {
		t.Error("speak text empty")
	}
}

func TestMachine_AwaitingConfirmYes(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build it"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "do the thing"}
	m.Handle(PMIntakeResult{Objective: &obj})
	effects := m.Handle(UserUtterance{Text: "yes"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	var sawStart, sawSpeak bool
	for _, e := range effects {
		switch e.(type) {
		case SendSidecarStartEffect:
			sawStart = true
		case SpeakEffect:
			sawSpeak = true
		}
	}
	if !sawStart || !sawSpeak {
		t.Errorf("effects = %v", effects)
	}
}

func TestMachine_AwaitingConfirmForceStart(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build it"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "sum"}
	m.Handle(PMIntakeResult{Objective: &obj})
	effects := m.Handle(UserUtterance{Text: "just go"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	found := false
	for _, e := range effects {
		if _, ok := e.(SendSidecarStartEffect); ok {
			found = true
		}
	}
	if !found {
		t.Error("no SendSidecarStartEffect")
	}
}

func TestMachine_AwaitingConfirmRejectGoesBackToIntake(t *testing.T) {
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build it"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "sum"}
	m.Handle(PMIntakeResult{Objective: &obj})
	effects := m.Handle(UserUtterance{Text: "no actually let's also add telemetry"})
	if m.State() != StateIntake {
		t.Fatalf("state = %s, want intake", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	if _, ok := effects[0].(CallPMIntakeEffect); !ok {
		t.Errorf("effect = %T", effects[0])
	}
}

func TestMachine_WorkingAskUserRoutes(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarAskUser{ID: "q1", Question: "use existing client?"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	r, ok := effects[0].(CallPMRouteEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if r.ID != "q1" {
		t.Errorf("id = %q", r.ID)
	}
	if r.Input.Question != "use existing client?" {
		t.Errorf("question = %q", r.Input.Question)
	}
}

func TestMachine_WorkingRouteAnswerInline(t *testing.T) {
	m := driveToWorking(t)
	m.Handle(SidecarAskUser{ID: "q1", Question: "use existing?"})
	effects := m.Handle(PMRouteResult{ID: "q1", AnswerInline: "yes"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	reply, ok := effects[0].(SendSidecarReplyEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if reply.ID != "q1" || reply.Answer != "yes" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestMachine_WorkingRouteEscalate(t *testing.T) {
	m := driveToWorking(t)
	m.Handle(SidecarAskUser{ID: "q1", Question: "use existing?"})
	effects := m.Handle(PMRouteResult{ID: "q1", SpokenQuestion: "existing or new?"})
	if m.State() != StateEscalating {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok || s.Text != "existing or new?" {
		t.Errorf("effect = %+v", effects[0])
	}
}

func TestMachine_EscalatingUserReplyGoesBackToWorking(t *testing.T) {
	m := driveToWorking(t)
	m.Handle(SidecarAskUser{ID: "q1", Question: "q"})
	m.Handle(PMRouteResult{ID: "q1", SpokenQuestion: "spoken"})
	effects := m.Handle(UserUtterance{Text: "existing"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	r, ok := effects[0].(SendSidecarReplyEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if r.ID != "q1" || r.Answer != "existing" {
		t.Errorf("reply = %+v", r)
	}
}

func TestMachine_SidecarDoneGoesIdle(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarDone{Summary: "edited 3 files"})
	if m.State() != StateIdle {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	s, ok := effects[0].(SpeakEffect)
	if !ok {
		t.Fatalf("effect = %T", effects[0])
	}
	if s.Text == "" {
		t.Error("empty summary speak")
	}
}

func TestMachine_SidecarErrorGoesIdle(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarError{Message: "oops"})
	if m.State() != StateIdle {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 1 {
		t.Fatalf("effects len = %d", len(effects))
	}
	if _, ok := effects[0].(SpeakEffect); !ok {
		t.Errorf("effect = %T", effects[0])
	}
}

func TestMachine_AssistantTextDoesNothing(t *testing.T) {
	m := driveToWorking(t)
	effects := m.Handle(SidecarAssistantText{Text: "editing file"})
	if m.State() != StateWorking {
		t.Fatalf("state = %s", m.State())
	}
	if len(effects) != 0 {
		t.Errorf("expected no effects, got %v", effects)
	}
}

// driveToWorking is a test helper that walks a fresh machine through
// Idle → Intake → AwaitingConfirm → Working.
func driveToWorking(t *testing.T) *Machine {
	t.Helper()
	m := NewMachine()
	m.Handle(HotkeyPress{})
	m.Handle(UserUtterance{Text: "build a thing"})
	obj := Objective{Goal: "g", ModelHint: "sonnet", SpokenSummary: "sum"}
	m.Handle(PMIntakeResult{Objective: &obj})
	m.Handle(UserUtterance{Text: "yes"})
	if m.State() != StateWorking {
		t.Fatalf("failed to reach Working, at %s", m.State())
	}
	return m
}
