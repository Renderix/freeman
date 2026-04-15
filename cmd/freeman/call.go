package main

import (
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
	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/conv"
	"github.com/Renderix/freeman/internal/engine"
	"github.com/spf13/cobra"
)

var callCmd = &cobra.Command{
	Use:   "call",
	Short: "Start a Freeman voice call",
	Long: `Start a Freeman voice call. Uses real audio hardware (mic + speakers),
Whisper for STT, Kokoro for TTS, and a long-lived pi-coding-agent
chat session that can spawn background coding tasks on demand.

Requires pi-coding-agent subscription auth: run scripts/pi_login.sh
once before first use.`,
	RunE: runCall,
}

func runCall(cmd *cobra.Command, args []string) error {
	conf := config.LoadConfig(configFile)

	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
	defer func() {
		if err := mgr.Stop(); err != nil {
			logger.Error("whisper-server stop", "err", err)
		}
	}()

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

	// 6. STT Transcriber.
	client := stt.NewClient(mgr.BaseURL(), 10*time.Second)
	tr := stt.NewTranscriber(client, uttCh, 16000)
	tr.Run(ctx)
	defer tr.Stop()

	// 7. Speaker, muting VAD + Transcriber together.
	muter := &audio.MultiMuter{Muters: []audio.Muter{v, tr}}
	sp, err := playback.Open(actx, playback.Config{
		DeviceID: conf.Freeman.Audio.OutputDevice,
		ChunkMs:  50,
		Voice:    conf.TTS.DefaultVoice,
		Speed:    conf.TTS.DefaultSpeed,
	}, eng, muter)
	if err != nil {
		return fmt.Errorf("playback open: %w", err)
	}
	defer sp.Close()

	// 8. Hotkey.
	hk, err := hotkey.Open(hotkey.Config{
		Mode: conf.Freeman.Hotkey.Mode,
		Key:  conf.Freeman.Hotkey.Key,
	})
	if err != nil {
		return fmt.Errorf("hotkey open: %w", err)
	}
	defer hk.Stop()

	// 9. TaskManager (no task running yet).
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	taskMgr := conv.NewTaskManager(repoRoot)
	defer taskMgr.Close()

	// 10. Conv session.
	convSession, err := conv.NewSession(ctx, conv.Deps{
		Transcriber:  tr,
		Speaker:      sp,
		Hotkey:       hk,
		SpeechOnsets: v.SpeechOnsets(),
		TaskManager:  taskMgr,
		RepoRoot:     repoRoot,
		Model:        conf.Freeman.PM.Model,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("conv session: %w", err)
	}
	defer convSession.Close()

	fmt.Fprintln(os.Stderr, "freeman: ready")
	return convSession.Run(ctx)
}

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
