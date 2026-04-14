package call

import "testing"

func TestStateString(t *testing.T) {
	cases := []struct {
		s    State
		want string
	}{
		{StateIdle, "idle"},
		{StateIntake, "intake"},
		{StateAwaitingConfirm, "awaiting_confirm"},
		{StateWorking, "working"},
		{StateEscalating, "escalating"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("State(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestEventKind(t *testing.T) {
	// Compile-time check: every event type implements Event.
	var _ Event = HotkeyPress{}
	var _ Event = UserUtterance{Text: "hi"}
	var _ Event = PMIntakeResult{}
	var _ Event = PMRouteResult{}
	var _ Event = SidecarAssistantText{}
	var _ Event = SidecarAskUser{}
	var _ Event = SidecarDone{}
	var _ Event = SidecarError{}
}
