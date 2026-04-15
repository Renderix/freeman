package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Renderix/freeman/internal/audio"
	"github.com/Renderix/freeman/internal/audio/capture"
	"github.com/Renderix/freeman/internal/audio/hotkey"
	"github.com/Renderix/freeman/internal/audio/playback"
	"github.com/Renderix/freeman/internal/audio/stt"
	"github.com/Renderix/freeman/internal/audio/vad"
	"github.com/Renderix/freeman/internal/call"
	"github.com/Renderix/freeman/internal/call/fakes"
	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/engine"
	"github.com/Renderix/freeman/internal/sidecar"
	"github.com/spf13/cobra"
)

var fakeAudio bool

func init() {
	callCmd.Flags().BoolVar(&fakeAudio, "fake-audio", false, "use Plan 1 stdin/stdout audio fakes (headless testing)")
}

var callCmd = &cobra.Command{
	Use:   "call",
	Short: "Start a Freeman voice call",
	Long: `Start a Freeman voice call. By default uses real audio hardware.

Use --fake-audio for Plan 1 harness: reads user utterances as lines from stdin,
writes spoken output as '[tts] ...' lines to stdout, uses a scripted
PM, and spawns the Bun TypeScript stub sidecar.

Send SIGUSR1 to the process to simulate a hotkey press (fake-audio mode).`,
	RunE: runCall,
}

func runCall(cmd *cobra.Command, args []string) error {
	conf := config.LoadConfig(configFile)

	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if fakeAudio {
		return runCallWithFakes(ctx, conf)
	}
	return runCallWithRealAudio(ctx, conf)
}

func runCallWithFakes(ctx context.Context, conf config.Config) error {
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

	tr := fakes.NewLineReaderTranscriber(os.Stdin)
	defer tr.Stop()

	speaker := fakes.NewStdoutSpeaker(os.Stdout)
	pm := fakes.NewScriptedPM()

	hkChan := make(chan struct{}, 4)
	sigChan := make(chan os.Signal, 4)
	signal.Notify(sigChan, syscall.SIGUSR1)
	defer func() {
		signal.Stop(sigChan)
		close(sigChan)
	}()
	go func() {
		for range sigChan {
			hkChan <- struct{}{}
		}
	}()
	hk := &channelHotkey{ch: hkChan}

	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     speaker,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     sc,
	})

	fmt.Fprintln(os.Stderr, "freeman: ready. SIGUSR1 to start a call, type utterances as lines.")
	fmt.Fprintf(os.Stderr, "freeman: pid=%d\n", os.Getpid())
	return session.Run(ctx)
}

func runCallWithRealAudio(ctx context.Context, conf config.Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 1. Shared audio context (malgo).
	actx, err := audio.New(logger)
	if err != nil {
		return fmt.Errorf("audio init: %w", err)
	}
	defer actx.Close()

	// 2. Kokoro engine (TTS).
	eng, err := engine.NewTTSEngine(
		filepath.Join(conf.Model.Dir, conf.Model.ModelFile),
		filepath.Join(conf.Model.Dir, conf.Model.VoicesFile),
		filepath.Join(conf.Model.Dir, conf.Model.TokensFile),
		filepath.Join(conf.Model.Dir, conf.Model.DataDir),
	)
	if err != nil {
		return fmt.Errorf("engine init: %w", err)
	}

	// 3. whisper-server subprocess.
	mgr := stt.NewManager(stt.ManagerConfig{
		ServerPath:       conf.Freeman.STT.ServerPath,
		Host:             "127.0.0.1",
		Port:             conf.Freeman.STT.ServerPort,
		ModelPath:        conf.Freeman.STT.ModelPath,
		StartupTimeoutMs: conf.Freeman.STT.StartupTimeoutMS,
	})
	fmt.Fprintln(os.Stderr, "freeman: warming up whisper…")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("whisper-server: %w", err)
	}
	defer mgr.Stop()

	// 4. Mic capture.
	cap, err := capture.Open(actx, capture.Config{
		DeviceID:   conf.Freeman.Audio.InputDevice,
		SampleRate: 16000,
		Channels:   1,
		FrameMs:    20,
	})
	if err != nil {
		return fmt.Errorf("capture open: %w", err)
	}
	defer cap.Stop()
	if err := cap.Start(); err != nil {
		return fmt.Errorf("capture start: %w", err)
	}

	// 5. VAD endpointing.
	v, err := vad.New(vad.Config{
		SilenceMs:      conf.Freeman.STT.VAD.SilenceMS,
		MinSpeechMs:    conf.Freeman.STT.VAD.MinSpeechMS,
		HangoverMs:     conf.Freeman.STT.VAD.HangoverMS,
		Aggressiveness: conf.Freeman.STT.VAD.Aggressiveness,
		SampleRate:     16000,
		FrameMs:        20,
	})
	if err != nil {
		return fmt.Errorf("vad init: %w", err)
	}
	uttCh := v.Run(ctx, cap.Frames())

	// 6. STT Transcriber (also implements audio.Muter).
	client := stt.NewClient(mgr.BaseURL(), 10*time.Second)
	tr := stt.NewTranscriber(client, uttCh, 16000)
	tr.Run(ctx)

	// 7. Playback Speaker, pointed at the Transcriber as its Muter.
	sp, err := playback.Open(actx, playback.Config{
		DeviceID: conf.Freeman.Audio.OutputDevice,
		ChunkMs:  50,
		Voice:    conf.TTS.DefaultVoice,
		Speed:    conf.TTS.DefaultSpeed,
	}, eng, tr)
	if err != nil {
		return fmt.Errorf("playback open: %w", err)
	}
	defer sp.Close()

	// 8. Hotkey (TTY or stdin-line).
	hk, err := hotkey.Open(hotkey.Config{
		Mode: conf.Freeman.Hotkey.Mode,
		Key:  conf.Freeman.Hotkey.Key,
	})
	if err != nil {
		return fmt.Errorf("hotkey open: %w", err)
	}
	defer hk.Stop()

	// 9. Stub sidecar (unchanged).
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

	fmt.Fprintln(os.Stderr, "freeman: ready")

	// 10. Session (ScriptedPM unchanged).
	pm := fakes.NewScriptedPM()
	session := call.NewSession(call.SessionDeps{
		Transcriber: tr,
		Speaker:     sp,
		PM:          pm,
		Hotkey:      hk,
		Sidecar:     sc,
	})
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
