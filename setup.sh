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

echo "-- TTS (Kokoro) --"
echo "Download from: https://github.com/k2-fsa/sherpa-onnx/releases"
TTS_MODEL=$(prompt "Path to Kokoro model directory" "$DIR/models/kokoro")
TTS_VOICE=$(prompt "Voice" "bm_george")
TTS_SPEED=$(prompt "Speed" "1.0")
echo ""

echo "-- STT (Moonshine) --"
echo "Download from: https://github.com/k2-fsa/sherpa-onnx/releases"
STT_MODEL=$(prompt "Path to Moonshine model directory" "$DIR/models/moonshine")
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

echo "-- Native libs --"
echo "Download the sherpa-onnx JVM package for your OS from:"
echo "https://github.com/k2-fsa/sherpa-onnx/releases"
LIBS_PATH=$(prompt "Path to sherpa-onnx libs directory" "$DIR/libs")
echo ""

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

# Write config.yaml
API_KEY_LINE=""
[ -n "$LLM_API_KEY" ] && API_KEY_LINE="  apiKey: $LLM_API_KEY"

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
  modelPath: $TTS_MODEL
  voice: $TTS_VOICE
  speed: $TTS_SPEED

stt:
  enabled: true
  modelPath: $STT_MODEL

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
