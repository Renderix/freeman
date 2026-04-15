#!/usr/bin/env bash
set -euo pipefail

MODEL_DIR="${MODEL_DIR:-./models/whisper}"
MODEL_NAME="${MODEL_NAME:-ggml-large-v3-turbo.bin}"
URL="${URL:-https://huggingface.co/ggerganov/whisper.cpp/resolve/main/${MODEL_NAME}}"

mkdir -p "${MODEL_DIR}"
OUT="${MODEL_DIR}/${MODEL_NAME}"

if [[ -f "${OUT}" ]]; then
	echo "whisper model already at ${OUT}"
	exit 0
fi

echo "downloading ${MODEL_NAME} (~1.5 GB) to ${OUT}…"
if command -v curl >/dev/null 2>&1; then
	curl -L --fail --progress-bar "${URL}" -o "${OUT}.part"
else
	wget --progress=bar:force -O "${OUT}.part" "${URL}"
fi
mv "${OUT}.part" "${OUT}"
echo "done: ${OUT}"
