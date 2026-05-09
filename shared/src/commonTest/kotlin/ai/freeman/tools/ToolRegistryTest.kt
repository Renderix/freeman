package ai.freeman.tools

import ai.freeman.llm.Tool
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

class ToolRegistryTest {
    private val sampleToolMd = """
        ---
        name: get_time
        description: Returns the current time.
        parameters:
          type: object
          properties: {}
        runtime: bash
        timeout: 5
        ---
        date +%H:%M:%S
    """.trimIndent()

    @Test fun parsesToolFromMarkdown() {
        val tool = MarkdownTool.parse(sampleToolMd)
        assertNotNull(tool)
        assertEquals("get_time", tool.name)
        assertEquals("Returns the current time.", tool.description)
        assertEquals("bash", tool.runtime)
    }

    @Test fun convertsToLlmTool() {
        val mdTool = MarkdownTool.parse(sampleToolMd)!!
        val llmTool: Tool = mdTool.toLlmTool()
        assertEquals("get_time", llmTool.name)
    }

    @Test fun registryContainsParsedTool() {
        val registry = ToolRegistry()
        registry.registerFromMarkdown(sampleToolMd)
        assertEquals(1, registry.tools().size)
        assertEquals("get_time", registry.tools().first().name)
    }
}
