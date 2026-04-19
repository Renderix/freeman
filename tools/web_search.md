---
name: web_search
description: Search the web via DuckDuckGo and return the top result snippets as plain text. Use when the user asks a question requiring up-to-date information, facts you're uncertain about, news, product details, documentation references, or anything that needs a fresh lookup.
runtime: shell
timeout_ms: 10000
parameters:
  type: object
  properties:
    query:
      type: string
      description: The search query in natural language.
  required: [query]
---
set -euo pipefail
# DuckDuckGo's html endpoint returns scraper-friendly markup. We
# deliberately avoid any API-keyed service so this tool stays
# vendor-independent. After stripping tags we drop everything before the
# first real result URL to skip the region-selector chrome.
curl -sSL -A "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15" \
  -G "https://html.duckduckgo.com/html/" \
  --data-urlencode "q=$ARG_query" \
  | sed -E 's/<[^>]+>/ /g; s/&nbsp;/ /g; s/&amp;/\&/g; s/&lt;/</g; s/&gt;/>/g; s/&quot;/"/g; s/&#39;/'"'"'/g' \
  | tr -s ' \t' ' ' \
  | awk 'NF && length($0) > 3' \
  | sed -n '1,120p'
