package ai.freeman.conv

import kotlin.test.Test
import kotlin.test.assertEquals

class SentenceBufferTest {

    @Test fun flushesOnPeriod() {
        val sentences = mutableListOf<String>()
        val buf = SentenceBuffer { sentences.add(it) }
        buf.append("Hello world")
        buf.append(".")
        buf.append(" Next")
        assertEquals(listOf("Hello world."), sentences)
    }

    @Test fun flushesOnExclamation() {
        val sentences = mutableListOf<String>()
        val buf = SentenceBuffer { sentences.add(it) }
        buf.append("Wow!")
        assertEquals(listOf("Wow!"), sentences)
    }

    @Test fun earlyFlushOnClauseBreakAfter30Chars() {
        val sentences = mutableListOf<String>()
        val buf = SentenceBuffer { sentences.add(it) }
        buf.append("This is a reasonably long clause,")
        assertEquals(listOf("This is a reasonably long clause,"), sentences)
    }

    @Test fun noFlushOnShortClause() {
        val sentences = mutableListOf<String>()
        val buf = SentenceBuffer { sentences.add(it) }
        buf.append("Hi,")
        assertEquals(emptyList(), sentences)
    }

    @Test fun flushRemainingOnClose() {
        val sentences = mutableListOf<String>()
        val buf = SentenceBuffer { sentences.add(it) }
        buf.append("Partial")
        buf.flush()
        assertEquals(listOf("Partial"), sentences)
    }
}
