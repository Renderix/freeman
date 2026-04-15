package hotkey

import (
	"bytes"
	"testing"
	"time"
)

func TestStdinLine_EmitsOnNewline(t *testing.T) {
	r := bytes.NewBufferString("hello\nworld\n")
	h := newStdinLineHotkey(r)
	h.run()

	got := 0
	timeout := time.After(time.Second)
loop:
	for {
		select {
		case <-h.Events():
			got++
		case <-timeout:
			break loop
		}
		if got >= 2 {
			break
		}
	}
	if got != 2 {
		t.Errorf("events = %d, want 2", got)
	}
}

func TestTTYKeyMatch_Enter(t *testing.T) {
	if !matchKey("enter", '\r') {
		t.Errorf("enter should match \\r")
	}
	if !matchKey("enter", '\n') {
		t.Errorf("enter should match \\n")
	}
	if matchKey("enter", ' ') {
		t.Errorf("enter should not match space")
	}
}

func TestTTYKeyMatch_Space(t *testing.T) {
	if !matchKey("space", ' ') {
		t.Errorf("space should match ' '")
	}
	if matchKey("space", '\r') {
		t.Errorf("space should not match \\r")
	}
}
