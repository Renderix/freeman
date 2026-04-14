package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/call/fakes"
	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/sidecar"
	"github.com/spf13/cobra"
)

var callCmd = &cobra.Command{
	Use:   "call",
	Short: "Start a Freeman voice call (Plan 1: fakes + stub sidecar)",
	Long: `Plan 1 harness: reads user utterances as lines from stdin,
writes spoken output as '[tts] ...' lines to stdout, uses a scripted
PM, and spawns the Bun TypeScript stub sidecar.

Send SIGUSR1 to the process to simulate a hotkey press.`,
	RunE: runCall,
}

func runCall(cmd *cobra.Command, args []string) error {
	_ = config.LoadConfig(configFile) // not yet used in Plan 1; ensures config is loadable

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// 1. Spawn the Bun stub sidecar.
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	stubPath := filepath.Join(repoRoot, "sidecar", "stub.ts")
	sc, err := sidecar.Spawn(ctx, "bun", "run", stubPath)
	if err != nil {
		return fmt.Errorf("spawn sidecar: %w", err)
	}
	defer sc.Close()

	// 2. Build fakes.
	tr := fakes.NewLineReaderTranscriber(os.Stdin)
	defer tr.Stop()

	speaker := fakes.NewStdoutSpeaker(os.Stdout)
	pm := fakes.NewScriptedPM()

	// 3. Hotkey via SIGUSR1.
	hkChan := make(chan struct{}, 4)
	sigChan := make(chan os.Signal, 4)
	signal.Notify(sigChan, syscall.SIGUSR1)
	go func() {
		for range sigChan {
			hkChan <- struct{}{}
		}
	}()
	hk := &channelHotkey{ch: hkChan}

	// 4. Session.
	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     speaker,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     sc,
	})

	fmt.Fprintln(os.Stderr, "freeman: ready. send SIGUSR1 to start a call, then type utterances as lines.")
	fmt.Fprintf(os.Stderr, "freeman: pid=%d\n", os.Getpid())
	return session.Run(ctx)
}

// channelHotkey adapts a plain channel to the Hotkey interface.
type channelHotkey struct{ ch chan struct{} }

func (c *channelHotkey) Events() <-chan struct{} { return c.ch }
func (c *channelHotkey) Stop()                   {}

// findRepoRoot walks up from the current working directory until it finds go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found walking up from cwd")
		}
		dir = parent
	}
}
