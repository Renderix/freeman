package stt

import (
	"os/exec"
	"testing"
)

func TestManager_BuildArgs(t *testing.T) {
	cfg := ManagerConfig{
		ServerPath: "/bin/whisper-server",
		Host:       "127.0.0.1",
		Port:       17101,
		ModelPath:  "/models/ggml.bin",
		Threads:    4,
	}
	args := buildArgs(cfg)
	want := []string{
		"--model", "/models/ggml.bin",
		"--host", "127.0.0.1",
		"--port", "17101",
		"--threads", "4",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range args {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestManager_ResolveServerPath_Empty(t *testing.T) {
	// When ServerPath is empty, resolveServerPath should use exec.LookPath.
	got, err := resolveServerPath("")
	if err == nil && got == "" {
		t.Errorf("resolveServerPath returned empty path with nil error")
	}
	// Either the binary is in PATH (got != "") or an error is returned. Both fine.
	_ = exec.ErrNotFound
}
