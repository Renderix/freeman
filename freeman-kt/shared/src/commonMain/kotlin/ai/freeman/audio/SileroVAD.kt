package ai.freeman.audio

import ai.onnxruntime.OnnxTensor
import ai.onnxruntime.OrtEnvironment
import ai.onnxruntime.OrtSession
import java.nio.FloatBuffer
import java.nio.LongBuffer

class SileroVAD(modelPath: String) : VAD {
    private val env = OrtEnvironment.getEnvironment()
    private val session: OrtSession = env.createSession(modelPath)
    private var h = FloatArray(2 * 1 * 64)
    private var c = FloatArray(2 * 1 * 64)

    override fun isSpeech(frame: FloatArray): Boolean {
        require(frame.size == AudioFrame.FRAME_SIZE)

        val inputTensor = OnnxTensor.createTensor(env, FloatBuffer.wrap(frame), longArrayOf(1, frame.size.toLong()))
        val srTensor = OnnxTensor.createTensor(env, LongBuffer.wrap(longArrayOf(AudioFrame.SAMPLE_RATE.toLong())), longArrayOf(1))
        val hTensor = OnnxTensor.createTensor(env, FloatBuffer.wrap(h), longArrayOf(2, 1, 64))
        val cTensor = OnnxTensor.createTensor(env, FloatBuffer.wrap(c), longArrayOf(2, 1, 64))

        val inputs = mapOf("input" to inputTensor, "sr" to srTensor, "h" to hTensor, "c" to cTensor)
        val result = session.run(inputs)

        val prob = (result.get("output").get().value as Array<*>)[0] as FloatArray
        h = (result.get("hn").get().value as Array<Array<FloatArray>>).flatMap { it.flatMap { row -> row.toList() } }.toFloatArray()
        c = (result.get("cn").get().value as Array<Array<FloatArray>>).flatMap { it.flatMap { row -> row.toList() } }.toFloatArray()

        result.close()
        listOf(inputTensor, srTensor, hTensor, cTensor).forEach { it.close() }

        return prob[0] > 0.5f
    }

    override fun reset() {
        h = FloatArray(2 * 1 * 64)
        c = FloatArray(2 * 1 * 64)
    }
}
