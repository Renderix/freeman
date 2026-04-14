package call_test

import (
	"bytes"
	"context"
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

