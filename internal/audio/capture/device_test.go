package capture

import (
	"testing"
	"time"
)

func TestSubscribeReceivesFrames(t *testing.T) {
	d := &Device{
		frameSize: 320,
		subs:      make(map[chan []int16]struct{}),
	}

	ch := d.Subscribe()
	if ch == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	frame := make([]int16, 320)
	for i := range frame {
		frame[i] = int16(i)
	}
	d.broadcast(frame)

	select {
	case got := <-ch:
		if len(got) != 320 {
			t.Fatalf("expected 320 samples, got %d", len(got))
		}
		if got[0] != 0 || got[1] != 1 {
			t.Fatal("frame data mismatch")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for frame")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	d := &Device{
		frameSize: 320,
		subs:      make(map[chan []int16]struct{}),
	}

	ch1 := d.Subscribe()
	ch2 := d.Subscribe()

	frame := make([]int16, 320)
	d.broadcast(frame)

	select {
	case <-ch1:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1 timeout")
	}
	select {
	case <-ch2:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 timeout")
	}
}

func TestUnsubscribe(t *testing.T) {
	d := &Device{
		frameSize: 320,
		subs:      make(map[chan []int16]struct{}),
	}

	// Subscribe, then immediately unsubscribe — removes from subs and closes ch.
	ch := d.Subscribe()
	d.Unsubscribe(ch)

	// After unsubscribe the subscriber map must be empty.
	d.subsMu.RLock()
	subsLen := len(d.subs)
	d.subsMu.RUnlock()
	if subsLen != 0 {
		t.Fatalf("expected 0 subs after Unsubscribe, got %d", subsLen)
	}

	// broadcast should not panic and should not enqueue anything (ch is closed
	// and removed from subs).
	frame := make([]int16, 320)
	d.broadcast(frame)

	// Verify the channel is closed (drain any zero-value reads) and no real
	// frame was delivered.
	select {
	case v, ok := <-ch:
		if ok {
			// A real frame was delivered — that's a bug.
			t.Fatalf("received real frame after unsubscribe: len=%d", len(v))
		}
		// ok==false means channel is closed with no data — expected.
	default:
		// channel is closed but nothing to read yet — also fine.
	}
}
