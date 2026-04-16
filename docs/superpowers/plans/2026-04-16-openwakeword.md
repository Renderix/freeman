# OpenWakeWord Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Porcupine wake word detection with OpenWakeWord, running three ONNX models in-process in Go with no Python runtime dependency and no API key.

**Architecture:** Three ONNX models (melspectrogram → embedding → keyword classification) run sequentially in Go via `onnxruntime_go`. The `Detector` public interface stays identical — only internals and config change. If ONNX Runtime conflicts with sherpa-onnx, fall back to a subprocess approach.

**Tech Stack:** Go, `github.com/yalue/onnxruntime_go`, OpenWakeWord ONNX models

**Spec:** `docs/superpowers/specs/2026-04-16-openwakeword-design.md`

---

### Task 1: Update Config for OpenWakeWord

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`

- [ ] **Step 1: Replace Porcupine config structs with OpenWakeWord config structs**

In `internal/config/config.go`, replace the Porcupine-specific types (lines 80-100):

```go
// Delete these:
type KeywordPathsConfig struct { ... }
type SensitivitiesConfig struct { ... }
type PersonaConfig struct { ... }

// Replace with:
type WakewordKeywordConfig struct {
	Model     string  `yaml:"model"`
	Threshold float32 `yaml:"threshold"`
}

type WakewordKeywordsConfig struct {
	Wake WakewordKeywordConfig `yaml:"wake"`
	Mute WakewordKeywordConfig `yaml:"mute"`
	Stop WakewordKeywordConfig `yaml:"stop"`
}

type WakewordConfig struct {
	ModelsDir      string                 `yaml:"models_dir"`
	Melspectrogram string                 `yaml:"melspectrogram"`
	Embedding      string                 `yaml:"embedding"`
	Keywords       WakewordKeywordsConfig `yaml:"keywords"`
}

type PersonaConfig struct {
	Name     string         `yaml:"name"`
	Greeting string         `yaml:"greeting"`
	Traits   []string       `yaml:"traits"`
	Rules    []string       `yaml:"rules"`
	Wakeword WakewordConfig `yaml:"wakeword"`
}
```

- [ ] **Step 2: Update config.yaml**

Replace the persona block:

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

- [ ] **Step 3: Fix config tests if any reference old fields**

Run: `go test ./internal/config/ -v`

If tests reference `AccessKeyEnv`, `KeywordPaths`, or `Sensitivities`, remove those assertions.

- [ ] **Step 4: Verify config package builds**

Run: `go build ./internal/config/`
Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go config.yaml
git commit -m "feat(config): replace Porcupine config with OpenWakeWord config"
```

---

### Task 2: Add onnxruntime_go Dependency, Remove Porcupine

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add onnxruntime_go**

```bash
go get github.com/yalue/onnxruntime_go
```

- [ ] **Step 2: Remove Porcupine**

```bash
go get -d github.com/Picovoice/porcupine/binding/go/v3@none
```

If the above doesn't work (module may not support `@none`), just remove the require line from go.mod manually and run:

```bash
go mod tidy
```

- [ ] **Step 3: Verify no Porcupine imports remain**

```bash
grep -r "Picovoice/porcupine" --include="*.go" .
```

Expected: Only `internal/audio/wakeword/wakeword.go` (which we'll fix in Task 3). If the wakeword package still imports porcupine, that's expected — Task 3 replaces it.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): add onnxruntime_go, remove porcupine"
```

---

### Task 3: Rewrite Wakeword Detector with OpenWakeWord

**Files:**
- Rewrite: `internal/audio/wakeword/wakeword.go`
- Modify: `internal/audio/wakeword/wakeword_test.go`

This is the core task. The `KeywordKind` type and public interface (`NewDetector`, `Run`, `Events`, `Stop`) stay the same. Everything else changes.

- [ ] **Step 1: Write tests for audio conversion and buffer helpers**

Add to `internal/audio/wakeword/wakeword_test.go`:

```go
func TestInt16ToFloat32(t *testing.T) {
	input := []int16{0, 16384, -16384, 32767, -32768}
	got := int16ToFloat32(input)
	if len(got) != 5 {
		t.Fatalf("expected 5, got %d", len(got))
	}
	if got[0] != 0.0 {
		t.Errorf("got[0] = %f, want 0.0", got[0])
	}
	if got[3] < 0.99 || got[3] > 1.01 {
		t.Errorf("got[3] = %f, want ~1.0", got[3])
	}
	if got[4] < -1.01 || got[4] > -0.99 {
		t.Errorf("got[4] = %f, want ~-1.0", got[4])
	}
}

