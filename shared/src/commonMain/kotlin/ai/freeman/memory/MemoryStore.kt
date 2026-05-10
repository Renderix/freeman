package ai.freeman.memory

interface MemoryStore {
    fun save(memory: Memory)
    fun search(query: String, limit: Int = 5): List<Memory>
    fun prune(olderThanDays: Int, minRecallCount: Int = 1)
    fun close()
}
