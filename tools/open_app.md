---
name: open_app
description: Launch or bring a macOS application to the foreground by name, optionally navigating to a URL or opening a file path. Use when the user asks to open, launch, or switch to an app, or asks to open a link in a specific browser (e.g., "open this URL in Brave"). If url is provided, the app is directed to open that URL.
runtime: shell
timeout_ms: 4000
parameters:
  type: object
  properties:
    app:
      type: string
      description: "macOS application name, e.g. 'Safari', 'Google Chrome', 'Brave Browser', 'Slack'."
    url:
      type: string
      description: "Optional URL or file path to open inside the app. Omit to just launch the app."
  required: [app]
---
set -euo pipefail
if [ -n "${ARG_url:-}" ]; then
  open -a "$ARG_app" "$ARG_url"
  echo "opened $ARG_app -> $ARG_url"
else
  open -a "$ARG_app"
  echo "opened $ARG_app"
fi
