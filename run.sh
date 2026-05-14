#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
# taskpolicy -b: yield CPU to foreground apps automatically on macOS
exec taskpolicy -b java \
  -Djava.library.path="$DIR/macos/libs" \
  -Xms64m -Xmx1500m \
  -jar "$DIR/macos/build/libs/macos-macos.jar" \
  "${1:-$DIR/config.yaml}"
