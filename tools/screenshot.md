---
name: screenshot
description: Capture the user's main screen and return the PNG file path. Use this when the user asks you to look at their screen, read something on screen, describe what's visible, or check an app/window state. Returns a local file path.
runtime: shell
timeout_ms: 5000
parameters:
  type: object
  properties: {}
  required: []
---
set -uo pipefail
OUT="${TMPDIR:-/tmp}/freeman-shot-${UUID}.png"
# -x = no shutter sound; -m = main display only.
ERR=$(screencapture -x -m "$OUT" 2>&1 >/dev/null)
if [ ! -s "$OUT" ]; then
  if echo "$ERR" | grep -qi "could not create image"; then
    echo "screenshot_permission_denied: terminal lacks macOS Screen Recording permission. Ask the user to grant it in System Settings > Privacy & Security > Screen Recording for the terminal app that launched freeman." >&2
    exit 1
  fi
  echo "$ERR" >&2
  exit 1
fi
echo "$OUT"
