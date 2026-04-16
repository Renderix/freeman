package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Renderix/freeman/internal/agent/picoding"
	"github.com/Renderix/freeman/internal/audio"
	"github.com/Renderix/freeman/internal/audio/capture"
	"github.com/Renderix/freeman/internal/audio/wakeword"
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
Whisper for STT, Kokoro for TTS, and a long-lived chat agent session
that can spawn background coding tasks on demand.

Requires pi-coding-agent subscription auth: run scripts/pi_login.sh
once before first use.`,
	RunE: runCall,
}

func runCall(cmd *cobra.Command, args []string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(repoRoot, p)
	}

	conf := config.LoadConfig(resolve(configFile))

	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(newlineWriter{os.Stderr}, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 1. Shared audio context (malgo).
	actx, err := audio.New(logger)
	if err != nil {
		return fmt.Errorf("audio init: %w", err)
	}
	defer actx.Close()

	// 2. Kokoro engine (TTS).
	modelDir := resolve(conf.Model.Dir)
	eng, err := engine.NewTTSEngine(
		filepath.Join(modelDir, conf.Model.ModelFile),
		filepath.Join(modelDir, conf.Model.VoicesFile),
		filepath.Join(modelDir, conf.Model.TokensFile),
		filepath.Join(modelDir, conf.Model.DataDir),
	)
	if err != nil {
		return fmt.Errorf("engine init: %w", err)
	}

	// 3. whisper-server subprocess.
	mgr := stt.NewManager(stt.ManagerConfig{
		ServerPath:       conf.Freeman.STT.ServerPath,
		Host:             "127.0.0.1",
		Port:             conf.Freeman.STT.ServerPort,
		ModelPath:        resolve(conf.Freeman.STT.ModelPath),
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
	vadFrames := cap.Subscribe()
	defer cap.Unsubscribe(vadFrames)
	uttCh := v.Run(ctx, vadFrames)

	// 6. STT Transcriber.
	client := stt.NewClient(mgr.BaseURL(), 10*time.Second)
	tr := stt.NewTranscriber(client, uttCh, 16000)
	tr.Run(ctx)
	defer tr.Stop()

	v.Mute()
	tr.Mute()

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

	// 8. Wakeword detector.
	wkFrames := cap.Subscribe()
	defer cap.Unsubscribe(wkFrames)
	wk, err := wakeword.NewDetector(wakeword.Config{
		ModelsDir:    conf.Persona.Wakeword.ModelsDir,
		MelModelFile: conf.Persona.Wakeword.Melspectrogram,
		EmbModelFile: conf.Persona.Wakeword.Embedding,
		Keywords: [3]wakeword.KeywordConfig{
			{ModelPath: conf.Persona.Wakeword.Keywords.Wake.Model, Threshold: conf.Persona.Wakeword.Keywords.Wake.Threshold},
			{ModelPath: conf.Persona.Wakeword.Keywords.Mute.Model, Threshold: conf.Persona.Wakeword.Keywords.Mute.Threshold},
			{ModelPath: conf.Persona.Wakeword.Keywords.Stop.Model, Threshold: conf.Persona.Wakeword.Keywords.Stop.Threshold},
		},
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("wakeword detector: %w", err)
	}
	defer wk.Stop()
	wk.Run(wkFrames)

	fmt.Fprintf(os.Stderr, "%s listening... say %q to begin\n", conf.Persona.Name, conf.Persona.Name)

	// 9. Agent backend.
	chatAgent := picoding.NewChatAgent(repoRoot)
	taskFactory := picoding.NewTaskAgentFactory(repoRoot, conf.Freeman.Worker.DefaultModel, conf.Freeman.Worker.OpusModel)

	// 10. TaskManager (no task running yet).
	taskMgr := conv.NewTaskManager(taskFactory, logger)
	defer taskMgr.Close()

	// 11. Conv session.
	convSession, err := conv.NewSession(ctx, conv.Deps{
		Transcriber:    tr,
		Speaker:        sp,
		WakewordEvents: wk.Events(),
		SpeechOnsets:   v.SpeechOnsets(),
		TaskManager:    taskMgr,
		ChatAgent:      chatAgent,
		ModelResolver:  taskFactory.ResolveModel,
		Persona:        conf.Persona,
		RepoRoot:       repoRoot,
		Model:          conf.Freeman.PM.Model,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("conv session: %w", err)
	}
	defer convSession.Close()

	return convSession.Run(ctx)
}

// newlineWriter wraps an io.Writer and prepends "\r\n" before each write.
// C libraries (sherpa-onnx, espeak-ng, malgo) occasionally write to fd 2
// without a trailing newline, leaving the terminal cursor mid-line. The
// carriage return resets the cursor to column 0 so slog lines aren't offset.
type newlineWriter struct{ w io.Writer }

func (n newlineWriter) Write(p []byte) (int, error) {
	if _, err := n.w.Write([]byte("\r\n")); err != nil {
		return 0, err
	}
	return n.w.Write(p)
}

// findRepoRoot locates the project root by walking up from the binary's
// directory (so it works regardless of cwd), then falls back to cwd.
func findRepoRoot() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		if root, err := walkUpForGoMod(filepath.Dir(exe)); err == nil {
			return root, nil
		}
	}
	if root, err := walkUpForGoMod("."); err == nil {
		return root, nil
	}
	return "", fmt.Errorf("go.mod not found (checked binary dir and cwd)")
}

func walkUpForGoMod(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
