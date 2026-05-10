# AVFoundation Audio Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace PortAudio capture and playback with AVAudioEngine + voice processing for hardware-accelerated AEC, noise suppression, and AGC on macOS/M1.

**Architecture:** A single Objective-C++ JNI wrapper owns the AVAudioEngine instance and exposes four native methods. Thin Kotlin adapters implement the existing `AudioCapture` interface and replace `PortAudioPlayback`. `isVoiceProcessingEnabled = true` on the input node hands echo cancellation to Apple's hardware DSP.

**Tech Stack:** AVFoundation (macOS system framework), Objective-C++, JNI, Kotlin/JVM

---

## File Map

| Action | Path |
|---|---|
| Create | `macos/native/libavfoundation_audio_jni.mm` |
| Create | `macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationAudioJNI.kt` |
| Create | `macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationCapture.kt` |
| Create | `macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationPlayback.kt` |
| Modify | `macos/src/macosMain/kotlin/ai/freeman/macos/Main.kt` |
| Modify | `setup.sh` |
| Modify | `scripts/setup_kotlin_libs.sh` |
| Modify | `.github/workflows/release.yml` |
| Delete | `macos/native/libportaudio_jni.c` |
| Delete | `macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioJNI.kt` |
| Delete | `macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioCapture.kt` |
| Delete | `macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioPlayback.kt` |

---

### Task 1: Write the AVFoundation JNI wrapper

**Files:**
- Create: `macos/native/libavfoundation_audio_jni.mm`

- [ ] **Step 1: Create the file**

```objc
#import <AVFoundation/AVFoundation.h>
#import <jni.h>
#import <stdlib.h>
#import <string.h>

static JavaVM*            jvm        = NULL;
static jobject            gCallback  = NULL;
static jmethodID          gOnFrame   = NULL;
static AVAudioEngine*     engine     = nil;
static AVAudioPlayerNode* playerNode = nil;

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_startCapture(
        JNIEnv* env, jclass cls, jobject callback, jint sampleRate, jint frameSize) {
    (*env)->GetJavaVM(env, &jvm);
    gCallback = (*env)->NewGlobalRef(env, callback);
    jclass cbClass = (*env)->GetObjectClass(env, gCallback);
    gOnFrame = (*env)->GetMethodID(env, cbClass, "onFrame", "([F)V");

    engine     = [[AVAudioEngine alloc] init];
    playerNode = [[AVAudioPlayerNode alloc] init];
    [engine attachNode:playerNode];
    [engine connect:playerNode to:[engine mainMixerNode] format:nil];

    AVAudioInputNode* inputNode = [engine inputNode];
    NSError* err = nil;
    if (![inputNode setVoiceProcessingEnabled:YES error:&err]) {
        NSLog(@"[AVFoundationJNI] voice processing unavailable: %@", err);
    }

    AVAudioFormat* captureFmt = [[AVAudioFormat alloc]
        initWithCommonFormat:AVAudioPCMFormatFloat32
                  sampleRate:(double)sampleRate
                    channels:1
                 interleaved:NO];

    [inputNode installTapOnBus:0
                    bufferSize:(AVAudioFrameCount)frameSize
                        format:captureFmt
                         block:^(AVAudioPCMBuffer* buf, AVAudioTime* when) {
        float* data  = buf.floatChannelData[0];
        jsize  count = (jsize)buf.frameLength;
        JNIEnv* tapEnv;
        if ((*jvm)->AttachCurrentThread(jvm, (void**)&tapEnv, NULL) != JNI_OK) return;
        jfloatArray arr = (*tapEnv)->NewFloatArray(tapEnv, count);
        (*tapEnv)->SetFloatArrayRegion(tapEnv, arr, 0, count, data);
        (*tapEnv)->CallVoidMethod(tapEnv, gCallback, gOnFrame, arr);
        (*tapEnv)->DeleteLocalRef(tapEnv, arr);
    }];

    if (![engine startAndReturnError:&err]) {
        NSLog(@"[AVFoundationJNI] engine start failed: %@", err);
        return -1;
    }
    [playerNode play];
    return 0;
}

JNIEXPORT void JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_stopCapture(
        JNIEnv* env, jclass cls) {
    if (engine) {
        [[engine inputNode] removeTapOnBus:0];
        [engine stop];
        engine = nil;
    }
    playerNode = nil;
    if (gCallback) { (*env)->DeleteGlobalRef(env, gCallback); gCallback = NULL; }
}

JNIEXPORT jint JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_playSamples(
        JNIEnv* env, jclass cls, jfloatArray samples, jint sampleRate) {
    if (!playerNode || !engine) return -1;

    jsize  len  = (*env)->GetArrayLength(env, samples);
    jfloat* src = (*env)->GetFloatArrayElements(env, samples, NULL);

    AVAudioFormat* fmt = [[AVAudioFormat alloc]
        initWithCommonFormat:AVAudioPCMFormatFloat32
                  sampleRate:(double)sampleRate
                    channels:1
                 interleaved:NO];
    AVAudioPCMBuffer* buf = [[AVAudioPCMBuffer alloc]
        initWithPCMFormat:fmt frameCapacity:(AVAudioFrameCount)len];
    buf.frameLength = (AVAudioFrameCount)len;
    memcpy(buf.floatChannelData[0], src, len * sizeof(float));
    (*env)->ReleaseFloatArrayElements(env, samples, src, JNI_ABORT);

    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [playerNode scheduleBuffer:buf completionHandler:^{ dispatch_semaphore_signal(sem); }];
    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    return 0;
}

JNIEXPORT void JNICALL Java_ai_freeman_macos_audio_AVFoundationAudioJNI_stopPlayback(
        JNIEnv* env, jclass cls) {
    if (playerNode) [playerNode stop];
}
```

