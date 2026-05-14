#!/bin/bash
set -e

DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG="$DIR/config.yaml"

echo "Freeman setup"
echo "============="
echo ""

# Check Java 21+
if ! command -v java &>/dev/null; then
  echo "Java not found. Install Java 21+ and re-run setup."
  exit 1
fi
JAVA_VER=$(java -version 2>&1 | awk -F '"' '/version/ {print $2}' | cut -d'.' -f1)
if [ "$JAVA_VER" -lt 21 ] 2>/dev/null; then
  echo "Java 21+ required (found $JAVA_VER). Please upgrade and re-run."
  exit 1
fi
echo "Java $JAVA_VER found."
echo ""

prompt() {
  local label="$1" default="$2" value
  read -rp "$label [$default]: " value
  echo "${value:-$default}"
}

prompt_yn() {
  local label="$1" default="$2" value
  read -rp "$label (y/n) [$default]: " value
  value="${value:-$default}"
  [[ "$value" =~ ^[Yy] ]] && echo "true" || echo "false"
}

echo "-- Persona --"
PERSONA_NAME=$(prompt "Assistant name" "Freeman")
PERSONA_GREETING=$(prompt "Greeting" "Hey, I'm $PERSONA_NAME. What can I do for you?")
PERSONA_FAREWELL=$(prompt "Farewell" "Catch you later.")
echo ""

echo "-- LLM --"
echo "Provider options: ollama, claude"
LLM_PROVIDER=$(prompt "Provider" "ollama")
if [ "$LLM_PROVIDER" = "claude" ]; then
  LLM_MODEL=$(prompt "Model" "claude-sonnet-4-6")
  LLM_API_KEY=$(prompt "API key (or set ANTHROPIC_API_KEY env var)" "")
  LLM_BASE_URL="https://api.anthropic.com"
else
  LLM_MODEL=$(prompt "Ollama model" "gemma4:e4b")
  LLM_BASE_URL=$(prompt "Ollama base URL" "http://localhost:11434")
  LLM_API_KEY=""
fi
echo ""

echo "-- TTS / STT --"
TTS_VOICE=$(prompt "TTS voice" "bm_george")
TTS_SPEED=$(prompt "TTS speed" "1.0")
echo "STT provider options: whisper (macOS, recommended), moonshine (lower latency)"
STT_PROVIDER=$(prompt "STT provider" "whisper")
echo ""

echo "-- Wake word --"
WAKEWORD_ENABLED=$(prompt_yn "Enable wake word detection?" "n")
if [ "$WAKEWORD_ENABLED" = "true" ]; then
  echo "Download OpenWakeWord + Silero models from: https://github.com/k2-fsa/sherpa-onnx/releases"
  WAKEWORD_DIR=$(prompt "Path to wakeword models directory" "$DIR/models/wakeword")
  WAKEWORD_THRESHOLD=$(prompt "Detection threshold (0.0-1.0)" "0.5")
else
  WAKEWORD_DIR="$DIR/models/wakeword"
  WAKEWORD_THRESHOLD="0.5"
fi
echo ""

echo "-- Memory --"
MEMORY_ENABLED=$(prompt_yn "Enable persistent memory (SQLite)?" "y")
MEMORY_DB=$(prompt "Database path" "~/.freeman/memory.db")
echo ""

LIBS_PATH="$DIR/libs"

# Build libavfoundation_audio_jni.dylib if on macOS and not already present
if [[ "$(uname)" == "Darwin" ]] && [ ! -f "$LIBS_PATH/libavfoundation_audio_jni.dylib" ]; then
  echo "-- AVFoundation Audio JNI --"
  JAVA_HOME="${JAVA_HOME:-$(java -XshowSettings:all -version 2>&1 | awk '/java.home/{print $3}')}"
  JNI_SRC="$DIR/macos/native/libavfoundation_audio_jni.mm"
  if [ -f "$JNI_SRC" ]; then
    mkdir -p "$LIBS_PATH"
    echo "Building libavfoundation_audio_jni.dylib..."
    # Use MacOSX15 SDK — newer SDKs (26+) are missing C++ stdlib headers for clang++
    MACOS_SDK=$(xcrun --sdk macosx15 --show-sdk-path 2>/dev/null || xcrun --show-sdk-path)
    CXX_INCLUDE="$MACOS_SDK/usr/include/c++/v1"
    clang++ -shared -fPIC -O2 \
      -isysroot "$MACOS_SDK" \
      -I"$CXX_INCLUDE" \
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

