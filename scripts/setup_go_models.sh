#!/bin/bash

# Setup models for Freeman Go
MODELS_DIR="models"
mkdir -p $MODELS_DIR

echo "📥 Downloading Kokoro ONNX model..."
curl -L https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2 -o $MODELS_DIR/kokoro.tar.bz2

echo "📦 Extracting models..."
tar xf $MODELS_DIR/kokoro.tar.bz2 -C $MODELS_DIR --strip-components=1

echo "✅ Models ready in $MODELS_DIR/"
echo ""
echo "You can now start the server with:"
echo "./freeman-go start --models ./models"