func TestRingBufferAppendAndSlice(t *testing.T) {
	rb := newRingFloat32(4)
	rb.append([]float32{1, 2})
	rb.append([]float32{3, 4})
	got := rb.lastN(4)
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}
	if got[0] != 1 || got[3] != 4 {
		t.Errorf("got %v, want [1 2 3 4]", got)
	}

	rb.append([]float32{5, 6})
	got = rb.lastN(4)
	if got[0] != 3 || got[3] != 6 {
		t.Errorf("after overflow got %v, want [3 4 5 6]", got)
	}
}

func TestRingBufferLastNPartial(t *testing.T) {
	rb := newRingFloat32(10)
	rb.append([]float32{1, 2, 3})
	got := rb.lastN(10)
	if len(got) != 3 {
		t.Fatalf("expected 3 (partial), got %d", len(got))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/audio/wakeword/ -run "TestInt16|TestRingBuffer" -v`
Expected: FAIL — functions don't exist yet.

- [ ] **Step 3: Rewrite wakeword.go**

Replace the entire contents of `internal/audio/wakeword/wakeword.go`:

```go
package wakeword

import (
	"fmt"
	"log/slog"
	"path/filepath"

	ort "github.com/yalue/onnxruntime_go"
)

type KeywordKind int

const (
	KeywordWake KeywordKind = iota
	KeywordMute
	KeywordStop
)

func (k KeywordKind) String() string {
	switch k {
	case KeywordWake:
		return "wake"
	case KeywordMute:
		return "mute"
	case KeywordStop:
		return "stop"
	default:
		return "unknown"
	}
}

type KeywordConfig struct {
	ModelPath string
	Threshold float32
}

type Config struct {
	ModelsDir    string
	MelModelFile string
	EmbModelFile string
	Keywords     [3]KeywordConfig
	Logger       *slog.Logger
}

type Detector struct {
	melSession *ort.AdvancedSession
	embSession *ort.AdvancedSession
	kwSessions [3]*ort.AdvancedSession
	thresholds [3]float32

	melInputTensor  *ort.Tensor[float32]
	melOutputTensor *ort.Tensor[float32]
	embInputTensor  *ort.Tensor[float32]
	embOutputTensor *ort.Tensor[float32]
	kwInputTensors  [3]*ort.Tensor[float32]
	kwOutputTensors [3]*ort.Tensor[float32]

	audioBuffer []float32
	melBuffer   *ringFloat32
	embBuffer   *ringFloat32
	melCount    int
	lastEmbMel  int

	events chan KeywordKind
	stopCh chan struct{}
	log    *slog.Logger
}

const (
	chunkSize    = 1280
	melBins      = 32
	melWindow    = 76
	melStep      = 8
	embSize      = 96
	kwEmbCount   = 16
	maxMelFrames = 970
	maxEmbs      = 120
)

func int16ToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 32768.0
	}
	return out
}

type ringFloat32 struct {
	data  []float32
	cap_  int
	count int
}

func newRingFloat32(capacity int) *ringFloat32 {
	return &ringFloat32{
		data: make([]float32, 0, capacity),
		cap_: capacity,
	}
}

func (r *ringFloat32) append(vals []float32) {
	r.data = append(r.data, vals...)
	r.count += len(vals)
	if len(r.data) > r.cap_ {
		r.data = r.data[len(r.data)-r.cap_:]
	}
}

func (r *ringFloat32) lastN(n int) []float32 {
	if len(r.data) < n {
		out := make([]float32, len(r.data))
		copy(out, r.data)
		return out
	}
	out := make([]float32, n)
	copy(out, r.data[len(r.data)-n:])
	return out
}

func (r *ringFloat32) len() int {
	return len(r.data)
}

