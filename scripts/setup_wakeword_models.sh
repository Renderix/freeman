#!/usr/bin/env bash
set -euo pipefail

MODELS_DIR="$(cd "$(dirname "$0")/.." && pwd)/models/wakeword"
mkdir -p "$MODELS_DIR"

BASE_URL="https://github.com/dscripka/openWakeWord/raw/refs/heads/main/openwakeword/resources/models"

echo "=== OpenWakeWord Shared Model Setup ==="

for f in melspectrogram.onnx embedding_model.onnx; do
    if [ -f "$MODELS_DIR/$f" ]; then
        echo "  OK: $f (already exists)"
    else
        echo "  Downloading: $f ..."
        curl -fSL "$BASE_URL/$f" -o "$MODELS_DIR/$f"
        echo "  OK: $f"
    fi
done

echo ""
echo "Shared models ready in $MODELS_DIR/"
echo ""
echo "Next: train custom keyword models with ./scripts/train_wakeword.sh"
echo "  or place pre-trained .onnx files (horus.onnx, mute.onnx, horus_stop.onnx) in $MODELS_DIR/"
