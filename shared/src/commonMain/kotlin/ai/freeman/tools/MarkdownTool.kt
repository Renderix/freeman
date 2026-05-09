package ai.freeman.tools

import ai.freeman.llm.Tool
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

data class MarkdownTool(
    val name: String,
    val description: String,
    val parametersYaml: String,
    val runtime: String,
    val timeoutSeconds: Int,
    val body: String,
) {
    fun toLlmTool(): Tool = Tool(
        name = name,
        description = description,
        parameters = parseParameters(parametersYaml),
    )

    companion object {
        private val FRONTMATTER = Regex("""^---\n(.*?)\n---\n?(.*)$""", RegexOption.DOT_MATCHES_ALL)

        fun parse(markdown: String): MarkdownTool? {
            val match = FRONTMATTER.find(markdown.trim()) ?: return null
            val front = match.groupValues[1]
            val body = match.groupValues[2].trim()

            fun field(key: String): String =
                Regex("""^$key:\s*"?([^"\n]+)"?\s*$""", RegexOption.MULTILINE)
                    .find(front)?.groupValues?.get(1)?.trim() ?: ""

            val paramsBlock = Regex("""parameters:\n((?:  .+\n?)+)""")
                .find(front)?.groupValues?.get(1) ?: ""

            return MarkdownTool(
                name = field("name"),
                description = field("description"),
                parametersYaml = paramsBlock,
                runtime = field("runtime"),
                timeoutSeconds = field("timeout").toIntOrNull() ?: 30,
                body = body,
            )
        }

        private fun parseParameters(yaml: String): JsonObject {
            return if (yaml.contains("type: object") || yaml.isBlank())
                buildJsonObject { put("type", "object") }
            else
                buildJsonObject { put("type", "object") }
        }
    }
}
