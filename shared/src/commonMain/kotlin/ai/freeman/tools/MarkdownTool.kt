package ai.freeman.tools

import ai.freeman.llm.Tool
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonObject

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
            // Parse simple two-level YAML: property names at 2-space indent,
            // their attributes (type, description) at 4-space indent.
            val properties = mutableMapOf<String, Pair<String, String>>() // name → (type, description)
            var currentProp: String? = null
            var currentType = "string"
            var currentDesc = ""
            for (line in yaml.lines()) {
                val prop = Regex("""^  (\w+):\s*$""").find(line)?.groupValues?.get(1)
                if (prop != null) {
                    currentProp?.let { properties[it] = Pair(currentType, currentDesc) }
                    currentProp = prop; currentType = "string"; currentDesc = ""
                    continue
                }
                Regex("""^\s+type:\s*(\S+)""").find(line)?.groupValues?.get(1)?.let { currentType = it }
                Regex("""^\s+description:\s*(.+)""").find(line)?.groupValues?.get(1)?.trim()?.let { currentDesc = it }
            }
            currentProp?.let { properties[it] = Pair(currentType, currentDesc) }

            return buildJsonObject {
                put("type", "object")
                if (properties.isNotEmpty()) {
                    putJsonObject("properties") {
                        properties.forEach { (name, td) ->
                            putJsonObject(name) {
                                put("type", td.first)
                                if (td.second.isNotBlank()) put("description", td.second)
                            }
                        }
                    }
                    put("required", kotlinx.serialization.json.buildJsonArray {
                        properties.keys.forEach { add(kotlinx.serialization.json.JsonPrimitive(it)) }
                    })
                }
            }
        }
    }
}
