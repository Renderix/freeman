// Package capture drives the microphone through malgo and exposes a clean Go
// channel of fixed-size PCM frames for downstream VAD/STT.
package capture

import (
	"fmt"
	"sync"
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
	stopOnce  sync.Once
	stopCh    chan struct{}
	frameSize int // samples per frame
	droppedT  time.Time
	log       func(msg string, kv ...any)
}

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
			select {
			case d.frames <- frame:
			default:
				// consumer is lagging — ring already applied drop-oldest,
				// so just drop this frame and log once in a while.
				d.logLaggingDrop()
			}
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

func (d *Device) logLaggingDrop() {
	d.log("capture: frame dropped, consumer lagging")
}
