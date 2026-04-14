package sidecar

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// NewClientFromPipes wires a client to existing stdin/stdout pipes.
// Use this in tests. In production, call Spawn instead.
func NewClientFromPipes(stdin io.Writer, stdout io.Reader) *Client {
	c := &Client{
		stdin:  stdin,
		stdout: stdout,
		events: make(chan Message, 16),
	}
	c.wg.Add(1)
	go c.readLoop()
	return c
}

// Spawn launches a subprocess and wires its stdin/stdout to a new Client.
// Example: Spawn(ctx, "bun", "run", "sidecar/stub.ts")
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
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	c := &Client{
		stdin:  stdin,
		stdout: stdout,
		events: make(chan Message, 16),
		closer: stdin,
		proc:   cmd,
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
		return nil
	}
	c.closed = true
	closer := c.closer
	proc := c.proc
	c.mu.Unlock()

	if closer != nil {
		_ = closer.Close()
	}
	if proc != nil {
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}
	// If stdout implements io.Closer (e.g. *io.PipeReader, *os.File), close it
	// so the readLoop's blocked Read returns immediately.
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
			// Wrap as ErrorMsg so the session sees it.
			c.events <- ErrorMsg{Type: MsgTypeError, Message: err.Error()}
			continue
		}
		c.events <- msg
	}
}
