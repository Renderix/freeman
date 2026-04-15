package audio

import "testing"

func TestNoopMuter(t *testing.T) {
	var m Muter = &NoopMuter{}
	// Should be safe to call repeatedly and in any order.
	m.Mute()
	m.Mute()
	m.Unmute()
	m.Unmute()
	m.Mute()
	m.Unmute()
}

func TestContext_NewCloseSkippable(t *testing.T) {
	ctx, err := New(nil)
	if err != nil {
		t.Skipf("audio context unavailable in this environment: %v", err)
	}
	if ctx == nil {
		t.Fatal("New returned nil context with nil error")
	}
	if err := ctx.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
