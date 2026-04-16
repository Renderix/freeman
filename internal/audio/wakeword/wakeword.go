package wakeword

import (
	"log/slog"

	porcupine "github.com/Picovoice/porcupine/binding/go"
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

type Config struct {
	// AccessKey is reserved for future Porcupine v2+ API key support.
	// The v1.9.x binding does not require an access key.
	AccessKey     string
	KeywordPaths  []string
	Sensitivities []float32
	Logger        *slog.Logger
}

type Detector struct {
	porc   porcupine.Porcupine
	events chan KeywordKind
	stopCh chan struct{}
	log    *slog.Logger
}

func NewDetector(cfg Config) (*Detector, error) {
	p := porcupine.Porcupine{
		KeywordPaths:  cfg.KeywordPaths,
		Sensitivities: cfg.Sensitivities,
	}
	if err := p.Init(); err != nil {
		return nil, err
	}
	return &Detector{
		porc:   p,
		events: make(chan KeywordKind, 4),
		stopCh: make(chan struct{}),
		log:    cfg.Logger,
	}, nil
}

func (d *Detector) Events() <-chan KeywordKind {
	return d.events
}

func (d *Detector) Run(frames <-chan []int16) {
	go d.readLoop(frames)
}

func (d *Detector) readLoop(frames <-chan []int16) {
	frameLen := porcupine.FrameLength
	var buf []int16

	for {
		select {
		case <-d.stopCh:
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			buf = append(buf, frame...)
			for len(buf) >= frameLen {
				idx, err := d.porc.Process(buf[:frameLen])
				if err != nil {
					d.log.Error("porcupine process error", "err", err)
					buf = buf[frameLen:]
					continue
				}
				if idx >= 0 {
					kind := KeywordKind(idx)
					d.log.Info("keyword detected", "keyword", kind.String())
					select {
					case d.events <- kind:
					default:
					}
				}
				buf = buf[frameLen:]
			}
		}
	}
}

func (d *Detector) Stop() {
	close(d.stopCh)
	d.porc.Delete()
}
