package fakes

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/Renderix/freeman/internal/call"
)

// Compile-time checks that each fake implements its port interface.
var (
	_ call.Speaker     = (*StdoutSpeaker)(nil)
	_ call.Transcriber = (*LineReaderTranscriber)(nil)
	_ call.Hotkey      = (*StdinHotkey)(nil)
	_ call.PM          = (*ScriptedPM)(nil)
)

// StdoutSpeaker prints "[tts] <text>" to the given writer.
type StdoutSpeaker struct {
	w  io.Writer
	mu sync.Mutex
}

// NewStdoutSpeaker returns a Speaker that writes to w.
func NewStdoutSpeaker(w io.Writer) *StdoutSpeaker {
	return &StdoutSpeaker{w: w}
}

// Speak implements call.Speaker.
func (s *StdoutSpeaker) Speak(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := fmt.Fprintf(s.w, "[tts] %s\n", text)
	return err
}

// LineReaderTranscriber reads lines from an io.Reader and emits each as an utterance.
type LineReaderTranscriber struct {
	r    io.Reader
	out  chan string
	stop chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// NewLineReaderTranscriber returns a Transcriber that reads lines from r.
func NewLineReaderTranscriber(r io.Reader) *LineReaderTranscriber {
	t := &LineReaderTranscriber{
		r:    r,
		out:  make(chan string, 4),
		stop: make(chan struct{}),
	}
	t.wg.Add(1)
	go t.loop()
	return t
}

// Utterances implements call.Transcriber.
func (t *LineReaderTranscriber) Utterances() <-chan string { return t.out }

// Stop implements call.Transcriber.
func (t *LineReaderTranscriber) Stop() {
	t.once.Do(func() {
		if rc, ok := t.r.(io.Closer); ok {
			_ = rc.Close()
		}
		close(t.stop)
	})
}

func (t *LineReaderTranscriber) loop() {
	defer t.wg.Done()
	defer close(t.out)
	scanner := bufio.NewScanner(t.r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		select {
		case <-t.stop:
			return
		case t.out <- line:
		}
	}
}

// StdinHotkey emits one hotkey event per line read from r.
// For Plan 1 the CLI uses a different input stream for hotkey vs utterances,
// but tests instantiate this directly.
type StdinHotkey struct {
	r    io.Reader
	out  chan struct{}
	stop chan struct{}
	once sync.Once
}

// NewStdinHotkey returns a Hotkey that emits one event per line read from r.
func NewStdinHotkey(r io.Reader) *StdinHotkey {
	h := &StdinHotkey{
		r:    r,
		out:  make(chan struct{}, 4),
		stop: make(chan struct{}),
	}
	go h.loop()
	return h
}

// Events implements call.Hotkey.
func (h *StdinHotkey) Events() <-chan struct{} { return h.out }

// Stop implements call.Hotkey.
func (h *StdinHotkey) Stop() {
	h.once.Do(func() {
		if rc, ok := h.r.(io.Closer); ok {
			_ = rc.Close()
		}
		close(h.stop)
	})
}

func (h *StdinHotkey) loop() {
	defer close(h.out)
	scanner := bufio.NewScanner(h.r)
	for scanner.Scan() {
		select {
		case <-h.stop:
			return
		case h.out <- struct{}{}:
		}
	}
}

// ScriptedPM is a deterministic fake PM:
//   - Intake: first call returns NeedsMore; second call returns a completed Objective.
//   - Route:  always returns AnswerInline: "yes".
type ScriptedPM struct {
	mu        sync.Mutex
	intakeCnt int
}

// NewScriptedPM returns a fake PM.
func NewScriptedPM() *ScriptedPM { return &ScriptedPM{} }

// Intake implements call.PM.
func (p *ScriptedPM) Intake(ctx context.Context, in call.IntakeInput) (call.PMIntakeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.intakeCnt++
	if p.intakeCnt == 1 {
		return call.PMIntakeResult{
			NeedsMore: true,
			Question:  "tell me more about constraints.",
		}, nil
	}
	return call.PMIntakeResult{
		NeedsMore: false,
		Objective: &call.Objective{
			Goal:               "scripted goal from: " + in.Latest,
			AcceptanceCriteria: []string{"tests pass"},
			Constraints:        []string{"no breaking changes"},
			ModelHint:          "sonnet",
			SpokenSummary:      "scripted summary.",
		},
	}, nil
}

// Route implements call.PM.
func (p *ScriptedPM) Route(ctx context.Context, in call.RouteInput) (call.PMRouteResult, error) {
	return call.PMRouteResult{
		AnswerInline: "yes",
	}, nil
}
