package ai.freeman.conv

import ai.freeman.config.PersonaConfig
import ai.freeman.llm.Tool
import ai.freeman.memory.Memory

object SystemPrompt {
    fun build(persona: PersonaConfig, tools: List<Tool>, recalled: List<Memory> = emptyList()): String {
        val sb = StringBuilder()
        sb.appendLine("You are ${persona.name}, a voice assistant running fully on-device.")
        sb.appendLine("Reply conversationally and concisely — responses are read aloud via TTS.")
        sb.appendLine("Never use markdown formatting, bullet points, or headers in your replies.")
        sb.appendLine()
        if (persona.rules.isNotEmpty()) {
            sb.appendLine("## Rules")
            persona.rules.forEach { sb.appendLine("- $it") }
            sb.appendLine()
        }
        if (recalled.isNotEmpty()) {
            sb.appendLine("## Relevant past exchanges")
            recalled.forEach { m -> sb.appendLine("${m.role}: ${m.content}") }
            sb.appendLine()
        }
        return sb.toString()
    }
}
