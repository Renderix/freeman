package session

import (
	"time"

	"github.com/Renderix/freeman/internal/buffer"
	"github.com/Renderix/freeman/internal/engine"
)

// Error codes per spec §3.3
const (
	ErrorCodeInvalidVoice   = "INVALID_VOICE"
	ErrorCodeTtsError       = "TTS_ERROR"
	ErrorCodeBufferOverflow = "BUFFER_OVERFLOW"
	ErrorCodeTimeout        = "TIMEOUT"
	ErrorCodeFatal          = "FATAL"
)

// Session manages an individual TTS streaming session.
type Session struct {
	ID                 string
	Voice              string
	Speed              float64
	Engine             *engine.TTSEngine
	Buffer             *buffer.SentenceBuffer
	SendAudio          func([]byte) error
	SendJSON           func(interface{}) error
	IsActive           bool
	SentenceCount      int
	StartTime          time.Time
	TotalDurationMs    int
	SentencesCompleted int
	PendingSentences   []string
}

// NewSession creates a new session.
func NewSession(id, voice string, speed float64, ttsEngine *engine.TTSEngine, sendAudio func([]byte) error, sendJSON func(interface{}) error) *Session {
	return &Session{
		ID:               id,
		Voice:            voice,
		Speed:            speed,
		Engine:           ttsEngine,
		Buffer:           buffer.NewSentenceBuffer(2.0, 150),
		SendAudio:        sendAudio,
		SendJSON:         sendJSON,
		IsActive:         true,
		StartTime:        time.Now(),
		PendingSentences: make([]string, 0),
	}
}

// HandleText handles incoming text chunks.
func (s *Session) HandleText(chunk string, isFinal bool) (int, error) {
	sentences := s.Buffer.AddChunk(chunk)

	if isFinal {
		finalSentence := s.Buffer.Flush()
		if finalSentence != "" {
			sentences = append(sentences, finalSentence)
		}
	}

	for _, sentence := range sentences {
		if err := s.processSentence(sentence); err != nil {
			return 0, err
		}
	}

	return len(s.PendingSentences), nil
}

func (s *Session) processSentence(text string) error {
	s.SentenceCount++
	sID := s.SentenceCount

	s.PendingSentences = append(s.PendingSentences, text)
	queuePosition := len(s.PendingSentences) - 1

	estimatedDuration := s.estimateDurationMs(text)

	s.SendJSON(map[string]interface{}{
		"type":                   "sentence_start",
		"id":                     sID,
		"text":                   text,
		"estimated_duration_sec": float64(estimatedDuration) / 1000.0,
		"queue_position":         queuePosition,
	})

	startGen := time.Now()

	// Generate audio
	audioBytes, err := s.Engine.Generate(text, s.Voice, s.Speed)

	// Remove from pending
	for i, p := range s.PendingSentences {
		if p == text {
			s.PendingSentences = append(s.PendingSentences[:i], s.PendingSentences[i+1:]...)
			break
		}
	}

	if err == nil && audioBytes != nil {
		if err := s.SendAudio(audioBytes); err != nil {
			return err
		}

		genTimeMs := int(time.Since(startGen).Milliseconds())
		durationMs := s.calculateDurationMs(audioBytes)
		s.TotalDurationMs += durationMs
		s.SentencesCompleted++

		s.SendJSON(map[string]interface{}{
			"type":            "sentence_complete",
			"id":              sID,
			"duration_ms":     durationMs,
			"processing_ms":   genTimeMs,
			"queue_remaining": len(s.PendingSentences),
		})
	} else {
		s.SendJSON(map[string]interface{}{
			"type":        "error",
			"sentence_id": sID,
			"message":     "TTS generation failed",
			"recoverable": true,
			"code":        ErrorCodeTtsError,
		})
	}

	return nil
}

func (s *Session) estimateDurationMs(text string) int {
	// Rough estimate: ~80ms per character
	baseDuration := len(text) * 80
	return int(float64(baseDuration) / s.Speed)
}

func (s *Session) calculateDurationMs(audioBytes []byte) int {
	// WAV header is 44 bytes
	if len(audioBytes) <= 44 {
		return 0
	}
	audioDataBytes := len(audioBytes) - 44
	// 24kHz, 16-bit mono = 48000 bytes per second = 48 bytes per ms
	return int(audioDataBytes / 48)
}

// Flush force flushes the buffer.
func (s *Session) Flush() error {
	sentence := s.Buffer.Flush()
	if sentence != "" {
		return s.processSentence(sentence)
	}
	return nil
}

// CheckTimeout checks for partial sentence timeout.
func (s *Session) CheckTimeout() error {
	sentence := s.Buffer.CheckTimeout()
	if sentence != "" {
		return s.processSentence(sentence)
	}
	return nil
}

// SendStreamEnd sends stream_end message.
func (s *Session) SendStreamEnd(reason string) error {
	return s.SendJSON(map[string]interface{}{
		"type":              "stream_end",
		"total_sentences":   s.SentencesCompleted,
		"total_duration_ms": s.TotalDurationMs,
		"reason":            reason,
	})
}
