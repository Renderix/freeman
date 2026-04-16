# OpenWakeWord Integration — Replace Porcupine

## Overview

Replace Porcupine wake word detection with OpenWakeWord, running three ONNX models in-process in Go. Eliminates the Picovoice API key requirement and commercial license. Custom keywords trained locally via openwakeword-trainer. No Python at runtime.

## ONNX Inference Pipeline

Three models run in sequence per 80ms audio chunk:

### 1. Melspectrogram Model (`melspectrogram.onnx`)
- **Input:** `[1, 1280]` float32 — 80ms of 16kHz audio (1280 samples)
- **Output:** mel frames `[N, 32]` — N mel-frequency bins per time step
- **Source:** Pre-trained, downloaded from OpenWakeWord releases (~2MB)

### 2. Embedding Model (`embedding_model.onnx`)
- **Input:** `[1, 76, 32, 1]` float32 — sliding window of 76 consecutive mel frames
- **Output:** `[1, 96]` float32 — speech embedding vector
- **Source:** Google speech embedding model, downloaded from OpenWakeWord releases (~3MB)
- **Sliding window:** Step size 8 frames. New embeddings computed when 8+ new mel frames accumulate.

### 3. Keyword Models (`horus.onnx`, `mute.onnx`, `horus_stop.onnx`)
- **Input:** `[1, 16]` float32 — 16 most recent embeddings (flattened from `[16, 96]` or shaped per model)
- **Output:** `[1, 1]` float32 — confidence score 0.0–1.0
- **Source:** Trained locally via openwakeword-trainer (~200KB each)
- **Threshold:** Per-keyword configurable. Score > threshold = detection.

### Data Flow

```
Raw int16 audio (1280 samples = 80ms)
    → convert to float32
    → melspectrogram.onnx → mel frames [N, 32]
    → accumulate in melBuffer (ring, max ~970 frames)
    → when 76+ frames: slide window (step 8)
        → embedding_model.onnx → embedding [1, 96]
        → accumulate in embBuffer (ring, max 16)
        → when 16 embeddings ready:
            → horus.onnx → score → if > 0.5: KeywordWake
            → mute.onnx → score → if > 0.5: KeywordMute
            → horus_stop.onnx → score → if > 0.7: KeywordStop
```

## Changes to `internal/audio/wakeword/`

### Public Interface — Unchanged

```go
func NewDetector(cfg Config) (*Detector, error)
func (d *Detector) Run(frames <-chan []int16)
func (d *Detector) Events() <-chan KeywordKind
func (d *Detector) Stop()
```

### Config Struct — Updated

```go
type KeywordConfig struct {
    ModelPath string
    Threshold float32
}

type Config struct {
    ModelsDir       string
    MelModelFile    string
    EmbModelFile    string
    Keywords        [3]KeywordConfig  // [wake, mute, stop]
    Logger          *slog.Logger
}
```

### Detector Internals — Rewritten

```go
type Detector struct {
    melSession  *ort.Session
    embSession  *ort.Session
    kwSessions  [3]*ort.Session
    thresholds  [3]float32

    audioBuffer []float32     // accumulates raw samples until 1280
    melBuffer   []float32     // rolling mel frames (max ~970 * 32)
    embBuffer   []float32     // rolling embeddings (max 16 * 96)
    melCount    int           // total mel frames accumulated
    embCount    int           // total embeddings accumulated

    events      chan KeywordKind
    stopCh      chan struct{}
    log         *slog.Logger
}
```

### readLoop Changes

The readLoop accumulates int16 frames from the capture channel. When 1280 samples are ready:

1. Convert `[]int16` → `[]float32` (divide by 32768.0)
2. Run melspectrogram model, append output to `melBuffer`
3. For each complete 76-frame window (step 8) since last embedding:
   - Reshape to `[1, 76, 32, 1]`, run embedding model
   - Append 96-float embedding to `embBuffer`
4. When `embBuffer` has 16+ entries:
   - For each keyword model: extract last 16 embeddings, run model, check threshold
   - Emit `KeywordKind` on match

## Config Changes

### PersonaConfig Update (`internal/config/config.go`)

Remove Porcupine-specific fields:

```go
// Remove:
AccessKeyEnv  string              `yaml:"access_key_env"`
KeywordPaths  KeywordPathsConfig  `yaml:"keyword_paths"`
Sensitivities SensitivitiesConfig `yaml:"sensitivities"`
```

