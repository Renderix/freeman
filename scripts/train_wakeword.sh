#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODELS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)/models/wakeword"
VENV_DIR="$SCRIPT_DIR/.wakeword-venv"

echo "=== OpenWakeWord Keyword Training ==="
echo ""
echo "This will train custom wake word models for: Horus, Mute, Horus stop"
echo "Training takes ~30-60 minutes per keyword on CPU."
echo ""

if [ ! -d "$VENV_DIR" ]; then
    echo "Creating Python virtual environment..."
    python3 -m venv "$VENV_DIR"
fi

source "$VENV_DIR/bin/activate"
pip install -q openwakeword

echo ""
echo "Training keywords..."

python3 -c "
import openwakeword
from openwakeword.train import train_custom_model
import os

models_dir = '$MODELS_DIR'
keywords = [
    ('horus', 'horus'),
    ('mute', 'mute'),
    ('horus_stop', 'horus stop'),
]

for filename, phrase in keywords:
    out_path = os.path.join(models_dir, filename + '.onnx')
    if os.path.exists(out_path):
        print(f'  SKIP: {filename}.onnx (already exists)')
        continue
    print(f'  Training: \"{phrase}\" -> {filename}.onnx ...')
    train_custom_model(phrase, output_path=out_path)
    print(f'  OK: {filename}.onnx')
"

echo ""
echo "Training complete. Models in $MODELS_DIR/"
echo "Ready to run: ./freeman call"
