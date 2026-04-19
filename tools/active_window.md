---
name: active_window
description: Report the name of the frontmost application and its focused window title. Use when the user asks what they're looking at, what app is focused, or which window is in front.
runtime: shell
timeout_ms: 3000
parameters:
  type: object
  properties: {}
  required: []
---
set -euo pipefail
osascript <<'APPLESCRIPT'
tell application "System Events"
  set frontApp to name of first application process whose frontmost is true
  set winTitle to ""
  try
    tell application process frontApp
      set winTitle to name of front window
    end tell
  end try
  return frontApp & " | " & winTitle
end tell
APPLESCRIPT
