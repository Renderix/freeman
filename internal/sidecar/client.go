package sidecar

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// Client manages stdin/stdout JSONL communication with a sidecar process.
type Client struct {
	stdin  io.Writer
	stdout io.Reader
	events chan Message
	closer io.Closer // optional; non-nil when Spawn was used
	proc   *exec.Cmd // optional
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// NewClientFromPipes wires a client to existing stdin/stdout pipes.
// Use this in tests. In production, call Spawn instead.
func NewClientFromPipes(stdin io.Writer, stdout io.Reader) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		stdin:  stdin,
		stdout: stdout,
		events: make(chan Message, 16),
		ctx:    ctx,
		cancel: cancel,
	}
	c.wg.Add(1)
	go c.readLoop()
	return c
}

// Spawn launches a subprocess and wires its stdin/stdout to a new Client.
// Example: Spawn(ctx, "bun", "run", "sidecar/stub.ts")
//
// The subprocess stderr is routed through a LinePrefixWriter so that its
// output is clearly labelled "[sidecar:task] " in Freeman's stderr stream
// and does not appear as bare, untagged lines mixed with Go slog output.
func Spawn(ctx context.Context, name string, args ...string) (*Client, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Route task-sidecar stderr through a prefix writer so its log lines are
	// clearly labelled and distinguishable from Freeman's own slog output.
	cmd.Stderr = NewLinePrefixWriter(os.Stderr, "[sidecar:task]")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	clientCtx, cancel := context.WithCancel(context.Background())
	c := &Client{
		stdin:  stdin,
		stdout: stdout,
		events: make(chan Message, 16),
		closer: stdin,
		proc:   cmd,
		ctx:    clientCtx,
		cancel: cancel,
	}
	c.wg.Add(1)
	go c.readLoop()
	return c, nil
}

// Events returns the channel of inbound messages from the sidecar.
// The channel is closed when the sidecar stdout EOFs or Close is called.
func (c *Client) Events() <-chan Message { return c.events }

// Send writes a single JSONL message to the sidecar stdin.
func (c *Client) Send(m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("sidecar client closed")
	}
	return Encode(c.stdin, m)
}

// Close shuts down the client. Safe to call multiple times.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.wg.Wait()
		return nil
	}
	c.closed = true
	closer := c.closer
	proc := c.proc
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if closer != nil {
		_ = closer.Close()
	}
	if proc != nil {
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}
	if rc, ok := c.stdout.(io.Closer); ok {
		_ = rc.Close()
	}
	c.wg.Wait()
	return nil
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	defer close(c.events)
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := Decode(line)
		if err != nil {
			msg = ErrorMsg{Type: MsgTypeError, Message: err.Error()}
		}
		select {
		case c.events <- msg:
		case <-c.ctx.Done():
			return
		}
	}
}
