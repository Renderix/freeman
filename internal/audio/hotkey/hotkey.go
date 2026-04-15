// Package hotkey provides a terminal-based hotkey that posts an event whenever
// the user presses a configured key. In TTY mode it puts the terminal into raw
// mode and reads single bytes; in stdin-line mode (fallback for non-TTY stdin)
// it posts on every newline.
package hotkey

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// Config selects the mode and the target key.
type Config struct {
	Mode string // "tty" | "stdin-line"
	Key  string // "enter" | "space"
}

// Hotkey is the public type returned by Open.
type Hotkey struct {
	events    chan struct{}
	stopOnce  sync.Once
	stopCh    chan struct{}
	restoreFn func()
}

func (h *Hotkey) Events() <-chan struct{} { return h.events }

func (h *Hotkey) Stop() {
	h.stopOnce.Do(func() {
		close(h.stopCh)
		if h.restoreFn != nil {
			h.restoreFn()
		}
	})
}

// Open constructs a Hotkey based on cfg. If cfg.Mode is "tty" but stdin is not
// a TTY, it falls back to stdin-line mode and prints a notice to stderr.
func Open(cfg Config) (*Hotkey, error) {
	if cfg.Key == "" {
		cfg.Key = "enter"
	}
	if cfg.Mode == "" {
		cfg.Mode = "tty"
	}
	h := &Hotkey{
		events: make(chan struct{}, 4),
		stopCh: make(chan struct{}),
	}
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if cfg.Mode == "tty" && !isTTY {
		fmt.Fprintln(os.Stderr, "freeman: stdin is not a TTY; hotkey falls back to line mode")
		cfg.Mode = "stdin-line"
	}
	switch cfg.Mode {
	case "tty":
		return h, h.startTTY(cfg.Key)
	case "stdin-line":
		h.restoreFn = func() {}
		go runStdinLine(os.Stdin, h.events, h.stopCh)
		return h, nil
	default:
		return nil, fmt.Errorf("unknown hotkey mode %q", cfg.Mode)
	}
}

func (h *Hotkey) startTTY(key string) error {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("tty raw mode: %w", err)
	}
	h.restoreFn = func() { _ = term.Restore(fd, oldState) }

	// Also restore on SIGINT / SIGTERM so a crash doesn't wedge the shell.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			h.Stop()
		case <-h.stopCh:
			signal.Stop(sigCh)
		}
	}()

	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-h.stopCh:
				return
			default:
			}
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			if n == 0 {
				continue
			}
			if matchKey(key, rune(buf[0])) {
				select {
				case h.events <- struct{}{}:
				default:
				}
			}
			if buf[0] == 0x03 { // Ctrl-C in raw mode
				h.Stop()
				_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
				return
			}
		}
	}()
	return nil
}

// matchKey maps the config key name to a byte match.
func matchKey(name string, r rune) bool {
	switch strings.ToLower(name) {
	case "enter":
		return r == '\r' || r == '\n'
	case "space":
		return r == ' '
	default:
		return false
	}
}

// runStdinLine drives the stdin-line fallback: one event per newline from r.
// Runs until r hits EOF or stopCh closes.
func runStdinLine(r io.Reader, events chan<- struct{}, stopCh <-chan struct{}) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-stopCh:
			return
		default:
		}
		select {
		case events <- struct{}{}:
		default:
		}
	}
}

// stdinLineHotkey is a test-only wrapper that exposes the runStdinLine
// goroutine over an owned channel, so unit tests can feed it a bytes.Buffer
// without touching os.Stdin or TTY plumbing.
type stdinLineHotkey struct {
	events chan struct{}
	stopCh chan struct{}
	reader io.Reader
}

func newStdinLineHotkey(r io.Reader) *stdinLineHotkey {
	return &stdinLineHotkey{
		events: make(chan struct{}, 4),
		stopCh: make(chan struct{}),
		reader: r,
	}
}

func (s *stdinLineHotkey) Events() <-chan struct{} { return s.events }

func (s *stdinLineHotkey) run() {
	go runStdinLine(s.reader, s.events, s.stopCh)
}
