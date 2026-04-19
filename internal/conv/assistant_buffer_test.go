package conv

import (
	"reflect"
	"testing"
)

func TestAssistantBufferEarlyFlushOnClauseBreak(t *testing.T) {
	b := &assistantBuffer{}
	// First chunk under the minimum: no flush.
	if out := b.appendAndFlush("Sure,"); len(out) != 0 {
		t.Fatalf("premature flush: %v", out)
	}
	// Second chunk crosses the 30-char threshold at ", " — expect first flush.
	out := b.appendAndFlush(" let me explain this quickly, ")
	want := []string{"Sure, let me explain this quickly,"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
	if !b.firstEmitted {
		t.Fatal("firstEmitted should be true after early flush")
	}
	// Subsequent text must go through newline-only path, not early-flush.
	if out := b.appendAndFlush("then continue, "); len(out) != 0 {
		t.Fatalf("unexpected early flush after first: %v", out)
	}
	// A newline finalises a sentence normally.
	out = b.appendAndFlush("it works.\n")
	want2 := []string{"then continue, it works."}
	if !reflect.DeepEqual(out, want2) {
		t.Fatalf("got %v, want %v", out, want2)
	}
}

func TestAssistantBufferNoEarlyFlushWithoutClauseBreak(t *testing.T) {
	b := &assistantBuffer{}
	// Long prefix with no clause punctuation: should wait for newline.
	out := b.appendAndFlush("That is approximately thirty six degrees")
	if len(out) != 0 {
		t.Fatalf("premature flush without clause break: %v", out)
	}
	out = b.appendAndFlush(" celsius.\n")
	want := []string{"That is approximately thirty six degrees celsius."}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestAssistantBufferDrainResetsFirstEmitted(t *testing.T) {
	b := &assistantBuffer{}
	b.firstEmitted = true
	_ = b.drain()
	if b.firstEmitted {
		t.Fatal("drain should reset firstEmitted for the next turn")
	}
}

func TestFindEarlyClauseBreak(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"short,", -1},
		{"not long enough yet,", -1},
		{"this is definitely long enough, and keeps going", len("this is definitely long enough,")},
		{"no clause breaks here just plain words", -1},
		{"here is a suitably long headline: followed by more", len("here is a suitably long headline:")},
	}
	for _, c := range cases {
		got := findEarlyClauseBreak(c.in)
		if got != c.want {
			t.Errorf("findEarlyClauseBreak(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
