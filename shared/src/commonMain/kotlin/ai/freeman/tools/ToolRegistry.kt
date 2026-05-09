package ai.freeman.tools

import ai.freeman.llm.Tool

class ToolRegistry {
    private val mdTools = mutableMapOf<String, MarkdownTool>()

    fun registerFromMarkdown(markdown: String) {
        val tool = MarkdownTool.parse(markdown) ?: return
        mdTools[tool.name] = tool
    }

    fun loadFromDir(dirPath: String) {
        java.io.File(dirPath).walkTopDown()
            .filter { it.extension == "md" }
            .forEach { registerFromMarkdown(it.readText()) }
    }

    fun tools(): List<Tool> = mdTools.values.map { it.toLlmTool() }

    suspend fun dispatch(name: String, args: Map<String, String>, runner: ToolRunner): String {
        val tool = mdTools[name] ?: return """{"error":"unknown tool $name"}"""
        return runner.run(tool, args)
    }
}
