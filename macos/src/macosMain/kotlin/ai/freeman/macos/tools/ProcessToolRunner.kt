package ai.freeman.macos.tools

import ai.freeman.tools.MarkdownTool
import ai.freeman.tools.ToolRunner
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.util.concurrent.TimeUnit

class ProcessToolRunner : ToolRunner {
    override suspend fun run(tool: MarkdownTool, args: Map<String, String>): String =
        withContext(Dispatchers.IO) {
            withTimeoutOrNull(tool.timeoutSeconds * 1000L) {
                val process = ProcessBuilder("bash", "-c", tool.body)
                    .apply {
                        environment().putAll(args.mapKeys { "ARG_${it.key.uppercase()}" })
                        redirectErrorStream(true)
                    }
                    .start()
                process.waitFor(tool.timeoutSeconds.toLong(), TimeUnit.SECONDS)
                process.inputStream.bufferedReader().readText().trim()
            } ?: """{"error":"tool ${tool.name} timed out after ${tool.timeoutSeconds}s"}"""
        }
}
