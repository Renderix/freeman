package ai.freeman.tools

interface ToolStore {
    fun upsert(tool: StoredTool)
    fun loadAll(): List<StoredTool>
    fun close()
}

data class StoredTool(
    val name: String,
    val description: String,
    val paramsJson: String = """{"type":"object"}""",
    val body: String,
    val runtime: String = "bash",
    val timeoutSeconds: Int = 30,
)
