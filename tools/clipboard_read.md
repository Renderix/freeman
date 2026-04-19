---
name: clipboard_read
description: Read the current contents of the user's clipboard as text. Use when the user asks what's on their clipboard, to read/summarise what they just copied, or references "this" in a context that implies they copied something.
runtime: shell
timeout_ms: 2000
parameters:
  type: object
  properties: {}
  required: []
---
pbpaste
