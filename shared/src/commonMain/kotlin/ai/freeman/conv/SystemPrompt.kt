package ai.freeman.conv

import ai.freeman.config.PersonaConfig
import ai.freeman.llm.Tool
import ai.freeman.memory.Memory
import ai.freeman.skills.StoredSkill

object SystemPrompt {
    fun build(
        persona: PersonaConfig,
        tools: List<Tool>,
        recalled: List<Memory> = emptyList(),
        skills: List<StoredSkill> = emptyList(),
        override: String? = null,
    ): String {
        val base = (override
            ?: SystemPrompt::class.java.getResourceAsStream("/system-prompt.md")
                ?.bufferedReader()?.readText()
            ?: "You are ${persona.name}, a voice assistant. Be concise and conversational."
        ).replace("{name}", persona.name)

        val sb = StringBuilder(base).appendLine()
        if (persona.rules.isNotEmpty()) {
            sb.appendLine()
            sb.appendLine("Additional instructions:")
            persona.rules.forEach { sb.appendLine("- $it") }
        }
        if (skills.isNotEmpty()) {
            sb.appendLine()
            sb.appendLine("Active skills for this situation:")
            skills.forEach { sk ->
                sb.appendLine("${sk.name}: ${sk.instructions}")
            }
        }
        if (recalled.isNotEmpty()) {
            sb.appendLine()
            sb.appendLine("Relevant past exchanges:")
            recalled.forEach { m -> sb.appendLine("${m.role}: ${m.content}") }
        }
        return sb.toString()
    }
}
