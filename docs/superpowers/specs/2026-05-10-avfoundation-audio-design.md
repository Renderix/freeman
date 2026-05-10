# AVFoundation Audio Design

## Problem

PortAudio provides raw mic input with no echo cancellation, noise suppression, or AGC. On macOS with
a single mic (M1 Air), the TTS playback bleeds into mic capture, causing Freeman to transcribe and
respond to its own voice.

## Solution

Replace the entire PortAudio layer with `AVAudioEngine` + `isVoiceProcessingEnabled = true`.
Apple's voice processing pipeline runs in hardware DSP on Apple Silicon — AEC, NS, and AGC are
applied at the driver level before samples reach the JVM tap callback. The OS automatically uses the
engine's output node as the AEC reference signal; no manual reference signal tracking is needed.

## Architecture

```
Mic → AVAudioInputNode (isVoiceProcessingEnabled=true) ──────────────────→ tap → JVM onFrame
                  ↑ AEC reference (OS-managed)                                         ↓
TTS FloatArray → AVAudioPlayerNode → engine mainMixerNode → speakers         STT / VAD pipeline
```

A single Objective-C++ JNI wrapper (`libavfoundation_audio_jni.mm`) owns the `AVAudioEngine`
instance and exposes four JNI methods: `startCapture`, `stopCapture`, `playSamples`, `stopPlayback`.
The Kotlin layer is thin adapters over those four calls.

## Components

| File | Responsibility |
|---|---|
| `macos/native/libavfoundation_audio_jni.mm` | AVAudioEngine lifecycle, tap callback, playback scheduling |
| `macos/src/.../audio/AVFoundationAudioJNI.kt` | JNI declarations + `System.loadLibrary` |
| `macos/src/.../audio/AVFoundationCapture.kt` | Implements `AudioCapture` |
| `macos/src/.../audio/AVFoundationPlayback.kt` | Replaces `PortAudioPlayback` |

## Sample Rates

- **Capture:** tap installed with explicit 16 kHz mono float32 format — AVAudioEngine resamples from hardware rate (48 kHz) internally. Frame size 512 samples (32 ms) unchanged.
- **Playback:** TTS outputs 24 kHz. `AVAudioPCMBuffer` created with 24 kHz format; engine handles resampling to output device.

## Playback Blocking

`playSamples` blocks until the buffer has been fully played using a `dispatch_semaphore`. This preserves the existing synchronous contract used by `onSpeak` in `ConversationLoop`.

## Removed

- `PortAudioJNI.kt`, `PortAudioCapture.kt`, `PortAudioPlayback.kt`
- `macos/native/libportaudio_jni.c`, `macos/libs/libportaudio_jni.dylib`
- `brew install portaudio` from setup scripts

## Build

```bash
clang++ -shared -fPIC -O2 \
  -framework AVFoundation -framework AudioToolbox -framework Foundation \
  -I$JAVA_HOME/include -I$JAVA_HOME/include/darwin \
  -o macos/libs/libavfoundation_audio_jni.dylib \
  macos/native/libavfoundation_audio_jni.mm
```

AVFoundation is a macOS system framework — no brew dependencies.
