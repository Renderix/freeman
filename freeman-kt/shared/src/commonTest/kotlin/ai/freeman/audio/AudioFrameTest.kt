package ai.freeman.audio

import kotlin.test.Test
import kotlin.test.assertEquals

class AudioFrameTest {
    @Test fun frameSizeMatchesSampleRate() {
        assertEquals(32, AudioFrame.FRAME_MS)
    }

    @Test fun sileroFrameSize() {
        assertEquals(512, AudioFrame.FRAME_SIZE)
    }
}
