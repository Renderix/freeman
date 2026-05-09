package ai.freeman.tts

import kotlinx.serialization.Serializable

@Serializable
data class VoiceProfile(
    val name: String,
    val referenceAudioPath: String? = null,   // path to .wav clip for cloning
    val speakerId: Int = 0,                   // used by kokoro built-in voices
)
