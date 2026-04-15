package call_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/call/fakes"
	"github.com/Renderix/freeman/internal/sidecar"
)

// syncBuffer is a bytes.Buffer protected by a mutex so the session goroutine
// can write (via Speaker.Speak) concurrently with the test goroutine reading
// (via waitFor) without triggering the race detector.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestSession_HappyPath(t *testing.T) {
	// Pipes simulating a sidecar process.
	sidecarStdinR, sidecarStdinW := io.Pipe()
	sidecarStdoutR, sidecarStdoutW := io.Pipe()
	client := sidecar.NewClientFromPipes(sidecarStdinW, sidecarStdoutR)
	defer client.Close()
	defer sidecarStdinW.Close()

	// Stub sidecar goroutine.
	sidecarDone := make(chan struct{})
	go func() {
		defer close(sidecarDone)
		defer sidecarStdoutW.Close()
		scanner := newLineScanner(sidecarStdinR)
		// First line: start.
		if !scanner.Scan() {
			return
		}
		_, err := sidecar.Decode(scanner.Bytes())
		if err != nil {
			return
		}
		// Emit ask_user.
		_ = sidecar.Encode(sidecarStdoutW, sidecar.AskUserMsg{
			Type: sidecar.MsgTypeAskUser, ID: "q1", Question: "use existing?",
		})
		// Read reply.
		if !scanner.Scan() {
			return
		}
		reply, _ := sidecar.Decode(scanner.Bytes())
		r, ok := reply.(sidecar.AskUserReplyMsg)
		if !ok || r.ID != "q1" {
			return
		}
		// Emit done.
		_ = sidecar.Encode(sidecarStdoutW, sidecar.DoneMsg{
			Type: sidecar.MsgTypeDone, Summary: "ok",
		})
	}()

	// Fakes.
	var speakerBuf syncBuffer
	speaker := fakes.NewStdoutSpeaker(&speakerBuf)

	// The transcriber + hotkey are driven by in-memory buffers that we write
	// to as the test progresses.
	trInput, trWriter := io.Pipe()
	tr := fakes.NewLineReaderTranscriber(trInput)
	defer tr.Stop()

	hkInput, hkWriter := io.Pipe()
	hk := fakes.NewStdinHotkey(hkInput)
	defer hk.Stop()

	pm := fakes.NewScriptedPM()

	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     speaker,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     client,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- session.Run(ctx)
	}()

	// Drive the flow.
	// 1. Press hotkey → session enters Intake, speaks greeting.
	_, _ = hkWriter.Write([]byte("\n"))
	waitFor(t, &speakerBuf, "what are we building", 1*time.Second)

	// 2. Utterance 1 → PM returns NeedsMore → speaker gets follow-up.
	_, _ = trWriter.Write([]byte("build a feature flag\n"))
	waitFor(t, &speakerBuf, "constraints", 1*time.Second)

	// 3. Utterance 2 → PM returns Objective → speaker gets confirmation.
	_, _ = trWriter.Write([]byte("off, 10 percent\n"))
	waitFor(t, &speakerBuf, "should i start", 1*time.Second)

	// 4. Confirm → dispatch → sidecar gets start, emits ask_user.
	// PM routes inline → session replies → sidecar emits done → speaker gets summary.
	_, _ = trWriter.Write([]byte("yes\n"))
	waitFor(t, &speakerBuf, "done", 2*time.Second)

	cancel()
	<-runDone
	<-sidecarDone
}

// waitFor polls buf until it contains want, or fails.
func waitFor(t *testing.T, buf *syncBuffer, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in speaker output; got:\n%s", want, buf.String())
}

// newLineScanner wraps an io.Reader in a bufio.Scanner configured for JSONL.
func newLineScanner(r io.Reader) interface {
	Scan() bool
	Bytes() []byte
} {
	s := &lineScanner{r: r, buf: make([]byte, 0, 4096)}
	return s
}

type lineScanner struct {
	r    io.Reader
	buf  []byte
	line []byte
}

func (s *lineScanner) Scan() bool {
	s.line = nil
	for {
		if i := bytes.IndexByte(s.buf, '\n'); i >= 0 {
			s.line = append([]byte(nil), s.buf[:i]...)
			s.buf = s.buf[i+1:]
			return true
		}
		tmp := make([]byte, 4096)
		n, err := s.r.Read(tmp)
		if n > 0 {
			s.buf = append(s.buf, tmp[:n]...)
		}
		if err != nil {
			return false
		}
	}
}

func (s *lineScanner) Bytes() []byte { return s.line }

