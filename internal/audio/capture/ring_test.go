package capture

import "testing"

func TestRing_PushPop(t *testing.T) {
	r := NewRing(4)
	if got := r.Push([]int16{1, 2, 3}); got != 0 {
		t.Errorf("dropped on first push = %d, want 0", got)
	}
	if got := r.Push([]int16{4, 5}); got != 1 {
		t.Errorf("dropped on overflow = %d, want 1", got)
	}
	// Capacity 4, we pushed 5 elements. Oldest one dropped.
	out := r.PopAll()
	// Expected contents: [2, 3, 4, 5] — oldest (1) dropped.
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4", len(out))
	}
	if out[0] != 2 || out[3] != 5 {
		t.Errorf("out = %v, want [2 3 4 5]", out)
	}
}

func TestRing_DroppedCounter(t *testing.T) {
	r := NewRing(2)
	r.Push([]int16{1, 2})
	r.Push([]int16{3, 4, 5}) // drops 1, 2, 3
	if r.Dropped() != 3 {
		t.Errorf("dropped = %d, want 3", r.Dropped())
	}
}

func TestRing_WrapAround(t *testing.T) {
	r := NewRing(8)
	r.Push([]int16{1, 2, 3, 4})
	if got := r.PopAll(); len(got) != 4 {
		t.Errorf("first pop = %v", got)
	}
	r.Push([]int16{5, 6, 7, 8, 9})
	got := r.PopAll()
	if len(got) != 5 {
		t.Fatalf("wrap pop len = %d", len(got))
	}
	if got[0] != 5 || got[4] != 9 {
		t.Errorf("wrap pop = %v", got)
	}
}
