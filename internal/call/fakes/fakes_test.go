package fakes

import (
	"bytes"
	"context"
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
		case t := <-u:
			got = append(got, t)
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
