package ai.freeman.memory

interface MemoryStore {
    fun save(memory: Memory)
    fun search(query: String, limit: Int = 5): List<Memory>
    fun close()
}
