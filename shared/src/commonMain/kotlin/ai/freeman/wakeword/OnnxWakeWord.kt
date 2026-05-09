package ai.freeman.wakeword

import ai.freeman.audio.AudioFrame
import ai.onnxruntime.OnnxTensor
import ai.onnxruntime.OrtEnvironment
import ai.onnxruntime.OrtSession
import java.nio.FloatBuffer

class OnnxWakeWord(
    melPath: String,
    embeddingPath: String,
    keywordPath: String,
    private val threshold: Float = 0.5f,
) : WakeWord {
    private val env = OrtEnvironment.getEnvironment()
    private val melSession: OrtSession = env.createSession(melPath)
    private val embSession: OrtSession = env.createSession(embeddingPath)
    private val kwSession: OrtSession = env.createSession(keywordPath)

    private var callback: (() -> Unit)? = null
    private val melBuffer = ArrayDeque<FloatArray>(16)

    fun processFrame(frame: FloatArray): Boolean {
        val mel = computeMel(frame)
        melBuffer.addLast(mel)
        if (melBuffer.size > 16) melBuffer.removeFirst()
        if (melBuffer.size < 16) return false
        val embedding = computeEmbedding(melBuffer.toList())
        val score = computeKeyword(embedding)
        if (score > threshold) { callback?.invoke(); return true }
        return false
    }

    override fun start(onDetected: () -> Unit) { callback = onDetected }
    override fun stop() { callback = null }

    private fun computeMel(frame: FloatArray): FloatArray {
        val t = OnnxTensor.createTensor(env, FloatBuffer.wrap(frame), longArrayOf(1, frame.size.toLong()))
        val r = melSession.run(mapOf("input" to t))
        val mel = (r.get("output").get().value as Array<FloatArray>)[0]
        r.close(); t.close()
        return mel
    }

    private fun computeEmbedding(frames: List<FloatArray>): FloatArray {
        val flat = frames.flatMap { it.toList() }.toFloatArray()
        val t = OnnxTensor.createTensor(env, FloatBuffer.wrap(flat),
            longArrayOf(1, frames.size.toLong(), frames[0].size.toLong()))
        val r = embSession.run(mapOf("input" to t))
        val emb = (r.get("output").get().value as Array<FloatArray>)[0]
        r.close(); t.close()
        return emb
    }

    private fun computeKeyword(embedding: FloatArray): Float {
        val t = OnnxTensor.createTensor(env, FloatBuffer.wrap(embedding),
            longArrayOf(1, 1, embedding.size.toLong()))
        val r = kwSession.run(mapOf("input" to t))
        val score = ((r.get("output").get().value as Array<Array<FloatArray>>)[0][0][0])
        r.close(); t.close()
        return score
    }
}
