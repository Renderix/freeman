package stt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ManagerConfig configures the whisper-server subprocess.
type ManagerConfig struct {
	ServerPath       string // empty = look up whisper-server in PATH
	Host             string // default 127.0.0.1
	Port             int    // 0 = pick ephemeral
	ModelPath        string
	Threads          int
	StartupTimeoutMs int
}

// Manager owns the whisper-server child process lifecycle.
type Manager struct {
	cfg       ManagerConfig
	cmd       *exec.Cmd
	baseURL   string
	stderrBuf *lineBuffer
	mu        sync.Mutex
	stopped   bool
}

// Start spawns whisper-server and blocks until it answers GET / or the timeout
// elapses. On failure it kills the child and returns an error with the last
// stderr lines attached for diagnostics.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bin, err := resolveServerPath(m.cfg.ServerPath)
	if err != nil {
		return err
	}
	if m.cfg.Host == "" {
		m.cfg.Host = "127.0.0.1"
	}
	if m.cfg.Port == 0 {
		p, err := pickEphemeralPort()
		if err != nil {
			return err
		}
		m.cfg.Port = p
	}
	if m.cfg.Threads == 0 {
		m.cfg.Threads = 4
	}
	if m.cfg.StartupTimeoutMs == 0 {
		m.cfg.StartupTimeoutMs = 10000
	}

	args := buildArgs(m.cfg)
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	m.stderrBuf = &lineBuffer{cap: 40}
	go m.stderrBuf.consume(stderrPipe)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start whisper-server: %w", err)
	}
	m.cmd = cmd
	m.baseURL = fmt.Sprintf("http://%s:%d", m.cfg.Host, m.cfg.Port)

	if err := m.waitReady(ctx); err != nil {
		_ = m.killLocked()
		return fmt.Errorf("%w\n--- whisper-server stderr ---\n%s", err, m.stderrBuf.String())
	}
	return nil
}

// BaseURL returns the http://host:port for use by Client.
func (m *Manager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.baseURL
}

// Stop terminates the subprocess with SIGTERM, then SIGKILL after a grace period.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.killLocked()
}

func (m *Manager) killLocked() error {
	if m.cmd == nil || m.cmd.Process == nil || m.stopped {
		return nil
	}
	m.stopped = true
	_ = m.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- m.cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		_ = m.cmd.Process.Kill()
		<-done
		return nil
	}
}

func (m *Manager) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(time.Duration(m.cfg.StartupTimeoutMs) * time.Millisecond)
	hc := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		req, err := http.NewRequestWithContext(ctx, "GET", m.baseURL+"/", nil)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("whisper-server readiness timed out")
}

func resolveServerPath(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("whisper-server not at %q: %w", configured, err)
		}
		return configured, nil
	}
	p, err := exec.LookPath("whisper-server")
	if err != nil {
		return "", fmt.Errorf("whisper-server not found in PATH; set freeman.stt.server_path in config")
	}
	return p, nil
}

func buildArgs(cfg ManagerConfig) []string {
	return []string{
		"--model", cfg.ModelPath,
		"--host", cfg.Host,
		"--port", strconv.Itoa(cfg.Port),
		"--threads", strconv.Itoa(cfg.Threads),
	}
}

func pickEphemeralPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// lineBuffer accumulates the last N lines of stderr for diagnostics.
type lineBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

func (b *lineBuffer) consume(r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		b.mu.Lock()
		b.lines = append(b.lines, s.Text())
		if len(b.lines) > b.cap {
			b.lines = b.lines[len(b.lines)-b.cap:]
		}
		b.mu.Unlock()
	}
	if err := s.Err(); err != nil {
		b.mu.Lock()
		b.lines = append(b.lines, "[scanner error: "+err.Error()+"]")
		b.mu.Unlock()
	}
}

func (b *lineBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := ""
	for _, l := range b.lines {
		out += l + "\n"
	}
	return out
}

// NewManager constructs a Manager; Start must be called before use.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{cfg: cfg}
}
