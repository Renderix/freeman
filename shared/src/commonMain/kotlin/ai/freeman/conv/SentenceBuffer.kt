package ai.freeman.conv

private val SENTENCE_END = setOf('.', '!', '?')
private val CLAUSE_BREAK = setOf(',', ';', ':')
private const val EARLY_FLUSH_MIN_LEN = 30

class SentenceBuffer(private val onSentence: (String) -> Unit) {
    private val buf = StringBuilder()

    fun append(text: String) {
        buf.append(text)
        tryFlush()
    }

    fun flush() {
        if (buf.isNotBlank()) {
            onSentence(buf.toString())
            buf.clear()
        }
    }

    private fun tryFlush() {
        val s = buf.toString()

        // Flush up to the first sentence-end found anywhere in the buffer,
        // then recurse to catch additional complete sentences in the remainder.
        val sentEnd = s.indexOfFirst { it in SENTENCE_END }
        if (sentEnd >= 0) {
            val chunk = s.substring(0, sentEnd + 1)
            if (chunk.isNotBlank()) onSentence(chunk)
            buf.clear()
            buf.append(s.substring(sentEnd + 1))
            tryFlush()
            return
        }

        // No sentence-end yet — flush on a clause break if buffer is long enough.
        val lastChar = s.trimEnd().lastOrNull() ?: return
        if (lastChar in CLAUSE_BREAK && s.length >= EARLY_FLUSH_MIN_LEN) {
            onSentence(s)
            buf.clear()
        }
    }
}
