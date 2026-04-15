package stt

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodeWAV_Header(t *testing.T) {
	samples := []int16{0, 100, -100, 200, -200, 300}
	buf := EncodeWAV(samples, 16000)
	if len(buf) != 44+len(samples)*2 {
		t.Fatalf("len = %d, want %d", len(buf), 44+len(samples)*2)
	}
	if !bytes.Equal(buf[0:4], []byte("RIFF")) {
		t.Errorf("missing RIFF")
	}
	if !bytes.Equal(buf[8:12], []byte("WAVE")) {
		t.Errorf("missing WAVE")
	}
	var sr uint32
	_ = binary.Read(bytes.NewReader(buf[24:28]), binary.LittleEndian, &sr)
	if sr != 16000 {
		t.Errorf("sr = %d", sr)
	}
	// Data bytes start at 44, little-endian int16.
	var first int16
	_ = binary.Read(bytes.NewReader(buf[44:46]), binary.LittleEndian, &first)
	if first != 0 {
		t.Errorf("first sample = %d, want 0", first)
	}
}