func NewDetector(cfg Config) (*Detector, error) {
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("onnx runtime init: %w", err)
	}

	melPath := filepath.Join(cfg.ModelsDir, cfg.MelModelFile)
	embPath := filepath.Join(cfg.ModelsDir, cfg.EmbModelFile)

	melIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, chunkSize))
	if err != nil {
		return nil, fmt.Errorf("mel input tensor: %w", err)
	}
	melOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, melBins))
	if err != nil {
		return nil, fmt.Errorf("mel output tensor: %w", err)
	}
	melSess, err := ort.NewAdvancedSession(melPath,
		[]string{"input"}, []string{"output"},
		[]ort.ArbitraryTensor{melIn}, []ort.ArbitraryTensor{melOut}, nil)
	if err != nil {
		return nil, fmt.Errorf("mel session: %w", err)
	}

	embIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, melWindow, melBins, 1))
	if err != nil {
		return nil, fmt.Errorf("emb input tensor: %w", err)
	}
	embOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, embSize))
	if err != nil {
		return nil, fmt.Errorf("emb output tensor: %w", err)
	}
	embSess, err := ort.NewAdvancedSession(embPath,
		[]string{"input"}, []string{"output"},
		[]ort.ArbitraryTensor{embIn}, []ort.ArbitraryTensor{embOut}, nil)
	if err != nil {
		return nil, fmt.Errorf("emb session: %w", err)
	}

	d := &Detector{
		melSession:      melSess,
		embSession:      embSess,
		melInputTensor:  melIn,
		melOutputTensor: melOut,
		embInputTensor:  embIn,
		embOutputTensor: embOut,
		melBuffer:       newRingFloat32(maxMelFrames * melBins),
		embBuffer:       newRingFloat32(maxEmbs * embSize),
		events:          make(chan KeywordKind, 4),
		stopCh:          make(chan struct{}),
		log:             cfg.Logger,
	}

	for i, kw := range cfg.Keywords {
		kwPath := filepath.Join(cfg.ModelsDir, kw.ModelPath)
		kwIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, kwEmbCount*embSize))
		if err != nil {
			return nil, fmt.Errorf("kw[%d] input tensor: %w", i, err)
		}
		kwOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1))
		if err != nil {
			return nil, fmt.Errorf("kw[%d] output tensor: %w", i, err)
		}
		kwSess, err := ort.NewAdvancedSession(kwPath,
			[]string{"input"}, []string{"output"},
			[]ort.ArbitraryTensor{kwIn}, []ort.ArbitraryTensor{kwOut}, nil)
		if err != nil {
			return nil, fmt.Errorf("kw[%d] session (%s): %w", i, kwPath, err)
		}
		d.kwSessions[i] = kwSess
		d.kwInputTensors[i] = kwIn
		d.kwOutputTensors[i] = kwOut
		d.thresholds[i] = kw.Threshold
	}

	return d, nil
}

func (d *Detector) Events() <-chan KeywordKind {
	return d.events
}

func (d *Detector) Run(frames <-chan []int16) {
	go d.readLoop(frames)
}

func (d *Detector) readLoop(frames <-chan []int16) {
	for {
		select {
		case <-d.stopCh:
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			d.audioBuffer = append(d.audioBuffer, int16ToFloat32(frame)...)
			for len(d.audioBuffer) >= chunkSize {
				d.processChunk(d.audioBuffer[:chunkSize])
				d.audioBuffer = d.audioBuffer[chunkSize:]
			}
		}
	}
}

func (d *Detector) processChunk(chunk []float32) {
	copy(d.melInputTensor.GetData(), chunk)
	if err := d.melSession.Run(); err != nil {
		d.log.Error("mel inference error", "err", err)
		return
	}
	melOut := d.melOutputTensor.GetData()
	d.melBuffer.append(melOut)
	d.melCount++

	melFrames := d.melBuffer.len() / melBins
	for d.lastEmbMel+melWindow <= melFrames {
		startIdx := d.lastEmbMel * melBins
		window := d.melBuffer.data[startIdx : startIdx+melWindow*melBins]
		copy(d.embInputTensor.GetData(), window)
		if err := d.embSession.Run(); err != nil {
			d.log.Error("emb inference error", "err", err)
			d.lastEmbMel += melStep
			continue
		}
		embOut := d.embOutputTensor.GetData()
		d.embBuffer.append(embOut)
		d.lastEmbMel += melStep
	}

	if d.embBuffer.len()/embSize >= kwEmbCount {
		embData := d.embBuffer.lastN(kwEmbCount * embSize)
		for i := 0; i < 3; i++ {
			copy(d.kwInputTensors[i].GetData(), embData)
			if err := d.kwSessions[i].Run(); err != nil {
				d.log.Error("kw inference error", "kw", KeywordKind(i).String(), "err", err)
				continue
			}
			score := d.kwOutputTensors[i].GetData()[0]
			if score >= d.thresholds[i] {
				kind := KeywordKind(i)
				d.log.Info("keyword detected", "keyword", kind.String(), "score", score)
				select {
				case d.events <- kind:
				default:
				}
			}
		}
	}
}

