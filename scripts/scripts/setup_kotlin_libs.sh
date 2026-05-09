#!/usr/bin/env bash
# Downloads sherpa-onnx native bindings for macOS JVM and Android.
# Run once before building: ./scripts/setup_kotlin_libs.sh

set -euo pipefail

SHERPA_VERSION="1.13.1"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$SCRIPT_DIR/.."

MACOS_LIBS="$ROOT/macos/libs"
ANDROID_LIBS="$ROOT/android/libs"
ANDROID_JNI="$ROOT/android/src/main/jniLibs"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

BASE_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/v${SHERPA_VERSION}"

mkdir -p "$MACOS_LIBS" "$ANDROID_LIBS" "$ANDROID_JNI"

# ── Java API JAR (provides com.k2fsa.sherpa.onnx.*) ───────────────────────
if [ ! -f "$MACOS_LIBS/sherpa-onnx.jar" ]; then
    echo "[sherpa-onnx] Downloading Java API JAR…"
    curl -fL "$BASE_URL/sherpa-onnx-v${SHERPA_VERSION}.jar" \
         -o "$MACOS_LIBS/sherpa-onnx.jar"
    echo "[sherpa-onnx] API JAR → macos/libs/sherpa-onnx.jar"
else
    echo "[sherpa-onnx] Java API JAR already present — skipping."
fi

# Same API JAR used for Android compilation
if [ ! -f "$ANDROID_LIBS/sherpa-onnx.jar" ]; then
    cp "$MACOS_LIBS/sherpa-onnx.jar" "$ANDROID_LIBS/sherpa-onnx.jar"
fi

# ── macOS native lib JAR (bundles dylibs, loaded automatically at runtime) ─
ARCH=$(uname -m)
NATIVE_JAR_NAME="sherpa-onnx-native-lib-osx-${ARCH}-v${SHERPA_VERSION}.jar"
# GitHub uses 'aarch64' for Apple Silicon, match that naming
if [ "$ARCH" = "arm64" ]; then
    NATIVE_JAR_NAME="sherpa-onnx-native-lib-osx-aarch64-v${SHERPA_VERSION}.jar"
fi

if [ ! -f "$MACOS_LIBS/$NATIVE_JAR_NAME" ]; then
    echo "[sherpa-onnx] Downloading macOS native lib JAR (${ARCH})…"
    curl -fL "$BASE_URL/$NATIVE_JAR_NAME" -o "$MACOS_LIBS/$NATIVE_JAR_NAME"
    echo "[sherpa-onnx] Native JAR → macos/libs/$NATIVE_JAR_NAME"
else
    echo "[sherpa-onnx] macOS native JAR already present — skipping."
fi

# ── Android native JNI .so files ──────────────────────────────────────────
if [ ! -f "$ANDROID_JNI/arm64-v8a/libsherpa-onnx-jni.so" ]; then
    echo "[sherpa-onnx] Downloading Android JNI .so files…"
    curl -fL "$BASE_URL/sherpa-onnx-v${SHERPA_VERSION}-android.tar.bz2" \
         -o "$TMP/android.tar.bz2"
    tar -xjf "$TMP/android.tar.bz2" -C "$TMP"
    find "$TMP" -name "jniLibs" -type d | head -1 | xargs -I{} cp -r {}/* "$ANDROID_JNI/"
    echo "[sherpa-onnx] Android .so files → android/src/main/jniLibs/"
else
    echo "[sherpa-onnx] Android JNI .so files already present — skipping."
fi

echo ""
echo "Done. Build with: ./gradlew :macos:macosJar"
