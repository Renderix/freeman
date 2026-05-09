package ai.freeman.android.audio

import ai.freeman.audio.AudioCapture
import ai.freeman.audio.AudioFrame
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.MediaRecorder
import kotlinx.coroutines.*

class AndroidAudioCapture : AudioCapture {
    private var record: AudioRecord? = null
    private var job: Job? = null

    override fun start(onFrame: (FloatArray) -> Unit) {
        val bufferSize = AudioRecord.getMinBufferSize(
            AudioFrame.SAMPLE_RATE,
            AudioFormat.CHANNEL_IN_MONO,
            AudioFormat.ENCODING_PCM_FLOAT,
        ).coerceAtLeast(AudioFrame.FRAME_SIZE * 4)

        record = AudioRecord(
            MediaRecorder.AudioSource.MIC,
            AudioFrame.SAMPLE_RATE,
            AudioFormat.CHANNEL_IN_MONO,
            AudioFormat.ENCODING_PCM_FLOAT,
            bufferSize,
        )
        record!!.startRecording()

        job = CoroutineScope(Dispatchers.IO).launch {
            val buf = FloatArray(AudioFrame.FRAME_SIZE)
            while (isActive) {
                val read = record!!.read(buf, 0, buf.size, AudioRecord.READ_BLOCKING)
                if (read > 0) onFrame(buf.copyOf(read))
            }
        }
    }

    override fun stop() {
        job?.cancel()
        record?.stop()
        record?.release()
        record = null
    }
}
