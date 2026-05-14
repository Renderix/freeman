#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG="${1:-$DIR/config.yaml}"
# taskpolicy -b: yield CPU to foreground apps automatically on macOS
exec taskpolicy -b java \
  -Djava.library.path="$DIR/libs" \
  -Xms64m -Xmx1500m \
  -jar "$DIR/freeman.jar" "$CONFIG"
