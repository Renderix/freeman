package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// acquireSingleInstance ensures only one `freeman call` runs at a time.
// Uses an advisory flock on ~/.freeman/daemon.lock. The lock is
// released automatically when the process exits — clean shutdown,
// SIGKILL, or crash all clear it without manual cleanup. A stale PID
// file from an old crash is harmless: flock looks at the kernel's
// lock table, not file contents.
//
// Returns an error if another freeman call is already holding the
// lock. Callers should propagate it so the launchd-spawned daemon
// logs a clean "already running" message and exits (no KeepAlive
// means it stays down — the user's existing instance keeps going).
func acquireSingleInstance() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	lockPath := filepath.Join(home, ".freeman", "daemon.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	// Non-blocking exclusive lock. If held, errno EWOULDBLOCK.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		existing, _ := os.ReadFile(lockPath)
		_ = f.Close()
		return fmt.Errorf("another freeman call is already running (pid %s, lock %s) — stop it via the menu bar Disable button, `freeman daemon stop`, or the 'disengage' wake word", trimPID(string(existing)), lockPath)
	}
	// Stamp our PID for diagnostics and keep fd open for the process
	// lifetime — the lock releases on fd close (or exit).
	_, _ = f.Seek(0, 0)
	_ = f.Truncate(0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return nil
}

func trimPID(s string) string {
	for i, c := range s {
		if c == '\n' || c == '\r' || c == ' ' {
			return s[:i]
		}
	}
	return s
}