Add OpenWakeWord fields:

```go
type WakewordKeywordConfig struct {
    Model     string  `yaml:"model"`
    Threshold float32 `yaml:"threshold"`
}

type WakewordConfig struct {
    ModelsDir      string                 `yaml:"models_dir"`
    Melspectrogram string                 `yaml:"melspectrogram"`
    Embedding      string                 `yaml:"embedding"`
    Keywords       WakewordKeywordsConfig `yaml:"keywords"`
}

type WakewordKeywordsConfig struct {
    Wake WakewordKeywordConfig `yaml:"wake"`
    Mute WakewordKeywordConfig `yaml:"mute"`
    Stop WakewordKeywordConfig `yaml:"stop"`
}
```

Add to `PersonaConfig`:

```go
type PersonaConfig struct {
    Name     string         `yaml:"name"`
    Greeting string         `yaml:"greeting"`
    Traits   []string       `yaml:"traits"`
    Rules    []string       `yaml:"rules"`
    Wakeword WakewordConfig `yaml:"wakeword"`
}
```

### config.yaml

```yaml
persona:
  name: "Horus"
  greeting: "I'm here"
  traits:
    - "concise"
    - "technical"
  rules:
    - "No markdown in responses"
    - "No bullet points"
    - "No code fences"
    - "Keep responses under 3 sentences"
  wakeword:
    models_dir: "./models/wakeword"
    melspectrogram: "melspectrogram.onnx"
    embedding: "embedding_model.onnx"
    keywords:
      wake:
        model: "horus.onnx"
        threshold: 0.5
      mute:
        model: "mute.onnx"
        threshold: 0.5
      stop:
        model: "horus_stop.onnx"
        threshold: 0.7
```

## Call Command Changes (`cmd/freeman/call.go`)

Update wakeword detector construction:

```go
// Remove:
accessKey := os.Getenv(conf.Persona.AccessKeyEnv)
// ... Porcupine config

// Replace with:
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
```

Remove the `PICOVOICE_ACCESS_KEY` env var check entirely.

## Dependency Changes

### Remove
- `github.com/Picovoice/porcupine/binding/go/v3`

### Add
- `github.com/yalue/onnxruntime_go` — Go bindings for ONNX Runtime

### ONNX Runtime Conflict Mitigation

`sherpa-onnx-go` (Kokoro TTS) already links ONNX Runtime. If `onnxruntime_go` conflicts:
- **Fallback:** Wrap wakeword inference in a small Go subprocess that communicates via stdin/stdout (same pattern as Whisper). The `Detector` interface stays the same; only internals change.

## Scripts

### `scripts/setup_wakeword_models.sh` — Download Shared Models

Downloads `melspectrogram.onnx` and `embedding_model.onnx` from OpenWakeWord GitHub releases to `models/wakeword/`. Checks for existing files, skips if present.

### `scripts/train_wakeword.sh` — Train Custom Keywords

1. Creates a Python venv
2. Installs openwakeword-trainer
3. Trains three keywords: "Horus", "Mute", "Horus stop"
4. Copies resulting `.onnx` files to `models/wakeword/`
5. Training takes ~30-60 min per keyword on CPU

Training is a one-time operation. The `.onnx` files can be cached/committed.

## Model Files

```
models/wakeword/
    melspectrogram.onnx      # ~2MB, downloaded (shared)
    embedding_model.onnx     # ~3MB, downloaded (shared)
    horus.onnx               # ~200KB, trained locally
    mute.onnx                # ~200KB, trained locally
    horus_stop.onnx          # ~200KB, trained locally
```

## Key Design Decisions

1. **No Python at runtime:** All inference in Go via ONNX Runtime. Python only needed for one-time keyword training.
2. **Same public interface:** `Detector` API unchanged — swap is transparent to session and call.go.
3. **No API key:** OpenWakeWord is fully open-source. No signup, no business email, no license restrictions.
4. **Subprocess fallback:** If ONNX Runtime conflicts with sherpa-onnx, wakeword runs as a separate process. Interface stays the same.
5. **Per-keyword thresholds:** Each keyword gets its own confidence threshold. "Horus stop" gets 0.7 (higher) to avoid accidental shutdown.
