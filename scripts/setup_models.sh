#!/usr/bin/env bash
# Downloads all runtime models required to run Freeman on macOS.
# Run once: ./scripts/setup_models.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODELS="$SCRIPT_DIR/../models"

mkdir -p "$MODELS"

# ── Kokoro TTS ─────────────────────────────────────────────────────────────
KOKORO_DIR="$MODELS/kokoro"
if [ ! -f "$KOKORO_DIR/model.onnx" ]; then
    echo "[kokoro] Downloading Kokoro-82M ONNX…"
    mkdir -p "$KOKORO_DIR"
    BASE="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models"
    curl -fL "$BASE/kokoro-en-v0_19.tar.bz2" -o /tmp/kokoro.tar.bz2
    tar -xjf /tmp/kokoro.tar.bz2 -C /tmp
    cp -r /tmp/kokoro-en-v0_19/. "$KOKORO_DIR/"
    rm -f /tmp/kokoro.tar.bz2
    echo "[kokoro] → models/kokoro/"
else
    echo "[kokoro] Already present — skipping."
fi

# ── Moonshine STT ──────────────────────────────────────────────────────────
MOONSHINE_DIR="$MODELS/moonshine"
if [ ! -f "$MOONSHINE_DIR/encode.int8.onnx" ]; then
    echo "[moonshine] Downloading Moonshine Tiny ONNX…"
    mkdir -p "$MOONSHINE_DIR"
    curl -fL "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-moonshine-tiny-en-int8.tar.bz2" \
         -o /tmp/moonshine.tar.bz2
    tar -xjf /tmp/moonshine.tar.bz2 -C /tmp
    cp -r /tmp/sherpa-onnx-moonshine-tiny-en-int8/. "$MOONSHINE_DIR/"
    rm -f /tmp/moonshine.tar.bz2
    echo "[moonshine] → models/moonshine/"
else
    echo "[moonshine] Already present — skipping."
fi

# ── Silero VAD ─────────────────────────────────────────────────────────────
SILERO_DIR="$MODELS/silero"
if [ ! -f "$SILERO_DIR/silero_vad.onnx" ]; then
    echo "[silero] Downloading Silero VAD ONNX…"
    mkdir -p "$SILERO_DIR"
    curl -fL "https://github.com/snakers4/silero-vad/raw/master/src/silero_vad/data/silero_vad.onnx" \
         -o "$SILERO_DIR/silero_vad.onnx"
    echo "[silero] → models/silero/"
else
    echo "[silero] Already present — skipping."
fi

# ── OpenWakeWord / hey-freeman ─────────────────────────────────────────────
WW_DIR="$MODELS/wakeword"
if [ ! -f "$WW_DIR/hey_freeman.onnx" ]; then
    echo "[wakeword] Downloading OpenWakeWord shared models…"
    mkdir -p "$WW_DIR"
    OWW_BASE="https://github.com/dscripka/openWakeWord/releases/download/v0.6.0"
    curl -fL "$OWW_BASE/melspectrogram.onnx"   -o "$WW_DIR/melspectrogram.onnx"
    curl -fL "$OWW_BASE/embedding_model.onnx"  -o "$WW_DIR/embedding_model.onnx"
    echo "[wakeword] Shared models → models/wakeword/"
    echo ""
    echo "  NOTE: You still need a hey_freeman.onnx keyword model."
    echo "  Train one at https://github.com/dscripka/openWakeWord"
    echo "  or use a placeholder: touch $WW_DIR/hey_freeman.onnx"
else
    echo "[wakeword] Already present — skipping."
fi

echo ""
echo "Done. Start Ollama then run:"
echo "  java -Djava.library.path=macos/libs -jar macos/build/libs/macos-macos.jar config.yaml"
