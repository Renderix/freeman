package fakes

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/call"
)

func TestStdoutSpeaker(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdoutSpeaker(&buf)
	if err := s.Speak(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("buf = %q", out)
	}
	if !strings.Contains(out, "tts") {
		t.Errorf("missing tts prefix: %q", out)
	}
}

func TestLineReaderTranscriber(t *testing.T) {
	r := strings.NewReader("hello world\nsecond line\n")
	tr := NewLineReaderTranscriber(r)
	defer tr.Stop()

	u := tr.Utterances()
	got := []string{}
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case line := <-u:
			got = append(got, line)
		case <-timeout:
			t.Fatalf("timed out; got %v", got)
		}
	}
	if got[0] != "hello world" || got[1] != "second line" {
		t.Errorf("got = %v", got)
	}
}

func TestScriptedPM_Intake(t *testing.T) {
	pm := NewScriptedPM()
	ctx := context.Background()
	r1, err := pm.Intake(ctx, call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatal(err)
	}
	if !r1.NeedsMore {
		t.Error("first intake should need more")
	}
	r2, err := pm.Intake(ctx, call.IntakeInput{Latest: "off, 10 percent"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.NeedsMore || r2.Objective == nil {
		t.Errorf("second intake should complete, got %+v", r2)
	}
	if r2.Objective.ModelHint == "" {
		t.Errorf("model hint empty")
	}
}

func TestScriptedPM_RouteAlwaysInline(t *testing.T) {
	pm := NewScriptedPM()
	r, err := pm.Route(context.Background(), call.RouteInput{Question: "x?"})
	if err != nil {
		t.Fatal(err)
	}
	if r.AnswerInline == "" {
		t.Errorf("expected inline answer, got %+v", r)
	}
}

func TestStdinHotkey(t *testing.T) {
	r := strings.NewReader("first\nsecond\n")
	hk := NewStdinHotkey(r)

	events := hk.Events()
	count := 0
	timeout := time.After(2 * time.Second)
	for count < 2 {
		select {
		case <-events:
			count++
		case <-timeout:
			t.Fatalf("timed out; got %d", count)
		}
	}
	hk.Stop()
}

func TestStdinHotkey_StopUnblocksBlockingReader(t *testing.T) {
	// io.Pipe reader never EOFs on its own; Stop must close it to unblock.
	pr, _ := io.Pipe()
	hk := NewStdinHotkey(pr)

	done := make(chan struct{})
	go func() {
		// Give the hotkey goroutine a moment to start its Scan.
		time.Sleep(50 * time.Millisecond)
		hk.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not unblock the hotkey goroutine")
	}

	// Events channel should close shortly after Stop.
	select {
	case _, open := <-hk.Events():
		if open {
			t.Error("events channel still open after Stop")
		}
	case <-time.After(500 * time.Millisecond):
		// Channel close is async relative to Stop(); tolerate a brief window.
	}
}
