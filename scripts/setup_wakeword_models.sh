#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODELS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)/models/wakeword"
mkdir -p "$MODELS_DIR"

BASE_URL="https://github.com/dscripka/openWakeWord/releases/download/v0.5.1"

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
echo "=== Keyword Model Copy ==="

TRAINING_DIR="$SCRIPT_DIR/../../openwakeword-training/my_custom_model"
if [ ! -d "$TRAINING_DIR" ]; then
    echo "ERROR: keyword training directory not found at $TRAINING_DIR" >&2
    echo "       Train models in the sibling openwakeword-training repo first:" >&2
    echo "       https://github.com/... (../openwakeword-training)" >&2
    exit 1
fi
TRAINING_DIR="$(cd "$TRAINING_DIR" && pwd)"

for f in horus.onnx standby.onnx disengage.onnx; do
    src="$TRAINING_DIR/$f"
    dst="$MODELS_DIR/$f"
    if [ ! -f "$src" ]; then
        echo "ERROR: missing trained model $src" >&2
        echo "       Train it in ../openwakeword-training first." >&2
        exit 1
    fi
    if [ -f "$dst" ]; then
        echo "  OK: $f (already exists)"
    else
        cp -f "$src" "$dst"
        echo "  OK: $f (copied from openwakeword-training)"
    fi
done

echo ""
echo "=== ONNX Runtime Shared Library ==="

REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LIB_DIR="$REPO_ROOT/lib"
mkdir -p "$LIB_DIR"

ORT_VERSION="1.24.4"
case "$(uname -s)-$(uname -m)" in
    Darwin-arm64)  ORT_PKG="onnxruntime-osx-arm64-${ORT_VERSION}";   LIB_NAME="libonnxruntime.dylib" ;;
    Darwin-x86_64) ORT_PKG="onnxruntime-osx-x86_64-${ORT_VERSION}";  LIB_NAME="libonnxruntime.dylib" ;;
    Linux-x86_64)  ORT_PKG="onnxruntime-linux-x64-${ORT_VERSION}";   LIB_NAME="libonnxruntime.so"    ;;
    Linux-aarch64) ORT_PKG="onnxruntime-linux-aarch64-${ORT_VERSION}"; LIB_NAME="libonnxruntime.so"  ;;
    *) echo "ERROR: unsupported platform $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

if [ -f "$LIB_DIR/$LIB_NAME" ]; then
    echo "  OK: $LIB_NAME (already exists)"
else
    echo "  Downloading ONNX Runtime ${ORT_VERSION}..."
    TMP_DIR="$(mktemp -d)"
    trap "rm -rf $TMP_DIR" EXIT
    curl -fSL "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${ORT_PKG}.tgz" \
        -o "$TMP_DIR/ort.tgz"
    tar -xzf "$TMP_DIR/ort.tgz" -C "$TMP_DIR"
    cp -f "$TMP_DIR/$ORT_PKG/lib/$LIB_NAME" "$LIB_DIR/$LIB_NAME"
    # Also copy versioned dylib so dlopen of install_name works on macOS
    for f in "$TMP_DIR/$ORT_PKG/lib/"libonnxruntime.*.dylib "$TMP_DIR/$ORT_PKG/lib/"libonnxruntime.so.*; do
        [ -f "$f" ] && cp -f "$f" "$LIB_DIR/"
    done
    echo "  OK: $LIB_NAME (downloaded v${ORT_VERSION})"
fi

echo ""
echo "Models ready in $MODELS_DIR/"
echo "ONNX runtime ready in $LIB_DIR/"
