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
	libName := "libonnxruntime.dylib"
	switch runtime.GOOS {
	case "linux":
		libName = "libonnxruntime.so"
	case "windows":
		libName = "onnxruntime.dll"
	}
	// 1. Next to the binary (after symlink resolution). This handles
	// both a dev checkout (binary at <repo>/freeman, lib at <repo>/lib)
	// and an installed layout (binary at ~/.freeman/freeman, lib at
	// ~/.freeman/lib either real or symlinked from the source repo).
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		c := filepath.Join(filepath.Dir(exe), "lib", libName)
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	// 2. Traditional cwd-relative + system paths, for backwards compat
	// and for users who put the dylib in a Homebrew/system location.
	var fallbacks []string
	switch runtime.GOOS {
	case "darwin":
		fallbacks = []string{"./lib/libonnxruntime.dylib", "/opt/homebrew/lib/libonnxruntime.dylib", "/usr/local/lib/libonnxruntime.dylib"}
	case "linux":
		fallbacks = []string{"./lib/libonnxruntime.so", "/usr/lib/libonnxruntime.so", "/usr/local/lib/libonnxruntime.so"}
	case "windows":
		fallbacks = []string{"./lib/onnxruntime.dll"}
	}
	for _, c := range fallbacks {
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
	// lookback holds the last melLookbackSamples of audio so the next
	// mel inference sees proper STFT context across the chunk boundary.
	lookback   []float32
	melBuffer  *ringFloat32
	embBuffer  *ringFloat32
	melCount   int
	lastEmbMel int

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
	chunkSize = 1280
	// melLookbackSamples is the pre-chunk context OpenWakeWord prepends
	// before running the mel model, so STFT windows at chunk boundaries
	// see the tail of the previous chunk. Without this we get 5 mel
	// frames per 80ms chunk instead of 8 and the keyword classifier
	// sees a ~2s history instead of the ~1.3s it was trained on.
	melLookbackSamples = 480
	melInputSamples    = chunkSize + melLookbackSamples // 1760
	melBins            = 32
	melFramesPerChunk  = 8
	melWindow          = 76
	melStep            = 8
	embSize            = 96
	kwEmbCount         = 16
	maxMelFrames       = 970
	maxEmbs            = 120

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

// append adds vals to the ring and returns the number of float32s dropped
// from the front when the capacity is exceeded. Callers that hold
// positional indices into the ring (e.g. lastEmbMel) must subtract the
// returned count from their indices, otherwise they drift off the end
// of the buffer once streaming exceeds the cap (~9.7s of audio at
// 8 frames × 32 bins per 80ms chunk for the mel buffer).
func (r *ringFloat32) append(vals []float32) int {
	r.data = append(r.data, vals...)
	r.count += len(vals)
	if len(r.data) > r.cap_ {
		dropped := len(r.data) - r.cap_
		r.data = r.data[dropped:]
		return dropped
	}
	return 0
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

	// Single-thread ORT sessions: these models are tiny, parallelism
	// overhead outweighs any gain, and leaving ORT on its default
	// (intra_op = core count) means wakeword and Kokoro TTS fight for
	// the same cores during synth, surfacing as choppy playback. One
	// SessionOptions is shared by all five sessions so wakeword gets a
	// bounded CPU budget separate from sherpa-onnx's pool.
	sessOpts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("session options: %w", err)
	}
	defer sessOpts.Destroy()
	if err := sessOpts.SetIntraOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("intra-op threads: %w", err)
	}
	if err := sessOpts.SetInterOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("inter-op threads: %w", err)
	}

	melIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, melInputSamples))
	if err != nil {
		return nil, fmt.Errorf("mel input tensor: %w", err)
	}
	melOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, melFramesPerChunk, melBins))
	if err != nil {
		return nil, fmt.Errorf("mel output tensor: %w", err)
	}
	melSess, err := ort.NewAdvancedSession(melPath,
		[]string{"input"}, []string{"output"},
		[]ort.ArbitraryTensor{melIn}, []ort.ArbitraryTensor{melOut}, sessOpts)
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
		[]ort.ArbitraryTensor{embIn}, []ort.ArbitraryTensor{embOut}, sessOpts)
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

	// Prime the mel buffer with 76 frames of 1.0, matching OpenWakeWord's
	// np.ones((76, 32)) initialisation. Without this, embeddings cannot
	// be computed until ~1.2s of audio has been processed, so short
	// utterances (wake words are 0.5-1s) never trigger at all.
	primer := make([]float32, melWindow*melBins)
	for i := range primer {
		primer[i] = 1.0
	}
	d.melBuffer.append(primer)

	// Prime the embedding buffer by pushing ~4s of random noise through
	// the mel+embedding pipeline. Python's reference does the same at
	// __init__ to ensure the keyword classifier always has 16 embeddings
	// available on the very first call — otherwise the first second of
	// real audio produces zero classifier runs and short wake words are
	// missed. Using a stable pseudo-random sequence keeps this
	// deterministic for tests.
	primeEmbBuffer(d)

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
			[]ort.ArbitraryTensor{kwIn}, []ort.ArbitraryTensor{kwOut}, sessOpts)
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

