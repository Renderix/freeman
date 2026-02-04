# Freeman TTS Usage Guide

## Starting the Server

```bash
./freeman-go start --config config.yaml
```

The server runs on port 17000 by default (configurable in `config.yaml`).

## Web UI

Open your browser to **http://localhost:17000** for the built-in test interface.

Features:
- Voice selection (24 voices: American/British, male/female)
- Speed control (0.5x - 2.0x)
- Real-time latency display
- Audio playback history

## Settings Page

Go to **http://localhost:17000/settings** to configure default voice and speed.

Settings are persisted to `~/.config/freeman/settings.json` and automatically loaded when the UI starts.

You can also access settings via API:
```bash
# Get current settings
curl http://localhost:17000/api/settings

# Update settings
curl -X POST http://localhost:17000/api/settings \
  -H "Content-Type: application/json" \
  -d '{"voice": "af_bella", "speed": 1.2}'
```

## WebSocket API

Connect to `ws://localhost:17000/ws/stream` for programmatic access.

### Protocol

**1. Initialize session:**
```json
{"type": "init", "voice": "af_heart", "speed": 1.0}
```

Response:
```json
{"type": "init_ack", "session_id": "uuid", "voice": "af_heart", "speed": 1.0, "status": "ready"}
```

**2. Send text:**
```json
{"type": "text", "chunk": "Hello world.", "is_final": true}
```

- `chunk`: Text to synthesize
- `is_final`: Set `true` to flush buffer and end stream after this text

Response sequence:
```json
{"type": "text_ack", "buffered_chars": 12, "pending_sentences": 1}
{"type": "sentence_start", "id": 1, "text": "Hello world.", "estimated_duration_sec": 0.96}
```
Then binary WAV audio data, followed by:
```json
{"type": "sentence_complete", "id": 1, "duration_ms": 520, "processing_ms": 85}
{"type": "stream_end", "total_sentences": 1, "total_duration_ms": 520, "reason": "client_final"}
```

**3. Flush buffer (optional):**
```json
{"type": "flush"}
```

Forces any buffered partial text to be synthesized immediately.

**4. End session:**
```json
{"type": "end"}
```

### Streaming Text

For LLM streaming, send chunks as they arrive:

```json
{"type": "init", "voice": "af_heart", "speed": 1.0}
{"type": "text", "chunk": "The quick ", "is_final": false}
{"type": "text", "chunk": "brown fox.", "is_final": false}
{"type": "text", "chunk": " It jumped.", "is_final": true}
```

The server buffers text and synthesizes complete sentences automatically. Sentence boundaries are detected by `.`, `!`, `?` (avoiding false positives like "Dr." or "Mr.").

## Available Voices

| Code | Description |
|------|-------------|
| `af_heart` | American Female - Heart (warm, expressive) |
| `af_alloy` | American Female - Alloy (professional) |
| `af_bella` | American Female - Bella (calm) |
| `af_jessica` | American Female - Jessica (friendly) |
| `af_nova` | American Female - Nova (energetic) |
| `af_sarah` | American Female - Sarah (neutral) |
| `am_adam` | American Male - Adam (natural) |
| `am_echo` | American Male - Echo (deep) |
| `am_michael` | American Male - Michael (authoritative) |
| `bf_emma` | British Female - Emma (warm) |
| `bf_lily` | British Female - Lily (bright) |
| `bm_george` | British Male - George (classic) |
| `bm_lewis` | British Male - Lewis (smooth) |

Full list: `af_heart`, `af_alloy`, `af_aoede`, `af_bella`, `af_jessica`, `af_kore`, `af_nicole`, `af_nova`, `af_river`, `af_sarah`, `af_sky`, `am_adam`, `am_echo`, `am_eric`, `am_fenrir`, `am_liam`, `am_michael`, `am_onyx`, `am_puck`, `bf_alice`, `bf_emma`, `bf_isabella`, `bf_lily`, `bm_daniel`, `bm_fable`, `bm_george`, `bm_lewis`

## Example: Python Client

```python
import asyncio
import websockets
import json

async def speak(text, voice="af_heart", speed=1.0):
    async with websockets.connect("ws://localhost:17000/ws/stream") as ws:
        # Initialize
        await ws.send(json.dumps({"type": "init", "voice": voice, "speed": speed}))
        await ws.recv()  # init_ack

        # Send text
        await ws.send(json.dumps({"type": "text", "chunk": text, "is_final": True}))

        # Receive responses
        audio_data = b""
        while True:
            msg = await ws.recv()
            if isinstance(msg, bytes):
                audio_data += msg
            else:
                data = json.loads(msg)
                if data["type"] == "stream_end":
                    break

        return audio_data  # WAV format

# Usage
audio = asyncio.run(speak("Hello, this is Freeman TTS."))
with open("output.wav", "wb") as f:
    f.write(audio)
```

## Example: curl + websocat

```bash
# Install websocat: brew install websocat
echo '{"type":"init","voice":"af_heart","speed":1.0}
{"type":"text","chunk":"Hello world.","is_final":true}' | websocat ws://localhost:17000/ws/stream
```

## Configuration

Edit `config.yaml`:

```yaml
server:
  port: 17000

model:
  dir: "./models"          # Path to model files
  model_file: "model.onnx"
  voices_file: "voices.bin"
  tokens_file: "tokens.txt"
  data_dir: "espeak-ng-data"

tts:
  default_voice: "af_heart"
  default_speed: 1.0
  max_sentence_chars: 150
  partial_sentence_timeout_sec: 2.0  # Auto-flush incomplete sentences
```

## Metrics

Go to **http://localhost:17000/metrics** to view server metrics including:
- Memory usage (allocated, heap, system)
- Runtime stats (goroutines, CPUs, uptime)
- Active WebSocket sessions

JSON endpoint: `GET /api/metrics`

## Health Check

```bash
curl http://localhost:17000/health
```

Response:
```json
{"status": "healthy", "engine_loaded": true}
```
