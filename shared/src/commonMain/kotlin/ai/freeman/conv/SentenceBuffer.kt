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
        val lastChar = s.trimEnd().lastOrNull() ?: return

        if (lastChar in SENTENCE_END) {
            onSentence(s)
            buf.clear()
            return
        }

        if (lastChar in CLAUSE_BREAK && s.length >= EARLY_FLUSH_MIN_LEN) {
            onSentence(s)
            buf.clear()
        }
    }
}
