package ai.freeman.android.tools

import ai.freeman.tools.MarkdownTool
import ai.freeman.tools.ToolRunner

class AndroidToolRunner : ToolRunner {
    override suspend fun run(tool: MarkdownTool, args: Map<String, String>): String =
        """{"error":"tool execution not supported on Android in this build"}"""
}