// TestSession_BargeinCancelsSpeak: inject SpeakEffect, then send a speech onset
// before Speak completes. Assert the next UserUtterance carries InterruptedText.
func TestSession_BargeinCancelsSpeak(t *testing.T) {
	// Sidecar pipes (not used in this test but NewSession requires a *sidecar.Client).
	scStdinR, scStdinW := io.Pipe()
	scStdoutR, scStdoutW := io.Pipe()
	defer scStdinW.Close()
	defer scStdoutW.Close()
	defer scStdoutR.Close()
	defer scStdinR.Close()
	sc := sidecar.NewClientFromPipes(scStdinW, scStdoutR)
	defer sc.Close()

	var speakerBuf syncBuffer
	// slowSpeaker blocks until its context is canceled, simulating long TTS.
	slowSpeaker := &slowCancelSpeaker{buf: &speakerBuf}

	trInput, trWriter := io.Pipe()
	tr := fakes.NewLineReaderTranscriber(trInput)
	defer tr.Stop()

	hkInput, hkWriter := io.Pipe()
	hk := fakes.NewStdinHotkey(hkInput)
	defer hk.Stop()

	pm := fakes.NewScriptedPM()
	onsets := make(chan struct{}, 1)

	session := call.NewSession(call.SessionDeps{
		Transcriber:  tr,
		Speaker:      slowSpeaker,
		PM:           pm,
		Hotkey:       hk,
		Sidecar:      sc,
		SpeechOnsets: onsets,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(ctx) }()

	// Press hotkey → enters Intake, greeting SpeakEffect fires (async).
	_, _ = hkWriter.Write([]byte("\n"))

	// Wait for Speak to have started (slowSpeaker sets a flag).
	if !slowSpeaker.waitStarted(time.Second) {
		t.Fatal("Speak never started")
	}

	// Fire a speech onset — this should cancel the in-flight Speak.
	onsets <- struct{}{}

	// Wait for Speak to have been canceled.
	if !slowSpeaker.waitCanceled(time.Second) {
		t.Fatal("Speak was not canceled after onset")
	}

	// Send an utterance — it should carry InterruptedText.
	_, _ = trWriter.Write([]byte("build a feature flag\n"))

	// PM will be called; wait for the follow-up question (NeedsMore=true).
	waitFor(t, &speakerBuf, "constraints", time.Second)

	cancel()
	<-runDone
}

// TestSession_NoInterruptedTextWithoutBargein: Speak completes normally;
// the next UserUtterance has empty InterruptedText.
func TestSession_NoInterruptedTextWithoutBargein(t *testing.T) {
	scStdinR, scStdinW := io.Pipe()
	scStdoutR, scStdoutW := io.Pipe()
	defer scStdinW.Close()
	defer scStdoutW.Close()
	defer scStdoutR.Close()
	defer scStdinR.Close()
	sc := sidecar.NewClientFromPipes(scStdinW, scStdoutR)
	defer sc.Close()

	var speakerBuf syncBuffer
	speaker := fakes.NewStdoutSpeaker(&speakerBuf)

	trInput, trWriter := io.Pipe()
	tr := fakes.NewLineReaderTranscriber(trInput)
	defer tr.Stop()

	hkInput, hkWriter := io.Pipe()
	hk := fakes.NewStdinHotkey(hkInput)
	defer hk.Stop()

	pm := &recordingPM{inner: fakes.NewScriptedPM()}
	onsets := make(chan struct{}, 1) // never sent

	session := call.NewSession(call.SessionDeps{
		Transcriber:  tr,
		Speaker:      speaker,
		PM:           pm,
		Hotkey:       hk,
		Sidecar:      sc,
		SpeechOnsets: onsets,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(ctx) }()

	_, _ = hkWriter.Write([]byte("\n"))
	waitFor(t, &speakerBuf, "what are we building", time.Second)

	_, _ = trWriter.Write([]byte("build a feature flag\n"))
	waitFor(t, &speakerBuf, "constraints", time.Second)

	if pm.lastInterruptedText() != "" {
		t.Errorf("InterruptedText = %q, want empty", pm.lastInterruptedText())
	}

	cancel()
	<-runDone
}

// slowCancelSpeaker blocks until its context is canceled, then returns.
// Used to simulate long TTS that can be barged in on.
type slowCancelSpeaker struct {
	buf     *syncBuffer
	mu      sync.Mutex
	started bool
	done    bool
}

func (s *slowCancelSpeaker) Speak(ctx context.Context, text string) error {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	_, _ = fmt.Fprintf(s.buf, "[tts] %s\n", text)
	<-ctx.Done()
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	return ctx.Err()
}

func (s *slowCancelSpeaker) waitStarted(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		v := s.started
		s.mu.Unlock()
		if v {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func (s *slowCancelSpeaker) waitCanceled(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		v := s.done
		s.mu.Unlock()
		if v {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// recordingPM wraps ScriptedPM and records the last IntakeInput.
type recordingPM struct {
	inner *fakes.ScriptedPM
	mu    sync.Mutex
	last  call.IntakeInput
}

func (p *recordingPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error) {
	p.mu.Lock()
	p.last = in
	p.mu.Unlock()
	return p.inner.Intake(ctx, in)
}

func (p *recordingPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error) {
	return p.inner.Route(ctx, in)
}

func (p *recordingPM) Reset() { p.inner.Reset() }

func (p *recordingPM) lastInterruptedText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last.InterruptedText
}
