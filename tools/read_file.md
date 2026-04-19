---
name: read_file
description: Read a text file from the user's Mac and return its contents. Use this for any "read", "look at", "what's in", or "check" request that references a file path, clipboard-copied path, or a file you discovered via file_search. Prefer this over spawning a background coding task for simple reads — it's much faster.
runtime: shell
timeout_ms: 4000
parameters:
  type: object
  properties:
    path:
      type: string
      description: "Absolute file path. Tilde (~) is expanded."
    max_bytes:
      type: integer
      description: "Truncate to this many bytes to stay within context. Default 20000."
      default: 20000
  required: [path]
---
set -euo pipefail
PATH_EXPANDED="${ARG_path/#\~/$HOME}"
MAX="${ARG_max_bytes:-20000}"
if [ ! -f "$PATH_EXPANDED" ]; then
  echo "not a regular file: $PATH_EXPANDED" >&2
  exit 1
fi
SIZE=$(wc -c < "$PATH_EXPANDED" | tr -d ' ')
head -c "$MAX" "$PATH_EXPANDED"
if [ "$SIZE" -gt "$MAX" ]; then
  echo
  echo "[truncated: file is $SIZE bytes, showing first $MAX]"
fi
