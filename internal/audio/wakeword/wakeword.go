package wakeword

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	ort "github.com/yalue/onnxruntime_go"
)

func resolveOnnxLib() (string, error) {
	if p := os.Getenv("ONNXRUNTIME_LIB_PATH"); p != "" {
		return p, nil
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"./lib/libonnxruntime.dylib", "/opt/homebrew/lib/libonnxruntime.dylib", "/usr/local/lib/libonnxruntime.dylib"}
	case "linux":
		candidates = []string{"./lib/libonnxruntime.so", "/usr/lib/libonnxruntime.so", "/usr/local/lib/libonnxruntime.so"}
	case "windows":
		candidates = []string{"./lib/onnxruntime.dll"}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}
	return "", fmt.Errorf("onnxruntime shared library not found; set ONNXRUNTIME_LIB_PATH or run ./scripts/setup_wakeword_models.sh")
}

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

	// Monotonic chunk counter + per-keyword refractory gate. A single
	// utterance produces high scores across every chunk the utterance's
	// embeddings remain in the 16-wide rolling window (≈1s), so without
	// a cooldown one "Horus" fires dozens of events.
	totalChunks   int
	cooldownUntil [3]int

	events chan KeywordKind
	stopCh chan struct{}
	log    *slog.Logger
}

const (
	chunkSize         = 1280
	melBins           = 32
	melFramesPerChunk = 5
	melWindow         = 76
	melStep           = 8
	embSize           = 96
	kwEmbCount        = 16
	maxMelFrames      = 970
	maxEmbs           = 120

	// Per-keyword refractory window after a detection. 25 chunks × 80ms =
	// 2s, long enough to ride out the utterance's echo through the 16-wide
	// embedding window and avoid repeat fires, short enough that the user
	// can re-trigger quickly.
	cooldownChunks = 25
)

// int16ToFloat32 casts PCM samples to float32 without scaling. The mel
// model in OpenWakeWord was trained against raw int16 magnitudes (just
// a dtype change, not [-1,1] normalisation), so scaling here shifts the
// mel output by ~90 dB and silently kills keyword accuracy.
func int16ToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
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
	libPath, err := resolveOnnxLib()
	if err != nil {
		return nil, err
	}
	ort.SetSharedLibraryPath(libPath)
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("onnx runtime init (%s): %w", libPath, err)
	}

	melPath := filepath.Join(cfg.ModelsDir, cfg.MelModelFile)
	embPath := filepath.Join(cfg.ModelsDir, cfg.EmbModelFile)

	melIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, chunkSize))
	if err != nil {
		return nil, fmt.Errorf("mel input tensor: %w", err)
	}
	melOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, melFramesPerChunk, melBins))
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
	embOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, 1, embSize))
	if err != nil {
		return nil, fmt.Errorf("emb output tensor: %w", err)
	}
	embSess, err := ort.NewAdvancedSession(embPath,
		[]string{"input_1"}, []string{"conv2d_19"},
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
		kwIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, kwEmbCount, embSize))
		if err != nil {
			return nil, fmt.Errorf("kw[%d] input tensor: %w", i, err)
		}
		kwOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1))
		if err != nil {
			return nil, fmt.Errorf("kw[%d] output tensor: %w", i, err)
		}
		kwSess, err := ort.NewAdvancedSession(kwPath,
			[]string{"onnx::Flatten_0"}, []string{"39"},
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
	// Apply OpenWakeWord's reference mel transform (mel/10 + 2) to bring
	// the ONNX melspectrogram output into the range the embedding model
	// was trained against. See openwakeword/utils.py:_get_melspectrogram.
	melOut := d.melOutputTensor.GetData()
	normalized := make([]float32, len(melOut))
	for i, v := range melOut {
		normalized[i] = v/10.0 + 2.0
	}
	d.melBuffer.append(normalized)
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
			kind := KeywordKind(i)
			if score >= d.thresholds[i] {
				if d.totalChunks < d.cooldownUntil[i] {
					continue
				}
				d.log.Info("keyword detected", "keyword", kind.String(), "score", score)
				d.cooldownUntil[i] = d.totalChunks + cooldownChunks
				select {
				case d.events <- kind:
				default:
				}
			}
		}
	}

	d.totalChunks++
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
