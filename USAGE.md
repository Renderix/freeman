# Freeman TTS Usage Guide

Complete documentation for using and extending Freeman TTS.

## Table of Contents

1. [Installation](#installation)
2. [Quick Start](#quick-start)
3. [CLI Commands](#cli-commands)
4. [WebSocket API](#websocket-api)
5. [Client Examples](#client-examples)
6. [Configuration](#configuration)
7. [Voice Reference](#voice-reference)
8. [Error Handling](#error-handling)
9. [Development Guide](#development-guide)
10. [Troubleshooting](#troubleshooting)

---

## Installation

### System Requirements

- Python 3.11+
- macOS (ARM64/x86_64), Linux, or Windows
- 2GB+ RAM
- ~2GB disk space

### Dependencies

**macOS:**
```bash
brew install espeak-ng
```

**Linux (Debian/Ubuntu):**
```bash
sudo apt install espeak-ng
```

**Linux (Fedora/RHEL):**
```bash
sudo dnf install espeak-ng
```

### Python Setup

```bash
# Clone the repository
git clone https://github.com/yourname/freeman.git
cd freeman

# Setup environment and install dependencies
uv sync
```

### Binary Installation (Future)

```bash
# macOS via Homebrew
brew install yourname/tap/freeman

# Manual
curl -L https://github.com/yourname/freeman/releases/latest/download/freeman-darwin-arm64 -o freeman
chmod +x freeman
sudo mv freeman /usr/local/bin/
```

---

## Quick Start

### Start the Server

```bash
uv run python -m src.cli start --port 17000
```

The server will initialize the Kokoro TTS engine (takes a few seconds on first run) and then listen for WebSocket connections.

### Send Text, Receive Audio

```python
import asyncio
import websockets
import json

async def speak():
    async with websockets.connect("ws://localhost:17000/ws/stream") as ws:
        # Initialize session (optional - uses defaults if omitted)
        await ws.send(json.dumps({
            "type": "init",
            "voice": "af_heart",
            "speed": 1.0
        }))

        # Send text
        await ws.send(json.dumps({
            "type": "text",
            "chunk": "Hello! This is Freeman speaking.",
            "is_final": True
        }))

        # Receive audio
        async for message in ws:
            if isinstance(message, bytes):
                with open("output.wav", "wb") as f:
                    f.write(message)
                print("Audio saved to output.wav")

        # End session
        await ws.send(json.dumps({"type": "end"}))

asyncio.run(speak())
```

---

## CLI Commands

### `freeman start`

Start the WebSocket TTS server.

```bash
uv run python -m src.cli start [OPTIONS]
```

**Options:**
| Option | Default | Description |
|--------|---------|-------------|
| `--port` | 17000 | Port to listen on |

**Example:**
```bash
uv run python -m src.cli start --port 8080
```

### `freeman setup`

Start the configuration web UI.

```bash
uv run python -m src.cli setup [OPTIONS]
```

**Options:**
| Option | Default | Description |
|--------|---------|-------------|
| `--port` | 8000 | Port for the UI |

**Example:**
```bash
uv run python -m src.cli setup --port 9000
# Open http://localhost:9000 in browser
```

### `freeman version`

Show version information.

```bash
uv run python -m src.cli version
```

---

## WebSocket API

### Connection

**URL:** `ws://host:port/ws/stream`

**Protocol:** RFC 6455 WebSocket

Each WebSocket connection creates a new TTS session. Sessions are isolated and cleaned up automatically on disconnect.

### Client → Server Messages

#### `init` - Initialize Session

Optional first message to configure voice and speed.

```json
{
  "type": "init",
  "voice": "af_heart",
  "speed": 1.0
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `voice` | string | No | config default | Voice identifier |
| `speed` | float | No | 1.0 | Speed multiplier (0.5-2.0) |

#### `text` - Stream Text

Send text chunks for TTS processing.

```json
{
  "type": "text",
  "chunk": "Hello world. ",
  "is_final": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `chunk` | string | Yes | Text fragment to process |
| `is_final` | boolean | No | `true` to flush buffer immediately |

**Behavior:**
- Text accumulates in a buffer
- Sentence detection runs on each chunk
- Complete sentences are queued for TTS
- `is_final: true` processes any remaining buffer

#### `flush` - Force Buffer Flush

Force process any text currently in the buffer.

```json
{
  "type": "flush"
}
```

#### `end` - End Session

Gracefully terminate the session.

```json
{
  "type": "end"
}
```

### Server → Client Messages

#### `init_ack` - Initialization Acknowledgment

```json
{
  "type": "init_ack",
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "voice": "af_heart",
  "speed": 1.0,
  "status": "ready"
}
```

#### `text_ack` - Text Receipt Acknowledgment

```json
{
  "type": "text_ack",
  "buffered_chars": 45,
  "pending_sentences": 2
}
```

#### `sentence_start` - Generation Started

```json
{
  "type": "sentence_start",
  "id": 1,
  "text": "Hello world.",
  "estimated_duration_sec": 1.5,
  "queue_position": 0
}
```

#### Binary Audio Data

Raw WAV audio bytes sent as binary WebSocket frames.

**Format:**
- Container: RIFF/WAVE
- Sample Rate: 24,000 Hz
- Bit Depth: 16-bit PCM
- Channels: 1 (Mono)

#### `sentence_complete` - Generation Complete

```json
{
  "type": "sentence_complete",
  "id": 1,
  "duration_ms": 1500,
  "processing_ms": 180,
  "queue_remaining": 0
}
```

#### `stream_end` - Session Complete

```json
{
  "type": "stream_end",
  "total_sentences": 5,
  "total_duration_ms": 7500,
  "reason": "client_end"
}
```

**Reasons:** `client_end`, `client_final`, `disconnect`

#### `error` - Error Occurred

```json
{
  "type": "error",
  "sentence_id": 2,
  "message": "TTS generation failed",
  "recoverable": true,
  "code": "TTS_ERROR"
}
```

### Message Sequence Diagram

```
Client                                      Server
  │ ──────────────── init ───────────────▶   │
  │ ◀────────────── init_ack ────────────    │
  │                                          │
  │ ─────────── text: "Hello" ───────────▶   │
  │ ◀─────────── text_ack ───────────────    │
  │                                          │
  │ ────────── text: " world." ──────────▶   │
  │ ◀─────────── text_ack ───────────────    │
  │ ◀──────── sentence_start (id=1) ─────    │
  │ ◀──────────── [WAV DATA] ────────────    │
  │ ◀─────── sentence_complete (id=1) ───    │
  │                                          │
  │ ─────────────── end ─────────────────▶   │
  │ ◀─────────── stream_end ─────────────    │
  │ ═══════════ DISCONNECT ══════════════    │
```

---

## Client Examples

### Python (with playback)

```python
import asyncio
import websockets
import json
import io
import soundfile as sf
import sounddevice as sd

async def stream_and_play(text: str, voice: str = "af_heart"):
    async with websockets.connect("ws://localhost:17000/ws/stream") as ws:
        # Initialize
        await ws.send(json.dumps({
            "type": "init",
            "voice": voice,
            "speed": 1.0
        }))

        # Wait for init_ack
        await ws.recv()

        # Send text
        await ws.send(json.dumps({
            "type": "text",
            "chunk": text,
            "is_final": True
        }))

        # Receive and play audio
        async for message in ws:
            if isinstance(message, bytes):
                # Decode WAV and play
                data, samplerate = sf.read(io.BytesIO(message))
                sd.play(data, samplerate)
                sd.wait()
            else:
                msg = json.loads(message)
                if msg.get("type") == "stream_end":
                    break

        await ws.send(json.dumps({"type": "end"}))

# Usage
asyncio.run(stream_and_play("Hello! How are you today?"))
```

### JavaScript/TypeScript (Browser)

```typescript
class FreemanTTSClient {
  private ws: WebSocket;
  private audioQueue: Blob[] = [];
  private isPlaying = false;
  private audioContext: AudioContext;

  constructor(url: string = 'ws://localhost:17000/ws/stream') {
    this.audioContext = new AudioContext();
    this.ws = new WebSocket(url);
    this.ws.binaryType = 'arraybuffer';
    this.setupHandlers();
  }

  private setupHandlers() {
    this.ws.onopen = () => {
      this.ws.send(JSON.stringify({ type: 'init' }));
    };

    this.ws.onmessage = async (event) => {
      if (event.data instanceof ArrayBuffer) {
        await this.playAudio(event.data);
      } else {
        const msg = JSON.parse(event.data);
        console.log('Message:', msg.type);
      }
    };
  }

  async speak(text: string) {
    this.ws.send(JSON.stringify({
      type: 'text',
      chunk: text,
      is_final: true
    }));
  }

  private async playAudio(buffer: ArrayBuffer) {
    const audioBuffer = await this.audioContext.decodeAudioData(buffer);
    const source = this.audioContext.createBufferSource();
    source.buffer = audioBuffer;
    source.connect(this.audioContext.destination);
    source.start();
  }

  end() {
    this.ws.send(JSON.stringify({ type: 'end' }));
    this.ws.close();
  }
}

// Usage
const client = new FreemanTTSClient();
client.speak("Hello from the browser!");
```

### Node.js

```javascript
const WebSocket = require('ws');
const fs = require('fs');

const ws = new WebSocket('ws://localhost:17000/ws/stream');

ws.on('open', () => {
  ws.send(JSON.stringify({
    type: 'text',
    chunk: 'Hello from Node.js!',
    is_final: true
  }));
});

ws.on('message', (data, isBinary) => {
  if (isBinary) {
    fs.writeFileSync('output.wav', data);
    console.log('Audio saved!');
    ws.send(JSON.stringify({ type: 'end' }));
  } else {
    console.log('Message:', JSON.parse(data.toString()));
  }
});
```

### cURL (health check)

```bash
curl http://localhost:17000/health
# {"status":"healthy","engine_loaded":true}
```

---

## Configuration

### Config File Location

```
~/.config/freeman/config.json
```

### Default Configuration

```json
{
  "voice": "af_heart",
  "speed": 1.0,
  "max_sentence_duration_sec": 10.0,
  "partial_sentence_timeout_sec": 2.0,
  "sample_rate": 24000
}
```

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `voice` | string | `af_heart` | Default voice for new sessions |
| `speed` | float | `1.0` | Default speed (0.5 to 2.0) |
| `max_sentence_duration_sec` | float | `10.0` | Max sentence length before auto-split |
| `partial_sentence_timeout_sec` | float | `2.0` | Timeout for incomplete sentences |
| `sample_rate` | int | `24000` | Audio sample rate (Hz) |

### Configuration Precedence

1. Client `init` message (highest priority)
2. Config file
3. Built-in defaults (lowest priority)

---

## Voice Reference

### American English - Female

| ID | Description |
|----|-------------|
| `af_heart` | Warm, expressive (default) |
| `af_alloy` | Professional |
| `af_aoede` | Soft |
| `af_bella` | Calm |
| `af_jessica` | Friendly |
| `af_kore` | Young |
| `af_nicole` | Mature |
| `af_nova` | Energetic |
| `af_river` | Smooth |
| `af_sarah` | Neutral |
| `af_sky` | Airy |

### American English - Male

| ID | Description |
|----|-------------|
| `am_adam` | Natural |
| `am_echo` | Deep |
| `am_eric` | Clear |
| `am_fenrir` | Strong |
| `am_liam` | Friendly |
| `am_michael` | Authoritative |
| `am_onyx` | Deep |
| `am_puck` | Youthful |

### British English - Female

| ID | Description |
|----|-------------|
| `bf_alice` | Refined |
| `bf_emma` | Warm |
| `bf_isabella` | Soft |
| `bf_lily` | Bright |

### British English - Male

| ID | Description |
|----|-------------|
| `bm_daniel` | Clear |
| `bm_fable` | Storyteller |
| `bm_george` | Classic |
| `bm_lewis` | Smooth |

---

## Error Handling

### Error Codes

| Code | Recoverable | Description | Client Action |
|------|-------------|-------------|---------------|
| `INVALID_VOICE` | No | Voice ID not recognized | Re-init with valid voice |
| `TTS_ERROR` | Yes | Audio generation failed | Retry or skip sentence |
| `BUFFER_OVERFLOW` | Yes | Too much pending text | Reduce send rate |
| `TIMEOUT` | No | Session idle too long | Reconnect |
| `FATAL` | No | Unrecoverable error | Reconnect |

### Error Response Format

```json
{
  "type": "error",
  "sentence_id": 2,
  "message": "Human-readable error description",
  "recoverable": true,
  "code": "TTS_ERROR"
}
```

### Handling Errors

```python
async for message in ws:
    if isinstance(message, bytes):
        # Handle audio
        pass
    else:
        msg = json.loads(message)
        if msg.get("type") == "error":
            if msg.get("recoverable"):
                print(f"Warning: {msg['message']}")
                # Continue processing
            else:
                print(f"Fatal error: {msg['message']}")
                break
```

---

## Development Guide

### Project Structure

```
freeman/
├── src/
│   ├── __init__.py        # Package info
│   ├── cli.py             # CLI entry point (Click)
│   ├── config.py          # Configuration management
│   ├── server.py          # WebSocket server (FastAPI)
│   ├── session.py         # Session management
│   ├── sentence_buffer.py # Text buffering & detection
│   └── tts_engine.py      # Kokoro TTS wrapper
├── static/
│   └── setup.html         # Configuration UI
├── requirements.txt       # Python dependencies
├── SPECIFICATION.md       # Technical specification
├── USAGE.md               # This file
└── README.md              # Quick start
```

### Adding a New Feature

#### 1. New Message Type

To add a new client→server message:

1. Edit `src/server.py`, add handler in the message loop:

```python
elif msg_type == "my_new_message":
    # Handle your message
    param = data.get("param")
    await current_session.handle_my_feature(param)
```

2. Add the session method in `src/session.py`:

```python
async def handle_my_feature(self, param: str):
    """Handle my new feature."""
    # Implementation
    await self.send_json({
        "type": "my_new_message_ack",
        "status": "success"
    })
```

#### 2. New Voice

Voices are defined in `src/tts_engine.py`:

```python
VOICES = {
    "my_new_voice": "Description",
    # ...
}
```

The voice ID must be supported by the Kokoro model.

#### 3. New Configuration Option

1. Add default in `src/config.py`:

```python
DEFAULT_CONFIG = {
    "my_new_option": "default_value",
    # ...
}
```

2. Use it where needed:

```python
from .config import config
value = config.get("my_new_option")
```

### Running Tests

```bash
# All tests
uv run pytest

# Specific test
uv run pytest tests/test_sentence_buffer.py -v

# With coverage
uv run pytest --cov=src tests/
```

### Code Style

- Follow PEP 8
- Use type hints
- Keep functions small and focused
- Document public APIs

### Building Binary

```bash
uv run python build.py
```

---

## Troubleshooting

### Server won't start

**Error:** `espeak-ng not found`

**Solution:**
```bash
# macOS
brew install espeak-ng

# Linux
sudo apt install espeak-ng
```

### No audio output

**Check:** Is the TTS engine loaded?
```bash
curl http://localhost:17000/health
# Should return: {"status":"healthy","engine_loaded":true}
```

**Check:** Are you receiving binary data?
```python
async for message in ws:
    print(f"Message type: {type(message)}")
    if isinstance(message, bytes):
        print(f"Audio size: {len(message)} bytes")
```

### High latency

**Possible causes:**
1. First request includes model loading (~5s)
2. Long sentences take longer to generate
3. CPU-only mode (no MPS) is slower

**Solutions:**
- Wait for warmup to complete before sending text
- Keep sentences under 150 characters
- Use Apple Silicon Mac for MPS acceleration

### WebSocket connection fails

**Check server is running:**
```bash
lsof -i :17000
```

**Check firewall:**
```bash
# macOS
sudo pfctl -s rules | grep 17000
```

### Buffer overflow errors

You're sending text faster than it can be processed.

**Solutions:**
1. Wait for `text_ack` before sending more
2. Send smaller chunks
3. Use `is_final: true` less frequently

---

## Performance Tips

1. **Batch sentences**: Send complete sentences when possible
2. **Use appropriate voices**: Some voices are faster than others
3. **Adjust speed**: Higher speed = faster generation (up to 2.0x)
4. **Keep connections alive**: Reuse WebSocket connections
5. **Buffer client-side**: Queue audio to avoid playback gaps

---

## API Endpoints

### WebSocket

| Endpoint | Description |
|----------|-------------|
| `ws://host:port/ws/stream` | Main TTS streaming endpoint |

### HTTP

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |

---

## Support

- **Issues**: [GitHub Issues](https://github.com/yourname/freeman/issues)
- **License**: Apache 2.0
