---
name: clipboard_write
description: Write text to the user's clipboard, replacing its current contents. Use when the user asks you to copy something for them, put something on the clipboard, or save text for pasting.
runtime: shell
timeout_ms: 2000
parameters:
  type: object
  properties:
    text:
      type: string
      description: The exact text to place on the clipboard.
  required: [text]
---
set -euo pipefail
printf '%s' "$ARG_text" | pbcopy
echo "copied ${#ARG_text} chars"