func (d *Detector) Stop() {
	close(d.stopCh)
	if d.melSession != nil {
		d.melSession.Destroy()
	}
	if d.embSession != nil {
		d.embSession.Destroy()
	}
	for _, s := range d.kwSessions {
		if s != nil {
			s.Destroy()
		}
	}
	d.melInputTensor.Destroy()
	d.melOutputTensor.Destroy()
	d.embInputTensor.Destroy()
	d.embOutputTensor.Destroy()
	for i := range d.kwInputTensors {
		if d.kwInputTensors[i] != nil {
			d.kwInputTensors[i].Destroy()
		}
		if d.kwOutputTensors[i] != nil {
			d.kwOutputTensors[i].Destroy()
		}
	}
	ort.DestroyEnvironment()
}
```

- [ ] **Step 4: Run helper tests**

Run: `go test ./internal/audio/wakeword/ -run "TestInt16|TestRingBuffer|TestKeyword" -v`
Expected: PASS for `TestInt16ToFloat32`, `TestRingBufferAppendAndSlice`, `TestRingBufferLastNPartial`, `TestKeywordKindFromIndex`, `TestKeywordKindString`.

- [ ] **Step 5: Verify build**

Run: `go build ./internal/audio/wakeword/`
Expected: Build succeeds. If ONNX Runtime conflicts with sherpa-onnx, this is where it will surface — see fallback notes at bottom of plan.

- [ ] **Step 6: Commit**

```bash
git add internal/audio/wakeword/
git commit -m "feat(wakeword): rewrite detector with OpenWakeWord ONNX pipeline"
```

---

### Task 4: Update Call Command Wiring

**Files:**
- Modify: `cmd/freeman/call.go`

- [ ] **Step 1: Replace wakeword detector construction**

In `cmd/freeman/call.go`, find the wakeword block (lines 147-174) and replace:

```go
// Remove:
accessKey := os.Getenv(conf.Persona.AccessKeyEnv)
if accessKey == "" {
	return fmt.Errorf("environment variable %s not set (Picovoice access key)", conf.Persona.AccessKeyEnv)
}
wkFrames := cap.Subscribe()
defer cap.Unsubscribe(wkFrames)
wk, err := wakeword.NewDetector(wakeword.Config{
	AccessKey: accessKey,
	KeywordPaths: []string{
		conf.Persona.KeywordPaths.Wake,
		conf.Persona.KeywordPaths.Mute,
		conf.Persona.KeywordPaths.Stop,
	},
	Sensitivities: []float32{
		conf.Persona.Sensitivities.Wake,
		conf.Persona.Sensitivities.Mute,
		conf.Persona.Sensitivities.Stop,
	},
	Logger: logger,
})

// Replace with:
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
```

Also remove the `"os"` import if it was only used for `os.Getenv` of the access key (check if `os` is used elsewhere in the file first).

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/freeman/`
Expected: Build succeeds.

- [ ] **Step 3: Run all tests**

Run: `go test ./...`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/freeman/call.go
git commit -m "feat(cmd): wire OpenWakeWord detector, remove Picovoice access key"
```

---

### Task 5: Update Setup Scripts

**Files:**
- Rewrite: `scripts/setup_wakeword_models.sh`
- Create: `scripts/train_wakeword.sh`

- [ ] **Step 1: Rewrite setup_wakeword_models.sh**

This script downloads the shared ONNX models (melspectrogram + embedding) from OpenWakeWord releases:

```bash
#!/usr/bin/env bash
set -euo pipefail

MODELS_DIR="$(cd "$(dirname "$0")/.." && pwd)/models/wakeword"
mkdir -p "$MODELS_DIR"

OWW_VERSION="v0.6.0"
BASE_URL="https://github.com/dscripka/openWakeWord/raw/refs/heads/main/openwakeword/resources/models"

echo "=== OpenWakeWord Shared Model Setup ==="

for f in melspectrogram.onnx embedding_model.onnx; do
    if [ -f "$MODELS_DIR/$f" ]; then
        echo "  OK: $f (already exists)"
    else
        echo "  Downloading: $f ..."
        curl -fSL "$BASE_URL/$f" -o "$MODELS_DIR/$f"
        echo "  OK: $f"
    fi
