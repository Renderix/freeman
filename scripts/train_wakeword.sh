#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODELS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)/models/wakeword"
TRAINER_DIR="$SCRIPT_DIR/.openwakeword-trainer"

echo "=== OpenWakeWord Keyword Training ==="
echo ""
echo "This trains custom wake word models using openwakeword-trainer."
echo "Training takes ~30-60 minutes per keyword on CPU."
echo "Requires: Python 3.10+, pip, ~4GB disk space"
echo ""

if [ ! -d "$TRAINER_DIR" ]; then
    echo "Cloning openwakeword-trainer..."
    git clone --depth 1 https://github.com/lgpearson1771/openwakeword-trainer.git "$TRAINER_DIR"
fi

cd "$TRAINER_DIR"

if [ ! -d "venv" ]; then
    echo "Creating virtual environment and installing dependencies..."
    python3 -m venv venv
    source venv/bin/activate
    pip install -q -r requirements.txt
else
    source venv/bin/activate
fi

echo ""
echo "Training keywords... (this takes a while)"
echo ""

for phrase_pair in "horus:horus" "mute:mute" "horus_stop:horus stop"; do
    filename="${phrase_pair%%:*}"
    phrase="${phrase_pair#*:}"
    outfile="$MODELS_DIR/${filename}.onnx"

    if [ -f "$outfile" ]; then
        echo "  SKIP: ${filename}.onnx (already exists)"
        continue
    fi

    echo "  Training: \"$phrase\" -> ${filename}.onnx ..."
    python3 train.py --phrase "$phrase" --output "$outfile" 2>&1 | tail -3
    echo "  OK: ${filename}.onnx"
done

echo ""
echo "Training complete. Models in $MODELS_DIR/"

MISSING=0
for f in horus.onnx mute.onnx horus_stop.onnx; do
    if [ ! -f "$MODELS_DIR/$f" ]; then
        echo "  MISSING: $f"
        MISSING=1
    else
        echo "  OK: $f"
    fi
done

if [ "$MISSING" -eq 1 ]; then
    echo ""
    echo "Some models are missing. Check training output above."
    echo ""
    echo "Alternative: use Google Colab notebook for training:"
    echo "  https://github.com/dscripka/openWakeWord/blob/main/notebooks/automatic_model_training.ipynb"
    exit 1
fi

echo ""
echo "Ready to run: ./freeman call"
