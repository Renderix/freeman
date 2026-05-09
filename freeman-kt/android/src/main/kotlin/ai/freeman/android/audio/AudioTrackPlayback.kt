package ai.freeman.android.audio

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack

class AudioTrackPlayback {
    private val track = AudioTrack.Builder()
        .setAudioAttributes(AudioAttributes.Builder()
            .setUsage(AudioAttributes.USAGE_MEDIA)
            .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
            .build())
        .setAudioFormat(AudioFormat.Builder()
            .setEncoding(AudioFormat.ENCODING_PCM_FLOAT)
            .setSampleRate(24000)
            .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
            .build())
        .setBufferSizeInBytes(
            AudioTrack.getMinBufferSize(24000, AudioFormat.CHANNEL_OUT_MONO,
                                         AudioFormat.ENCODING_PCM_FLOAT) * 4
        )
        .setTransferMode(AudioTrack.MODE_STREAM)
        .build()

    init { track.play() }

    fun play(samples: FloatArray) {
        track.write(samples, 0, samples.size, AudioTrack.WRITE_BLOCKING)
    }

    fun stop() { track.stop(); track.release() }
}
