#!/usr/bin/env bash
set -euo pipefail

MODELS_DIR="$(cd "$(dirname "$0")/.." && pwd)/models/wakeword"
mkdir -p "$MODELS_DIR"

echo "=== Porcupine Wake Word Model Setup ==="
echo ""
echo "Freeman requires custom .ppn keyword files from Picovoice Console."
echo ""
echo "Steps:"
echo "  1. Sign up at https://console.picovoice.ai/"
echo "  2. Create three custom keywords:"
echo "     - \"Horus\"       (save as horus.ppn)"
echo "     - \"Mute\"        (save as mute.ppn)"
echo "     - \"Horus stop\"  (save as horus-stop.ppn)"
echo "  3. Download each .ppn file for your platform (macOS / Linux)"
echo "  4. Place them in: $MODELS_DIR/"
echo ""
echo "  5. Set your Picovoice access key:"
echo "     export PICOVOICE_ACCESS_KEY=your-key-here"
echo ""

MISSING=0
for f in horus.ppn mute.ppn horus-stop.ppn; do
    if [ ! -f "$MODELS_DIR/$f" ]; then
        echo "  MISSING: $MODELS_DIR/$f"
        MISSING=1
    else
        echo "  OK: $MODELS_DIR/$f"
    fi
done

if [ "$MISSING" -eq 1 ]; then
    echo ""
    echo "Some keyword files are missing. See instructions above."
    exit 1
fi

echo ""
echo "All keyword files present. Ready to run: ./freeman call"
