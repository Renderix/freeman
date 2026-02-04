import asyncio
import json
import uuid
from typing import Dict, Optional

from fastapi import FastAPI, WebSocket, WebSocketDisconnect

from .config import config
from .tts_engine import TTSEngine
from .session import Session, ErrorCode

app = FastAPI(title="Freeman TTS Server")

# Global TTS engine
tts_engine: Optional[TTSEngine] = None
# Active sessions
sessions: Dict[str, Session] = {}

# Validation constants
MIN_SPEED = 0.5
MAX_SPEED = 2.0

@app.on_event("startup")
async def startup_event():
    global tts_engine
    print("🚀 Initializing Kokoro TTS engine...")
    tts_engine = TTSEngine()
    print("✅ Freeman engine ready!")

@app.get("/health")
async def health():
    return {"status": "healthy", "engine_loaded": tts_engine is not None}


def _create_session(
    session_id: str,
    voice: str,
    speed: float,
    websocket: WebSocket
) -> Session:
    """Helper to create a Session instance (DRY)."""
    return Session(
        session_id=session_id,
        voice=voice,
        speed=speed,
        tts_engine=tts_engine,
        send_audio=websocket.send_bytes,
        send_json=websocket.send_json
    )


def _validate_voice(voice: str) -> bool:
    """Check if voice is in available voices."""
    return voice in TTSEngine.VOICES


def _validate_speed(speed: float) -> bool:
    """Check if speed is within allowed range."""
    return MIN_SPEED <= speed <= MAX_SPEED

@app.websocket("/ws/stream")
async def websocket_stream(websocket: WebSocket):
    await websocket.accept()

    session_id = str(uuid.uuid4())
    current_session: Optional[Session] = None
    timeout_task: Optional[asyncio.Task] = None

    async def timeout_checker():
        """Background task to check for partial sentence timeouts."""
        while current_session and current_session.is_active:
            await asyncio.sleep(0.5)
            if current_session:
                await current_session.check_timeout()

    try:
        while True:
            message = await websocket.receive_text()
            data = json.loads(message)
            msg_type = data.get("type")

            if msg_type == "init":
                voice = data.get("voice", config.get("voice"))
                speed = data.get("speed", config.get("speed"))

                # Validate voice
                if not _validate_voice(voice):
                    await websocket.send_json({
                        "type": "error",
                        "message": f"Invalid voice: {voice}",
                        "recoverable": False,
                        "code": ErrorCode.INVALID_VOICE
                    })
                    break

                # Validate speed
                if not _validate_speed(speed):
                    await websocket.send_json({
                        "type": "error",
                        "message": f"Speed must be between {MIN_SPEED} and {MAX_SPEED}",
                        "recoverable": False,
                        "code": ErrorCode.INVALID_VOICE
                    })
                    break

                current_session = _create_session(session_id, voice, speed, websocket)
                sessions[session_id] = current_session

                # Start timeout checker
                timeout_task = asyncio.create_task(timeout_checker())

                await websocket.send_json({
                    "type": "init_ack",
                    "session_id": session_id,
                    "voice": voice,
                    "speed": speed,
                    "status": "ready"
                })

            elif msg_type == "text":
                if not current_session:
                    # Auto-init if client forgot
                    current_session = _create_session(
                        session_id,
                        config.get("voice"),
                        config.get("speed"),
                        websocket
                    )
                    sessions[session_id] = current_session
                    timeout_task = asyncio.create_task(timeout_checker())

                chunk = data.get("chunk", "")
                is_final = data.get("is_final", False)

                # Check buffer overflow
                if current_session.buffer.is_overflow(chunk):
                    await websocket.send_json({
                        "type": "error",
                        "message": "Buffer overflow - too much pending text",
                        "recoverable": True,
                        "code": ErrorCode.BUFFER_OVERFLOW
                    })
                    continue

                pending = await current_session.handle_text(chunk, is_final)

                await websocket.send_json({
                    "type": "text_ack",
                    "buffered_chars": len(current_session.buffer.buffer),
                    "pending_sentences": pending
                })

                # Note: We don't break on is_final anymore - session persists until "end"
                if is_final:
                    await current_session.send_stream_end("client_final")

            elif msg_type == "flush":
                if current_session:
                    await current_session.flush()

            elif msg_type == "end":
                if current_session:
                    await current_session.flush()
                    await current_session.send_stream_end("client_end")
                break

    except WebSocketDisconnect:
        print(f"Session {session_id} disconnected")
        if current_session:
            try:
                await current_session.send_stream_end("disconnect")
            except Exception:
                pass
    except Exception as e:
        print(f"WebSocket error in session {session_id}: {e}")
        try:
            await websocket.send_json({
                "type": "error",
                "message": str(e),
                "recoverable": False,
                "code": ErrorCode.FATAL
            })
        except Exception:
            pass
    finally:
        if timeout_task:
            timeout_task.cancel()
            try:
                await timeout_task
            except asyncio.CancelledError:
                pass
        if current_session:
            current_session.is_active = False
        if session_id in sessions:
            del sessions[session_id]

def start_server(port: int = 17000):
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=port)
