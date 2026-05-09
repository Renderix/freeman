package ai.freeman

import ai.freeman.tts.VoiceProfile
import ai.freeman.llm.Message
import ai.freeman.llm.Role
import ai.freeman.llm.Tool
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class InterfaceContractTest {

    @Test
    fun voiceProfileDefaultSpeakerId() {
        val profile = VoiceProfile(name = "test")
        assertEquals(0, profile.speakerId)
        assertNull(profile.referenceAudioPath)
    }

    @Test
    fun voiceProfileSerializable() {
        val profile = VoiceProfile(name = "george", speakerId = 2)
        val json = Json.encodeToString(VoiceProfile.serializer(), profile)
        val decoded = Json.decodeFromString(VoiceProfile.serializer(), json)
        assertEquals(profile, decoded)
    }

    @Test
    fun messageRoundTrip() {
        val msg = Message(role = Role.user, content = "hello")
        val json = Json.encodeToString(Message.serializer(), msg)
        val decoded = Json.decodeFromString(Message.serializer(), json)
        assertEquals(msg, decoded)
    }

    @Test
    fun toolParametersPassThrough() {
        val schema = buildJsonObject { put("type", "object") }
        val tool = Tool(name = "test", description = "desc", parameters = schema)
        assertEquals("test", tool.name)
    }
}