done

echo ""
echo "Shared models ready in $MODELS_DIR/"
echo ""
echo "Next: train custom keyword models with ./scripts/train_wakeword.sh"
echo "  or place pre-trained .onnx files (horus.onnx, mute.onnx, horus_stop.onnx) in $MODELS_DIR/"
```

- [ ] **Step 2: Create train_wakeword.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODELS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)/models/wakeword"
VENV_DIR="$SCRIPT_DIR/.wakeword-venv"

echo "=== OpenWakeWord Keyword Training ==="
echo ""
echo "This will train custom wake word models for: Horus, Mute, Horus stop"
echo "Training takes ~30-60 minutes per keyword on CPU."
echo ""

if [ ! -d "$VENV_DIR" ]; then
    echo "Creating Python virtual environment..."
    python3 -m venv "$VENV_DIR"
fi

source "$VENV_DIR/bin/activate"
pip install -q openwakeword

echo ""
echo "Training keywords..."

python3 -c "
import openwakeword
from openwakeword.train import train_custom_model
import os

models_dir = '$MODELS_DIR'
keywords = [
    ('horus', 'horus'),
    ('mute', 'mute'),
    ('horus_stop', 'horus stop'),
]

for filename, phrase in keywords:
    out_path = os.path.join(models_dir, filename + '.onnx')
    if os.path.exists(out_path):
        print(f'  SKIP: {filename}.onnx (already exists)')
        continue
    print(f'  Training: \"{phrase}\" -> {filename}.onnx ...')
    train_custom_model(phrase, output_path=out_path)
    print(f'  OK: {filename}.onnx')
"

echo ""
echo "Training complete. Models in $MODELS_DIR/"
echo "Ready to run: ./freeman call"
```

Note: The exact `train_custom_model` API may differ — the implementer should check OpenWakeWord's current training docs and adjust the Python call. The Colab notebook or `openwakeword-trainer` repo has the canonical training flow.

- [ ] **Step 3: Make executable**

```bash
chmod +x scripts/setup_wakeword_models.sh scripts/train_wakeword.sh
```

- [ ] **Step 4: Commit**

```bash
git add scripts/setup_wakeword_models.sh scripts/train_wakeword.sh
git commit -m "feat(scripts): rewrite setup for OpenWakeWord, add training script"
```

---

### Task 6: Clean Up Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Remove Porcupine from go.mod if still present**

```bash
go mod tidy
```

- [ ] **Step 2: Verify no Porcupine references remain anywhere**

```bash
grep -r "Picovoice\|porcupine" --include="*.go" .
grep -r "Picovoice\|porcupine" go.mod
```

Expected: No matches.

- [ ] **Step 3: Full build and test**

```bash
go build ./... && go test ./...
```

Expected: Build succeeds, all tests pass.

- [ ] **Step 4: Commit if go.mod changed**

```bash
git add go.mod go.sum
git commit -m "chore(deps): remove porcupine, tidy go.mod"
```

---

### Task 7: Update Docs

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update CLAUDE.md**

In Build & Run section, update the wakeword setup line:

```bash
./scripts/setup_wakeword_models.sh  # OpenWakeWord shared ONNX models → ./models/wakeword/
./scripts/train_wakeword.sh         # Train custom keyword models (one-time, ~30-60 min)
```

In Key Dependencies section, replace Porcupine line:

```
- `github.com/yalue/onnxruntime_go` — ONNX Runtime for wake word detection (OpenWakeWord)
```

Remove any remaining references to Porcupine or Picovoice access keys.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for OpenWakeWord"
```

---

## ONNX Runtime Conflict Fallback

If Task 3 Step 5 fails because `onnxruntime_go` and `sherpa-onnx-go` both try to load `libonnxruntime`:

**Option A: Use sherpa-onnx's ONNX Runtime.** The sherpa-onnx CGO binding already loads libonnxruntime. Use its C API to run custom ONNX sessions directly. This avoids loading two copies of the library.

**Option B: Subprocess approach.** Build the wakeword detector as a standalone Go binary (`cmd/wakeword-server/main.go`) that:
- Reads int16 PCM frames from stdin
- Runs the three ONNX models
- Writes JSONL keyword events to stdout (`{"keyword":"wake"}`)
- The `Detector` in the main process spawns this subprocess and routes capture frames to it via a pipe

The `Detector` public interface stays identical in both cases — only internals change.