// primeEmbBuffer runs 4 s of pseudo-random int16 audio through the
// mel+embedding pipeline at init, populating embBuffer with ~120 seed
// embeddings so the keyword classifier can fire from the very first
// chunk of real audio. Deterministic seed so tests are reproducible.
func primeEmbBuffer(d *Detector) {
	const primerSeconds = 4
	const totalSamples = 16000 * primerSeconds
	// Cheap LCG so we don't pull in math/rand and so the sequence is
	// stable across runs.
	var state uint32 = 12345
	next := func() float32 {
		state = state*1664525 + 1013904223
		// Scale to ~[-1000, 1000] as ints, then to float32 (no /32768
		// scale, matching int16ToFloat32's semantics).
		return float32(int32(state>>16)%2001 - 1000)
	}
	for pushed := 0; pushed < totalSamples; pushed += chunkSize {
		chunk := make([]float32, chunkSize)
		for i := range chunk {
			chunk[i] = next()
		}
		d.updateFeatures(chunk)
	}
	// After priming, reset the mel buffer offset so the next real audio
	// chunk starts computing embeddings fresh against the primed tail of
	// the mel buffer. melCount is also reset so heartbeats elsewhere
	// don't reflect warm-up work.
	d.melCount = 0
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

// updateFeatures runs the mel and embedding models for one 1280-sample
// chunk and appends the resulting embeddings to embBuffer. Keyword
// classification is handled separately in processChunk so that priming
// (which shovels random audio through this path at startup) doesn't
// emit events or logs.
func (d *Detector) updateFeatures(chunk []float32) {
	// Build a 1760-sample window: last 480 samples of previous audio
	// (zeros on the very first chunk) followed by the new 1280 samples.
	// This matches openwakeword/utils.py:_streaming_melspectrogram which
	// feeds the mel model raw_data_buffer[-n_samples-160*3:].
	melIn := d.melInputTensor.GetData()
	if len(d.lookback) == melLookbackSamples {
		copy(melIn, d.lookback)
	} else {
		// First chunk: pad leading samples with zeros.
		for i := 0; i < melLookbackSamples; i++ {
			melIn[i] = 0
		}
	}
	copy(melIn[melLookbackSamples:], chunk)

	// Stash the tail of this chunk for the next call's lookback.
	if cap(d.lookback) < melLookbackSamples {
		d.lookback = make([]float32, melLookbackSamples)
	}
	d.lookback = d.lookback[:melLookbackSamples]
	copy(d.lookback, chunk[chunkSize-melLookbackSamples:])

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
	// If the ring had to drop old frames, shift lastEmbMel by the same
	// number of frames so it still points at the correct position in the
	// remaining buffer. Without this, once the ring wraps, the embedding
	// loop never fires again and the keyword classifier keeps scoring
	// the last stale 16 embeddings — exactly the bug that made live
	// detection silently die after ~10s of audio.
	dropped := d.melBuffer.append(normalized)
	if dropped > 0 {
		d.lastEmbMel -= dropped / melBins
		if d.lastEmbMel < 0 {
			d.lastEmbMel = 0
		}
	}
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
}

func (d *Detector) processChunk(chunk []float32) {
	d.totalChunks++
	d.updateFeatures(chunk)

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
			if score >= 0.1 && score < d.thresholds[i] {
				d.log.Info("keyword near-miss", "keyword", kind.String(), "score", score, "threshold", d.thresholds[i])
			}
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
