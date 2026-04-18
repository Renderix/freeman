package wakeword

import (
	"testing"
)

func TestKeywordKindFromIndex(t *testing.T) {
	tests := []struct {
		index int
		want  KeywordKind
	}{
		{0, KeywordWake},
		{1, KeywordMute},
		{2, KeywordStop},
	}
	for _, tt := range tests {
		got := KeywordKind(tt.index)
		if got != tt.want {
			t.Errorf("index %d: got %d, want %d", tt.index, got, tt.want)
		}
	}
}

func TestKeywordKindString(t *testing.T) {
	tests := []struct {
		kind KeywordKind
		want string
	}{
		{KeywordWake, "wake"},
		{KeywordMute, "mute"},
		{KeywordStop, "stop"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("KeywordKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestInt16ToFloat32(t *testing.T) {
	// OpenWakeWord's mel model ingests raw int16 magnitudes cast to
	// float32 (no [-1,1] normalisation). Scaling here would shift the
	// mel output and kill keyword accuracy — verify we preserve values.
	input := []int16{0, 16384, -16384, 32767, -32768}
	got := int16ToFloat32(input)
	want := []float32{0, 16384, -16384, 32767, -32768}
	if len(got) != len(want) {
		t.Fatalf("expected %d, got %d", len(want), len(got))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %f, want %f", i, got[i], w)
		}
	}
}

func TestRingBufferAppendAndSlice(t *testing.T) {
	rb := newRingFloat32(4)
	rb.append([]float32{1, 2})
	rb.append([]float32{3, 4})
	got := rb.lastN(4)
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}
	if got[0] != 1 || got[3] != 4 {
		t.Errorf("got %v, want [1 2 3 4]", got)
	}
	rb.append([]float32{5, 6})
	got = rb.lastN(4)
	if got[0] != 3 || got[3] != 6 {
		t.Errorf("after overflow got %v, want [3 4 5 6]", got)
	}
}

func TestRingBufferLastNPartial(t *testing.T) {
	rb := newRingFloat32(10)
	rb.append([]float32{1, 2, 3})
	got := rb.lastN(10)
	if len(got) != 3 {
		t.Fatalf("expected 3 (partial), got %d", len(got))
	}
}