# Download ML models
echo "-- Downloading ML models --"
if [ -f "$DIR/scripts/setup_models.sh" ]; then
  bash "$DIR/scripts/setup_models.sh"
else
  # Inline model downloads for release ZIP (no scripts/ folder)
  MODELS="$DIR/models"
  mkdir -p "$MODELS"

  if [ ! -f "$MODELS/kokoro/model.onnx" ]; then
    echo "[kokoro] Downloading Kokoro TTS…"
    mkdir -p "$MODELS/kokoro"
    curl -fL "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2" -o /tmp/kokoro.tar.bz2
    tar -xjf /tmp/kokoro.tar.bz2 -C /tmp && cp -r /tmp/kokoro-en-v0_19/. "$MODELS/kokoro/" && rm -f /tmp/kokoro.tar.bz2
    echo "[kokoro] done"
  fi

  if [ ! -f "$MODELS/whisper-small/encoder.int8.onnx" ]; then
    echo "[whisper] Downloading Whisper small.en…"
    mkdir -p "$MODELS/whisper-small"
    curl -fL "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-small.en.tar.bz2" -o /tmp/whisper.tar.bz2
    tar -xjf /tmp/whisper.tar.bz2 -C /tmp && cp -r /tmp/sherpa-onnx-whisper-small.en/. "$MODELS/whisper-small/" && rm -f /tmp/whisper.tar.bz2
    echo "[whisper] done"
  fi

  if [ ! -f "$MODELS/moonshine/encode.int8.onnx" ]; then
    echo "[moonshine] Downloading Moonshine STT…"
    mkdir -p "$MODELS/moonshine"
    curl -fL "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-moonshine-tiny-en-int8.tar.bz2" -o /tmp/moonshine.tar.bz2
    tar -xjf /tmp/moonshine.tar.bz2 -C /tmp && cp -r /tmp/sherpa-onnx-moonshine-tiny-en-int8/. "$MODELS/moonshine/" && rm -f /tmp/moonshine.tar.bz2
    echo "[moonshine] done"
  fi

  if [ ! -f "$MODELS/silero/silero_vad.onnx" ]; then
    echo "[silero] Downloading Silero VAD…"
    mkdir -p "$MODELS/silero"
    curl -fL "https://github.com/snakers4/silero-vad/raw/master/src/silero_vad/data/silero_vad.onnx" -o "$MODELS/silero/silero_vad.onnx"
    echo "[silero] done"
  fi

  if [ ! -f "$MODELS/wakeword/melspectrogram.onnx" ]; then
    echo "[wakeword] Downloading OpenWakeWord shared models…"
    mkdir -p "$MODELS/wakeword"
    OWW_BASE="https://github.com/dscripka/openWakeWord/releases/download/v0.6.0"
    curl -fL "$OWW_BASE/melspectrogram.onnx"  -o "$MODELS/wakeword/melspectrogram.onnx"
    curl -fL "$OWW_BASE/embedding_model.onnx" -o "$MODELS/wakeword/embedding_model.onnx"
    echo "[wakeword] done (you still need hey_freeman.onnx — see README)"
  fi
fi
echo ""

# Write config.yaml
cat > "$CONFIG" <<EOF
persona:
  name: $PERSONA_NAME
  greeting: "$PERSONA_GREETING"
  farewell: "$PERSONA_FAREWELL"
  rules: []

llm:
  provider: $LLM_PROVIDER
  model: $LLM_MODEL
  baseUrl: $LLM_BASE_URL
  numCtx: 8192
  keepAlive: "-1"
$([ -n "$LLM_API_KEY" ] && echo "  apiKey: $LLM_API_KEY")
tts:
  modelPath: $DIR/models/kokoro
  voice: $TTS_VOICE
  speed: $TTS_SPEED

stt:
  enabled: true
  provider: $STT_PROVIDER
  modelPath: $DIR/models/$( [ "$STT_PROVIDER" = "whisper" ] && echo "whisper-small" || echo "moonshine" )

wakeword:
  enabled: $WAKEWORD_ENABLED
  modelsDir: $WAKEWORD_DIR
  threshold: $WAKEWORD_THRESHOLD

memory:
  enabled: $MEMORY_ENABLED
  dbPath: $MEMORY_DB
  historyWindow: 20
  recallLimit: 5

tools:
  dirs: []

libsPath: $LIBS_PATH
EOF

chmod +x "$DIR/run.sh"

echo ""
echo "Config written to $CONFIG"
echo ""
echo "Run Freeman with: ./run.sh"
