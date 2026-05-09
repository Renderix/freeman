# Running Freeman on Android

## Prerequisites

| Requirement | Version | Install |
|-------------|---------|---------|
| JDK 17+ | 17–21 | `brew install --cask temurin@21` |
| Android SDK | API 29+ | Via Android Studio or `sdkmanager` |
| Android Studio | latest | [developer.android.com/studio](https://developer.android.com/studio) |
| Device / Emulator | Android 10+ (API 29) | ARM64 device recommended |

> Gemma 4 E4B requires ~4 GB RAM. A physical ARM64 device is strongly recommended —
> x86 emulators are too slow for on-device inference.

---

## 1. Clone the repo

```bash
git clone https://github.com/Renderix/freeman.git
cd freeman
```

## 2. Download native libraries

```bash
./scripts/setup_kotlin_libs.sh
```

This places sherpa-onnx `.so` files under `android/src/main/jniLibs/` for all ABI targets
(arm64-v8a, armeabi-v7a, x86, x86_64). The Android build picks them up automatically.

## 3. Download the LLM model

Gemma 4 E4B is distributed as a LiteRT `.task` file. Download it from
[Kaggle — google/gemma/tfLite](https://www.kaggle.com/models/google/gemma/tfLite)
(requires a free Kaggle account and agreeing to the Gemma licence).

Push it to your device:

```bash
adb push gemma4-e4b.task /sdcard/Download/gemma4-e4b.task
```

Then copy it to the app's private storage on first launch, or update the path in
`android/src/main/kotlin/ai/freeman/android/MainActivity.kt`:

```kotlin
val llm = LiteRtProvider(
    modelPath = "/sdcard/Download/gemma4-e4b.task",
    context = this,
)
```

## 4. Download TTS + VAD models

The sherpa-onnx Kokoro models and Silero VAD need to be pushed to the device:

```bash
# Download on your Mac first (reuses the same script)
./scripts/setup_models.sh

# Push to app files directory (replace <package> if you change the app ID)
DEVICE_DIR=/data/data/ai.freeman.android/files/models

adb shell mkdir -p $DEVICE_DIR/kokoro
adb push models/kokoro/ $DEVICE_DIR/kokoro/

adb shell mkdir -p $DEVICE_DIR/silero
adb push models/silero/silero_vad.onnx $DEVICE_DIR/silero/

adb shell mkdir -p $DEVICE_DIR/wakeword
adb push models/wakeword/ $DEVICE_DIR/wakeword/
```

> You can also place these files in `android/src/main/assets/` and load them from assets
> at first launch — that avoids the manual `adb push` step but increases APK size.

## 5. Build and install

Open the project in Android Studio and run the `android` configuration, or via CLI:

```bash
./gradlew :android:installDebug
```

## 6. Grant microphone permission

On first launch Android will prompt for microphone access. Grant it — Freeman won't start
audio capture until the permission is confirmed.

---

## Configuration

Android doesn't use `config.yaml`. Settings are hardcoded in `MainActivity.kt`. Key values
to change:

```kotlin
// Model paths (relative to context.filesDir)
val llm = LiteRtProvider(modelPath = "$modelBase/models/gemma4-e4b.task", context = this)
val vad = SileroVAD("$modelBase/models/silero/silero_vad.onnx")

// Wake word sensitivity
val wakeWord = OnnxWakeWord(
    ...
    threshold = 0.5f,   // lower = more sensitive
)

// TTS voice — see voiceIds map in AndroidTTSFactory.kt
// "af_heart" | "bm_george" | "af_bella" | "am_adam" | …
```

---

## Troubleshooting

**App crashes on launch with `UnsatisfiedLinkError`**
The sherpa-onnx `.so` files are missing. Run `./scripts/setup_kotlin_libs.sh` and rebuild.

**LiteRT model fails to load**
Confirm the `.task` file path is correct and the file is fully copied (check `adb shell ls -lh`).
Gemma 4 E4B requires the MediaPipe LiteRT runtime — included as a Gradle dependency, no extra install needed.

**Wake word never triggers**
Lower `threshold` toward `0.3`. Ensure background noise is low and the microphone is not
muted at the OS level.

**Out of memory / slow inference**
Gemma 4 E4B needs ~4 GB RAM. Close other apps. On low-RAM devices consider switching to
a smaller model (e.g. Gemma 3 1B `.task`).
