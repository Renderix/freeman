#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG="${1:-$DIR/config.yaml}"

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

# Build a renamed JVM launcher so Activity Monitor shows "Freeman" instead of "java".
LAUNCHER="$DIR/.launcher/Freeman"
if [ ! -f "$LAUNCHER" ]; then
    mkdir -p "$DIR/.launcher"
    JAVA_HOME_DIR=$(java -XshowSettings:all -version 2>&1 | awk '/java.home/{print $3}')
    cp "$JAVA_HOME_DIR/bin/java" "$LAUNCHER"
    install_name_tool -add_rpath "$JAVA_HOME_DIR/lib" "$LAUNCHER" 2>/dev/null || true
    codesign --force --sign - "$LAUNCHER"
fi

# taskpolicy -b: yield CPU to foreground apps on macOS
exec taskpolicy -b "$LAUNCHER" \
  -Djava.library.path="$DIR/libs" \
  -Xms64m -Xmx1500m \
  -jar "$DIR/freeman.jar" "$CONFIG"
