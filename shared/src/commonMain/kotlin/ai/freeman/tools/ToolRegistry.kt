package ai.freeman.tools

import ai.freeman.llm.Tool
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject

class ToolRegistry {
    private val mdTools     = mutableMapOf<String, MarkdownTool>()
    private val storedTools = mutableMapOf<String, StoredTool>()

    fun registerFromMarkdown(markdown: String) {
        val tool = MarkdownTool.parse(markdown) ?: return
        mdTools[tool.name] = tool
    }

    fun loadFromDir(dirPath: String) {
        java.io.File(dirPath).walkTopDown()
            .filter { it.extension == "md" }
            .forEach { registerFromMarkdown(it.readText()) }
    }

    fun register(tool: StoredTool) {
        storedTools[tool.name] = tool
    }

    fun loadFromStore(store: ToolStore) {
        store.loadAll().forEach { register(it) }
    }

    fun tools(): List<Tool> =
        mdTools.values.map { it.toLlmTool() } +
        storedTools.values.map { it.toLlmTool() }

    suspend fun dispatch(name: String, args: Map<String, String>, runner: ToolRunner): String {
        mdTools[name]?.let { return runner.run(it, args) }
        storedTools[name]?.let { stored ->
            val md = MarkdownTool(
                name            = stored.name,
                description     = stored.description,
                parametersYaml  = "",
                runtime         = stored.runtime,
                timeoutSeconds  = stored.timeoutSeconds,
                body            = stored.body,
            )
            return runner.run(md, args)
        }
        return """{"error":"unknown tool $name"}"""
    }
}

private fun StoredTool.toLlmTool(): Tool = Tool(
    name        = name,
    description = description,
    parameters  = runCatching { Json.parseToJsonElement(paramsJson) as JsonObject }
                    .getOrDefault(JsonObject(emptyMap())),
)