- [ ] **Step 2: Compile and verify it builds**

```bash
JAVA_HOME=$(java -XshowSettings:all -version 2>&1 | awk '/java.home/{print $3}')
clang++ -shared -fPIC -O2 \
  -framework AVFoundation -framework AudioToolbox -framework Foundation \
  -I"$JAVA_HOME/include" -I"$JAVA_HOME/include/darwin" \
  -o macos/libs/libavfoundation_audio_jni.dylib \
  macos/native/libavfoundation_audio_jni.mm
```

Expected: `macos/libs/libavfoundation_audio_jni.dylib` created with no errors.

```bash
file macos/libs/libavfoundation_audio_jni.dylib
```

Expected: `Mach-O 64-bit dynamically linked shared library arm64`

- [ ] **Step 3: Commit**

```bash
git add macos/native/libavfoundation_audio_jni.mm macos/libs/libavfoundation_audio_jni.dylib
git commit -m "feat(audio): add AVFoundation JNI wrapper with voice processing"
```

---

### Task 2: Write Kotlin JNI declarations and audio classes

**Files:**
- Create: `macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationAudioJNI.kt`
- Create: `macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationCapture.kt`
- Create: `macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationPlayback.kt`

- [ ] **Step 1: Create AVFoundationAudioJNI.kt**

```kotlin
package ai.freeman.macos.audio

object AVFoundationAudioJNI {
    init {
        System.loadLibrary("avfoundation_audio_jni")
    }

    @JvmStatic external fun startCapture(callback: FrameCallback, sampleRate: Int, framesPerBuffer: Int): Int
    @JvmStatic external fun stopCapture()
    @JvmStatic external fun playSamples(samples: FloatArray, sampleRate: Int): Int
    @JvmStatic external fun stopPlayback()

    interface FrameCallback {
        fun onFrame(samples: FloatArray)
    }
}
```

- [ ] **Step 2: Create AVFoundationCapture.kt**

