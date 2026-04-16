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
