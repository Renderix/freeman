---
name: web_fetch
description: Fetch a URL over HTTP and return the page's readable text content (HTML tags stripped). Use this to follow up on a promising link from web_search when the user wants actual information from a page — e.g., live weather, news article contents, documentation pages. Returns up to ~12 KB of plain text; the page is truncated if larger.
runtime: shell
timeout_ms: 15000
parameters:
  type: object
  properties:
    url:
      type: string
      description: "Full URL including scheme, e.g. https://example.com/path."
    max_bytes:
      type: integer
      description: "Max characters of text to return. Default 12000."
      default: 12000
  required: [url]
---
set -euo pipefail
MAX="${ARG_max_bytes:-12000}"
# Strip tags, decode common entities, collapse whitespace, drop empty lines.
RAW=$(curl -sSL --max-time 12 \
  -A "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15" \
  "$ARG_url")
echo "$RAW" \
  | tr '\n' ' ' \
  | sed -E 's/<script[^>]*>.*<\/script>//gI; s/<style[^>]*>.*<\/style>//gI' \
  | sed -E 's/<[^>]+>/ /g; s/&nbsp;/ /g; s/&amp;/\&/g; s/&lt;/</g; s/&gt;/>/g; s/&quot;/"/g; s/&#39;/'"'"'/g' \
  | tr -s ' \t' ' ' \
  | awk -v max="$MAX" '{ if (length($0) > max) { print substr($0,1,max); print "\n[truncated]"; } else { print $0 } }'
