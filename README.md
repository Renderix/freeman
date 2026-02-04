# Freeman TTS

High-performance, real-time text-to-speech streaming server using Kokoro-82M.

```
LLM Text Stream  →  Freeman  →  Audio Stream
     "Hello"           ↓          🔊 <300ms
```

## Features

- **Real-time streaming**: <300ms latency from complete sentence to audio
- **Sentence-aware buffering**: Automatically detects sentence boundaries
- **26 high-quality voices**: American and British English, male and female
- **WebSocket API**: Bidirectional streaming for LLM integration
- **Apple Silicon optimized**: MPS acceleration on M1/M2/M3 Macs

## Quick Start

### Prerequisites

```bash
# macOS
brew install espeak-ng

# Linux
sudo apt install espeak-ng
```

### Installation

```bash
git clone https://github.com/Renderix/freeman.git
cd freeman
python -m venv venv
source venv/bin/activate
pip install -r requirements.txt
```

### Run

```bash
# Start the TTS server
python -m src.cli start --port 17000

# Or start the configuration UI
python -m src.cli setup --port 8000
```

### Test

```python
import asyncio
import websockets
import json

async def test():
    async with websockets.connect("ws://localhost:17000/ws/stream") as ws:
        await ws.send(json.dumps({
            "type": "text",
            "chunk": "Hello world.",
            "is_final": True
        }))

        async for msg in ws:
            if isinstance(msg, bytes):
                with open("output.wav", "wb") as f:
                    f.write(msg)
                print("Audio saved!")
                break

asyncio.run(test())
```

## Available Voices

| Voice | Description | Voice | Description |
|-------|-------------|-------|-------------|
| `af_heart` | American Female - Heart (default) | `am_adam` | American Male - Adam |
| `af_bella` | American Female - Bella | `am_michael` | American Male - Michael |
| `bf_emma` | British Female - Emma | `bm_george` | British Male - George |

See [USAGE.md](USAGE.md) for the complete voice list.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  Freeman TTS Server                  │
│                                                      │
│  WebSocket  →  Session  →  Sentence  →  TTS Engine  │
│   Server       Manager      Buffer      (Kokoro)    │
│                                          ↓          │
│                                   Audio (24kHz WAV) │
└─────────────────────────────────────────────────────┘
```

## Configuration

Settings are stored in `~/.config/freeman/config.json`:

```json
{
  "voice": "af_heart",
  "speed": 1.0,
  "max_sentence_duration_sec": 10.0,
  "partial_sentence_timeout_sec": 2.0,
  "sample_rate": 24000
}
```

## Performance

| Metric | Target |
|--------|--------|
| First Audio Latency | <300ms |
| Real-Time Factor | 5-10x |
| Max Sentence Length | ~150 chars (~10s audio) |
| Partial Timeout | 2 seconds |

## Standalone Binary

### Download

Pre-built binaries for macOS, Linux, and Windows are available on the [Releases page](https://github.com/Renderix/freeman/releases).

### Build from Source

```bash
# Install build dependencies (already in requirements.txt)
pip install -r requirements.txt

# Build standalone executable
python build.py
```

The binary will be created at `dist/freeman` (or `dist/freeman.exe` on Windows).

## Documentation

- [USAGE.md](USAGE.md) - Detailed usage guide, API reference, client examples

## Requirements

- Python 3.11+
- macOS (ARM64/x86_64), Linux, or Windows
- espeak-ng (for phonemization)
- ~2GB disk space (model + dependencies)

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make your changes
4. Run tests: `python -m pytest tests/`
5. Submit a pull request

## License

Apache 2.0 - See [LICENSE](LICENSE) for details.

## Acknowledgments

- [Kokoro TTS](https://github.com/hexgrad/kokoro) - The TTS model powering Freeman
- [FastAPI](https://fastapi.tiangolo.com/) - WebSocket server framework
