package ai.freeman.tools

interface ToolRunner {
    suspend fun run(tool: MarkdownTool, args: Map<String, String>): String
}
