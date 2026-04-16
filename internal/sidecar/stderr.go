package sidecar

import (
	"bytes"
	"io"
	"sync"
)

// NewLinePrefixWriter returns an io.Writer that prepends prefix to every
// newline-terminated line before forwarding it to dst. Partial lines (i.e.
// writes that don't end with '\n') are buffered until the terminating '\n'
// arrives. A final flush is performed when the writer is closed.
//
// This is used to label subprocess stderr so sidecar output is clearly
// distinguishable from Freeman's own structured slog output and can be
// filtered independently (e.g. grep -v '\[sidecar\]').
func NewLinePrefixWriter(dst io.Writer, prefix string) io.WriteCloser {
	return &linePrefixWriter{dst: dst, prefix: []byte(prefix)}
}

type linePrefixWriter struct {
	mu     sync.Mutex
	dst    io.Writer
	prefix []byte
	buf    bytes.Buffer
}

func (w *linePrefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			// No newline yet – buffer everything.
			w.buf.Write(p)
			break
		}
		// Write buffered partial line + the chunk up to and including '\n'.
		w.buf.Write(p[:idx+1])
		if err := w.flushLineLocked(); err != nil {
			return 0, err
		}
		p = p[idx+1:]
	}
	return total, nil
}

// Close flushes any buffered partial line (no trailing newline) and marks
// the writer done. Safe to call multiple times.
func (w *linePrefixWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return nil
	}
	// Partial line with no trailing newline – emit it anyway.
	w.buf.WriteByte('\n')
	return w.flushLineLocked()
}

// flushLineLocked writes prefix + w.buf to w.dst and resets the buffer.
// Must be called with w.mu held.
func (w *linePrefixWriter) flushLineLocked() error {
	line := w.buf.Bytes()
	w.buf.Reset()
	// Skip completely blank lines to reduce noise.
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	if _, err := w.dst.Write(w.prefix); err != nil {
		return err
	}
	_, err := w.dst.Write(line)
	return err
}
