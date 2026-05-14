#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"

LOCKFILE="$HOME/.freeman/freeman.pid"
mkdir -p "$(dirname "$LOCKFILE")"

if [ -f "$LOCKFILE" ]; then
    OLD_PID=$(cat "$LOCKFILE")
    if kill -0 "$OLD_PID" 2>/dev/null; then
        echo "Freeman is already running (PID $OLD_PID). Stop it first." >&2
        exit 1
    fi
fi

echo $$ > "$LOCKFILE"
trap 'rm -f "$LOCKFILE"' EXIT INT TERM

# taskpolicy -b: yield CPU to foreground apps on macOS
# exec -a Freeman: show "Freeman" in Activity Monitor instead of "java"
exec taskpolicy -b java -Xdock:name=Freeman \
  -Djava.library.path="$DIR/macos/libs" \
  -Xms64m -Xmx1500m \
  -jar "$DIR/macos/build/libs/macos-macos.jar" \
  "${1:-$DIR/config.yaml}"
