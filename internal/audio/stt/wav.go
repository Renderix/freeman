package stt

import "encoding/binary"

// EncodeWAV wraps PCM16 mono samples in a RIFF/WAVE header in memory.
func EncodeWAV(samples []int16, sampleRate int) []byte {
	const numChannels = 1
	const bitsPerSample = 16
	dataSize := len(samples) * 2
	buf := make([]byte, 44+dataSize)

	copy(buf[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], []byte("WAVE"))
	copy(buf[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], numChannels)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*numChannels*bitsPerSample/8))
	binary.LittleEndian.PutUint16(buf[32:34], numChannels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(buf[34:36], bitsPerSample)
	copy(buf[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[44+i*2:46+i*2], uint16(s))
	}
	return buf
}