```kotlin
package ai.freeman.macos.audio

import ai.freeman.audio.AudioCapture
import ai.freeman.audio.AudioFrame

class AVFoundationCapture : AudioCapture {
    override fun start(onFrame: (FloatArray) -> Unit) {
        AVFoundationAudioJNI.startCapture(object : AVFoundationAudioJNI.FrameCallback {
            override fun onFrame(samples: FloatArray) = onFrame(samples)
        }, AudioFrame.SAMPLE_RATE, AudioFrame.FRAME_SIZE)
    }

    override fun stop() = AVFoundationAudioJNI.stopCapture()
}
```

- [ ] **Step 3: Create AVFoundationPlayback.kt**

```kotlin
package ai.freeman.macos.audio

class AVFoundationPlayback {
    fun play(samples: FloatArray, sampleRate: Int = 24000) {
        AVFoundationAudioJNI.playSamples(samples, sampleRate)
    }

    fun stop() = AVFoundationAudioJNI.stopPlayback()
}
```

- [ ] **Step 4: Verify compilation**

```bash
./gradlew :macos:compileKotlinMacos 2>&1 | tail -10
```

Expected: `BUILD SUCCESSFUL`

- [ ] **Step 5: Commit**

```bash
git add macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationAudioJNI.kt \
        macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationCapture.kt \
        macos/src/macosMain/kotlin/ai/freeman/macos/audio/AVFoundationPlayback.kt
git commit -m "feat(audio): add AVFoundation Kotlin audio classes"
```

---

### Task 3: Wire into Main.kt and delete PortAudio

**Files:**
- Modify: `macos/src/macosMain/kotlin/ai/freeman/macos/Main.kt`
- Delete: `macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioJNI.kt`
- Delete: `macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioCapture.kt`
- Delete: `macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioPlayback.kt`
- Delete: `macos/native/libportaudio_jni.c`

- [ ] **Step 1: Update Main.kt imports and instantiation**

In `macos/src/macosMain/kotlin/ai/freeman/macos/Main.kt`, replace:

```kotlin
import ai.freeman.macos.audio.PortAudioCapture
import ai.freeman.macos.audio.PortAudioPlayback
```

with:

```kotlin
import ai.freeman.macos.audio.AVFoundationCapture
import ai.freeman.macos.audio.AVFoundationPlayback
```

And replace:

```kotlin
val playback = PortAudioPlayback()
```

with:

```kotlin
val playback = AVFoundationPlayback()
```

And replace:

```kotlin
val capture = PortAudioCapture()
```

with:

```kotlin
val capture = AVFoundationCapture()
```

- [ ] **Step 2: Delete PortAudio files**

```bash
rm macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioJNI.kt
rm macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioCapture.kt
rm macos/src/macosMain/kotlin/ai/freeman/macos/audio/PortAudioPlayback.kt
rm macos/native/libportaudio_jni.c
```

- [ ] **Step 3: Build fat JAR**

```bash
./gradlew :macos:macosJar 2>&1 | tail -10
```

Expected: `BUILD SUCCESSFUL`

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat(audio): replace PortAudio with AVFoundation in Main.kt, remove PortAudio files"
```

---

### Task 4: Update build scripts

**Files:**
- Modify: `setup.sh`
- Modify: `scripts/setup_kotlin_libs.sh`
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Update setup.sh**

Find and replace the PortAudio JNI build block in `setup.sh`:

```bash
# Build libportaudio_jni.dylib if on macOS and not already present
if [[ "$(uname)" == "Darwin" ]] && [ ! -f "$LIBS_PATH/libportaudio_jni.dylib" ]; then
```

Replace the entire block (from that line through the closing `fi`) with:

```bash
# Build libavfoundation_audio_jni.dylib if on macOS and not already present
if [[ "$(uname)" == "Darwin" ]] && [ ! -f "$LIBS_PATH/libavfoundation_audio_jni.dylib" ]; then
  echo "-- AVFoundation Audio JNI --"
  JAVA_HOME="${JAVA_HOME:-$(java -XshowSettings:all -version 2>&1 | awk '/java.home/{print $3}')}"
  JNI_SRC="$DIR/macos/native/libavfoundation_audio_jni.mm"
  if [ -f "$JNI_SRC" ]; then
    mkdir -p "$LIBS_PATH"
    echo "Building libavfoundation_audio_jni.dylib..."
    clang++ -shared -fPIC -O2 \
      -framework AVFoundation -framework AudioToolbox -framework Foundation \
      -I"$JAVA_HOME/include" -I"$JAVA_HOME/include/darwin" \
      -o "$LIBS_PATH/libavfoundation_audio_jni.dylib" \
      "$JNI_SRC"
    echo "libavfoundation_audio_jni.dylib → $LIBS_PATH/"
  else
    echo "JNI source not found at $JNI_SRC — skipping."
  fi
  echo ""
