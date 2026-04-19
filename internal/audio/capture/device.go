// Package capture drives the microphone through malgo and exposes a clean Go
// channel of fixed-size PCM frames for downstream VAD/STT.
package capture

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Renderix/freeman/internal/audio"
	"github.com/gen2brain/malgo"
)

// Config selects the capture device and format.
type Config struct {
	DeviceID   string // empty = default
	SampleRate int    // 16000 for Plan 2
	Channels   int    // 1
	FrameMs    int    // 20
}

// Device runs a malgo capture device and hands fixed-size frames to Frames().
type Device struct {
	cfg       Config
	dev       *malgo.Device
	ring      *Ring
	frames    chan []int16
	subs      map[chan []int16]struct{}
	subsMu    sync.RWMutex
	stopOnce  sync.Once
	stopCh    chan struct{}
	frameSize int // samples per frame
	droppedT  time.Time
	log       func(msg string, kv ...any)
	userMuted atomic.Bool
}

// SetUserMuted toggles a soft mute. While muted, every frame handed to
// downstream subscribers is zero-filled, so wake-word, VAD, and STT all
// go deaf together. The malgo device keeps running so turning mute off
// is instant and doesn't re-prompt the OS for mic access.
func (d *Device) SetUserMuted(muted bool) { d.userMuted.Store(muted) }

// UserMuted reports the current soft-mute state.
func (d *Device) UserMuted() bool { return d.userMuted.Load() }

// Open initializes a capture device. It does not start it — call Start.
func Open(actx *audio.Context, cfg Config) (*Device, error) {
	if actx == nil || actx.Raw() == nil {
		return nil, fmt.Errorf("audio context not initialized")
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Channels == 0 {
		cfg.Channels = 1
	}
	if cfg.FrameMs == 0 {
		cfg.FrameMs = 20
	}
	frameSize := cfg.SampleRate * cfg.FrameMs / 1000

	d := &Device{
		cfg:       cfg,
		ring:      NewRing(frameSize * 200), // ~4 s of mic audio
		frames:    make(chan []int16, 50),
		subs:      make(map[chan []int16]struct{}),
		stopCh:    make(chan struct{}),
		frameSize: frameSize,
		log: func(msg string, kv ...any) {
			actx.Log().Debug(msg, kv...)
		},
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(cfg.Channels)
	deviceConfig.SampleRate = uint32(cfg.SampleRate)
	deviceConfig.Alsa.NoMMap = 1

	channels := cfg.Channels
	ring := d.ring

	// DataProc signature: func(pOutputSamples, pInputSamples []byte, framecount uint32)
	callbacks := malgo.DeviceCallbacks{
		Data: func(_, pInput []byte, framecount uint32) {
			// pInput is PCM16 little-endian interleaved. Channels=1, so samples=framecount.
			n := int(framecount) * channels
			if n == 0 || len(pInput) < n*2 {
				return
			}
			samples := unsafe.Slice((*int16)(unsafe.Pointer(&pInput[0])), n)
			// Copy out of the C buffer before Push — Push stores into our ring's Go slice.
			copied := make([]int16, n)
			copy(copied, samples)
			ring.Push(copied)
		},
	}

	dev, err := malgo.InitDevice(actx.Raw().Context, deviceConfig, callbacks)
	if err != nil {
		return nil, fmt.Errorf("init capture device: %w", err)
	}
	d.dev = dev
	return d, nil
}

// Start begins capturing and kicks off the drain goroutine that converts the
// ring into fixed-size frames on the Frames() channel.
func (d *Device) Start() error {
	if err := d.dev.Start(); err != nil {
		return fmt.Errorf("start capture: %w", err)
	}
	go d.drain()
	return nil
}

// Frames returns the frame channel. Consumers must drain it; the drain
// goroutine applies drop-oldest on the ring when the channel is full.
func (d *Device) Frames() <-chan []int16 {
	return d.frames
}

// Stop halts capture and closes the Frames channel. Idempotent.
func (d *Device) Stop() {
	d.stopOnce.Do(func() {
		close(d.stopCh)
		if d.dev != nil {
			_ = d.dev.Stop()
			d.dev.Uninit()
		}
	})
}

// drain converts the ring into fixed-size frames. Runs until stopCh closes.
func (d *Device) drain() {
	defer close(d.frames)
	tick := time.NewTicker(time.Duration(d.cfg.FrameMs) * time.Millisecond / 2)
	defer tick.Stop()
	var pending []int16
	for {
		select {
		case <-d.stopCh:
			return
		case <-tick.C:
		}
		pending = append(pending, d.ring.PopAll()...)
		for len(pending) >= d.frameSize {
			frame := make([]int16, d.frameSize)
			copy(frame, pending[:d.frameSize])
			pending = pending[d.frameSize:]
			d.broadcast(frame)
		}
		if now := time.Now(); now.Sub(d.droppedT) > 5*time.Second {
			dropped := d.ring.Dropped()
			if dropped > 0 {
				d.log("capture: dropped samples in last interval", "count", dropped)
				d.droppedT = now
			}
		}
	}
}

// Subscribe returns a buffered channel that receives a copy of every frame.
func (d *Device) Subscribe() <-chan []int16 {
	ch := make(chan []int16, 50)
	d.subsMu.Lock()
	d.subs[ch] = struct{}{}
	d.subsMu.Unlock()
	return ch
}

// Unsubscribe removes ch from the fan-out set and closes it.
func (d *Device) Unsubscribe(ch <-chan []int16) {
	writeCh := *(*chan []int16)(unsafe.Pointer(&ch))
	d.subsMu.Lock()
	delete(d.subs, writeCh)
	d.subsMu.Unlock()
	close(writeCh)
}

// broadcast delivers a copy of frame to every subscriber, dropping if full.
// When user-muted, each copy is zero-filled so every consumer sees silence
// without needing to know about the mute flag.
func (d *Device) broadcast(frame []int16) {
	muted := d.userMuted.Load()
	d.subsMu.RLock()
	defer d.subsMu.RUnlock()
	for ch := range d.subs {
		cp := make([]int16, len(frame))
		if !muted {
			copy(cp, frame)
		}
		select {
		case ch <- cp:
		default:
		}
	}
}

func (d *Device) logLaggingDrop() {
	d.log("capture: frame dropped, consumer lagging")
}
