---
name: file_search
description: Search the user's Mac for files by name using Spotlight. Use when the user asks to find, locate, or look up a file, document, image, or folder on their machine. Returns up to 30 matching paths.
runtime: shell
timeout_ms: 6000
parameters:
  type: object
  properties:
    query:
      type: string
      description: "Name or name fragment to match. Spotlight treats this as a name token."
    limit:
      type: integer
      description: "Max results to return. Default 30."
      default: 30
  required: [query]
---
set -euo pipefail
LIMIT="${ARG_limit:-30}"
mdfind -name "$ARG_query" 2>/dev/null | sed -n "1,${LIMIT}p"