fi
```

- [ ] **Step 2: Update scripts/setup_kotlin_libs.sh**

Find and replace the PortAudio JNI build block (the block that was removed in the earlier session — verify it's already gone). If it still exists, remove it entirely. No AVFoundation block needed here since `setup.sh` handles it.

- [ ] **Step 3: Update .github/workflows/release.yml**

In `.github/workflows/release.yml`, find the step that downloads sherpa-onnx JARs and add a step after it to compile the JNI wrapper:

```yaml
      - name: Build AVFoundation JNI wrapper
        run: |
          JAVA_HOME=$(java -XshowSettings:all -version 2>&1 | awk '/java.home/{print $3}')
          clang++ -shared -fPIC -O2 \
            -framework AVFoundation -framework AudioToolbox -framework Foundation \
            -I"$JAVA_HOME/include" -I"$JAVA_HOME/include/darwin" \
            -o macos/libs/libavfoundation_audio_jni.dylib \
            macos/native/libavfoundation_audio_jni.mm
```

Also remove any `brew install portaudio` step if present.

- [ ] **Step 4: Commit**

```bash
git add setup.sh scripts/setup_kotlin_libs.sh .github/workflows/release.yml
git commit -m "feat(build): replace portaudio build with avfoundation JNI build in all scripts"
```

---

### Task 5: Smoke test and release

- [ ] **Step 1: Delete stale dylib and rebuild**

```bash
rm -f macos/libs/libportaudio_jni.dylib
./gradlew :macos:macosJar 2>&1 | tail -5
```

Expected: `BUILD SUCCESSFUL`

- [ ] **Step 2: Run Freeman and verify audio**

```bash
source ~/.zsh_aliases && export ANTHROPIC_API_KEY && \
  java -Djava.library.path=macos/libs \
       -jar macos/build/libs/macos-macos.jar config.yaml
```

Expected:
- `[Freeman] Hey, I'm Freeman. What can I do for you?` printed and spoken
- `[Freeman] Listening...` printed
- Speaking a question → `[User] <transcription>` printed → Freeman speaks a reply
- AI voice does NOT re-trigger the pipeline (voice processing AEC working)

- [ ] **Step 3: Tag and release**

```bash
git tag v1.0.6
git push origin main --tags
```

Then create GitHub release:

```bash
gh release create v1.0.6 --title "v1.0.6" --notes "$(cat <<'EOF'
## What's new

- **Audio**: Replace PortAudio with AVAudioEngine + hardware voice processing on macOS/M1
  - Hardware-accelerated AEC, noise suppression, and AGC via Apple Silicon DSP
  - Eliminates echo feedback (AI voice no longer re-triggers the pipeline)
  - No PortAudio brew dependency — AVFoundation is a macOS system framework
- **System prompt**: Default system prompt now loaded from bundled `system-prompt.md`; override via `systemPrompt:` in config.yaml
- **TTS**: Stream sentence-by-sentence to reduce time-to-first-audio
- **STT**: Fix missing `setTokens` in Moonshine config
- **LLM**: Fix blank response from OllamaProvider stream parser
- **Memory**: Fix FTS5 syntax error on punctuated input
EOF
)"
```
