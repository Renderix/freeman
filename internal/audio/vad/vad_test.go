package vad

import (
	"context"
	"testing"
	"time"
)

// fakeDetector flips "is speech" according to a scripted slice, one value per call.
type fakeDetector struct {
	script []bool
	idx    int
}

func (f *fakeDetector) IsSpeech(frame []int16, sampleRate int) (bool, error) {
	if f.idx >= len(f.script) {
		return false, nil
	}
	v := f.script[f.idx]
	f.idx++
	return v, nil
}

func frame() []int16 { return make([]int16, 320) } // 20 ms at 16 kHz

// Script: 5 silence frames, 20 speech frames (400 ms), 40 silence frames (800 ms).
// Speech segment is 400 ms, above the 300 ms min, so one utterance should fire.
func TestVAD_SingleUtterance(t *testing.T) {
	script := make([]bool, 0, 65)
	for i := 0; i < 5; i++ {
		script = append(script, false)
	}
	for i := 0; i < 20; i++ {
		script = append(script, true)
	}
	for i := 0; i < 40; i++ {
		script = append(script, false)
	}
	fd := &fakeDetector{script: script}
	v := NewWithDetector(Config{
		SilenceMs:      800,
		MinSpeechMs:    300,
		HangoverMs:     0,
		Aggressiveness: 2,
		SampleRate:     16000,
		FrameMs:        20,
	}, fd)

	in := make(chan []int16, len(script))
	for range script {
		in <- frame()
	}
	close(in)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := v.Run(ctx, in)

	var got []Utterance
	for u := range out {
		got = append(got, u)
	}
	if len(got) != 1 {
		t.Fatalf("utterances = %d, want 1", len(got))
	}
	if got[0].DurationMs != 400 {
		t.Errorf("duration = %d, want 400", got[0].DurationMs)
	}
	expectedSamples := 20 * 320
	if len(got[0].PCM) != expectedSamples {
		t.Errorf("pcm len = %d, want %d", len(got[0].PCM), expectedSamples)
	}
}

// Script: speech burst under MinSpeechMs followed by silence — drop.
func TestVAD_DropsShortSpeech(t *testing.T) {
	script := make([]bool, 0, 40)
	for i := 0; i < 5; i++ {
		script = append(script, true) // 100 ms, under 300 ms
	}
	for i := 0; i < 45; i++ {
		script = append(script, false) // 900 ms silence
	}
	fd := &fakeDetector{script: script}
	v := NewWithDetector(Config{
		SilenceMs:   800,
		MinSpeechMs: 300,
		SampleRate:  16000,
		FrameMs:     20,
	}, fd)

	in := make(chan []int16, len(script))
	for range script {
		in <- frame()
	}
	close(in)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := v.Run(ctx, in)

	var got []Utterance
	for u := range out {
		got = append(got, u)
	}
	if len(got) != 0 {
		t.Errorf("utterances = %d, want 0", len(got))
	}
}

// Script: two separated speech bursts, both above threshold — two utterances.
func TestVAD_TwoUtterances(t *testing.T) {
	script := make([]bool, 0)
	script = append(script, boolN(3, false)...)  // 60 ms pre
	script = append(script, boolN(20, true)...)  // 400 ms speech
	script = append(script, boolN(45, false)...) // 900 ms silence
	script = append(script, boolN(20, true)...)  // 400 ms speech
	script = append(script, boolN(45, false)...) // 900 ms silence
	fd := &fakeDetector{script: script}
	v := NewWithDetector(Config{
		SilenceMs:   800,
		MinSpeechMs: 300,
		SampleRate:  16000,
		FrameMs:     20,
	}, fd)

	in := make(chan []int16, len(script))
	for range script {
		in <- frame()
	}
	close(in)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := v.Run(ctx, in)

	var got []Utterance
	for u := range out {
		got = append(got, u)
	}
	if len(got) != 2 {
		t.Fatalf("utterances = %d, want 2", len(got))
	}
}

// Script: 5 silence frames then 20 speech frames.
// SpeechOnsets() must fire once, before the utterance channel fires.
func TestVAD_SpeechOnset(t *testing.T) {
	script := append(boolN(5, false), boolN(20, true)...)
	script = append(script, boolN(40, false)...) // 800 ms silence to flush utterance
	fd := &fakeDetector{script: script}
	v := NewWithDetector(Config{
		SilenceMs:   800,
		MinSpeechMs: 300,
		SampleRate:  16000,
		FrameMs:     20,
	}, fd)

	in := make(chan []int16, len(script))
	for range script {
		in <- frame()
	}
	close(in)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := v.Run(ctx, in)

	// Onset must arrive before or simultaneously with the first utterance.
	onsets := v.SpeechOnsets()

	select {
	case <-onsets:
		// good: got an onset
	case <-out:
		t.Fatal("utterance arrived before onset")
	case <-ctx.Done():
		t.Fatal("timed out waiting for onset")
	}

	// Utterance should follow.
	select {
	case u, ok := <-out:
		if !ok {
			t.Fatal("utterance channel closed without emitting")
		}
		if u.DurationMs != 400 {
			t.Errorf("duration = %d, want 400", u.DurationMs)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for utterance after onset")
	}
}

func boolN(n int, v bool) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = v
	}
	return out
}
