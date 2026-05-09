package ai.freeman.android.stt

import ai.freeman.stt.STT

/** Android skips STT — LiteRT-LM Gemma 4 accepts raw audio directly. */
class NoOpStt : STT {
    override suspend fun transcribe(audio: FloatArray): String = ""
}
