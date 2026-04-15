package pm_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/pm"
)

// fakeSidecar drives the Go→sidecar and sidecar→Go pipes for a PiPM under test.
// Tests configure responders keyed on command type.
type fakeSidecar struct {
	t          *testing.T
	stdin      *io.PipeReader // sidecar reads commands from here
	stdout     *io.PipeWriter // sidecar writes responses here
	onIntake   func(cmd map[string]any) map[string]any
	onRoute    func(cmd map[string]any) map[string]any
	resetCount atomic.Int32
	done       chan struct{}
}

func newFakeSidecar(t *testing.T) (*fakeSidecar, *pm.PiPM) {
	t.Helper()
	// Pipes: PiPM writes to fakeStdin (which fake sidecar reads);
	//        fake sidecar writes to fakeStdout (which PiPM reads).
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	fs := &fakeSidecar{
		t:      t,
		stdin:  stdinR,
		stdout: stdoutW,
		done:   make(chan struct{}),
	}

	p := pm.NewFromPipes(stdinW, stdoutR, pm.Config{
		Model:               "claude-haiku-4-5",
		ConfidenceThreshold: 0.8,
	})

	go fs.run()
	return fs, p
}

func (fs *fakeSidecar) run() {
	defer close(fs.done)
	scanner := bufio.NewScanner(fs.stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var cmd map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			continue
		}
		switch cmd["type"] {
		case "intake":
			if fs.onIntake == nil {
				continue
			}
			resp := fs.onIntake(cmd)
			fs.writeJSON(resp)
		case "route":
			if fs.onRoute == nil {
				continue
			}
			resp := fs.onRoute(cmd)
			fs.writeJSON(resp)
		case "reset":
			fs.resetCount.Add(1)
		}
	}
}

func (fs *fakeSidecar) writeJSON(v map[string]any) {
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	_, _ = fs.stdout.Write(b)
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestPiPM_IntakeNeedsMore(t *testing.T) {
	fs, p := newFakeSidecar(t)
	defer p.Close()

	fs.onIntake = func(cmd map[string]any) map[string]any {
		return map[string]any{
			"type":       "intake_result",
			"id":         cmd["id"],
			"needs_more": true,
			"question":   "what are the constraints?",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := p.Intake(ctx, call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if !res.NeedsMore {
		t.Error("NeedsMore = false, want true")
	}
	if res.Question != "what are the constraints?" {
		t.Errorf("Question = %q", res.Question)
	}
}

func TestPiPM_IntakeObjective(t *testing.T) {
	fs, p := newFakeSidecar(t)
	defer p.Close()

	fs.onIntake = func(cmd map[string]any) map[string]any {
		return map[string]any{
			"type":       "intake_result",
			"id":         cmd["id"],
			"needs_more": false,
			"objective": map[string]any{
				"goal":                "add feature flag system",
				"acceptance_criteria": []string{"flag defaults off"},
				"constraints":         []string{"no breaking changes"},
				"notes":               []string{},
				"model_hint":          "sonnet",
				"spoken_summary":      "build a feature flag system that defaults off",
			},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := p.Intake(ctx, call.IntakeInput{Latest: "build a feature flag"})
	if err != nil {
		t.Fatalf("Intake error: %v", err)
	}
	if res.NeedsMore {
		t.Error("NeedsMore = true, want false")
	}
	if res.Objective == nil || res.Objective.Goal != "add feature flag system" {
		t.Errorf("Objective = %+v", res.Objective)
	}
	if res.Objective.ModelHint != "sonnet" {
		t.Errorf("ModelHint = %q", res.Objective.ModelHint)
	}
}

func TestPiPM_RouteAnswerInlineAboveThreshold(t *testing.T) {
	fs, p := newFakeSidecar(t)
	defer p.Close()

	fs.onRoute = func(cmd map[string]any) map[string]any {
		return map[string]any{
			"type":          "route_result",
			"id":            cmd["id"],
			"answer_inline": "yes",
			"confidence":    0.95,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := p.Route(ctx, call.RouteInput{Question: "use existing client?"})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.AnswerInline != "yes" {
		t.Errorf("AnswerInline = %q", res.AnswerInline)
	}
	if res.SpokenQuestion != "" {
		t.Errorf("SpokenQuestion = %q, want empty", res.SpokenQuestion)
	}
}

func TestPiPM_RouteAnswerInlineBelowThreshold(t *testing.T) {
	fs, p := newFakeSidecar(t)
	defer p.Close()

	fs.onRoute = func(cmd map[string]any) map[string]any {
		return map[string]any{
			"type":          "route_result",
			"id":            cmd["id"],
			"answer_inline": "maybe",
			"confidence":    0.5,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := p.Route(ctx, call.RouteInput{Question: "use existing client?"})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.AnswerInline != "" {
		t.Errorf("AnswerInline = %q, want empty (low confidence should escalate)", res.AnswerInline)
	}
	if res.SpokenQuestion != "use existing client?" {
		t.Errorf("SpokenQuestion = %q", res.SpokenQuestion)
	}
}

func TestPiPM_RouteEscalate(t *testing.T) {
	fs, p := newFakeSidecar(t)
	defer p.Close()

	fs.onRoute = func(cmd map[string]any) map[string]any {
		return map[string]any{
			"type":            "route_result",
			"id":              cmd["id"],
			"spoken_question": "should i use the existing auth client?",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := p.Route(ctx, call.RouteInput{Question: "use existing?"})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if res.SpokenQuestion != "should i use the existing auth client?" {
		t.Errorf("SpokenQuestion = %q", res.SpokenQuestion)
	}
}

func TestPiPM_ResetSendsResetCommand(t *testing.T) {
	fs, p := newFakeSidecar(t)
	defer p.Close()

	fs.onIntake = func(cmd map[string]any) map[string]any {
		return map[string]any{
			"type":       "intake_result",
			"id":         cmd["id"],
			"needs_more": true,
			"question":   "ok",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = p.Intake(ctx, call.IntakeInput{Latest: "first"})
	p.Reset()
	// Give the reset a moment to be read by the fake sidecar loop.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fs.resetCount.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := fs.resetCount.Load(); got != 1 {
		t.Errorf("resetCount = %d, want 1", got)
	}
}

func TestPiPM_SidecarError(t *testing.T) {
	fs, p := newFakeSidecar(t)
	defer p.Close()

	fs.onIntake = func(cmd map[string]any) map[string]any {
		return map[string]any{
			"type":    "error",
			"id":      cmd["id"],
			"message": "model not found",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := p.Intake(ctx, call.IntakeInput{Latest: "x"})
	if err == nil {
		t.Fatal("expected error from sidecar error message")
	}
}
