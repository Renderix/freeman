package ai.freeman.tasks

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs

class AgentTaskTest {
    @Test fun initialStateIsRunning() {
        val task = AgentTask(id = "t1", goal = "do something")
        assertIs<TaskState.Running>(task.state)
    }

    @Test fun transitionsToNeedsInput() {
        val task = AgentTask(id = "t1", goal = "do something")
        task.requestInput("What color?")
        assertIs<TaskState.NeedsInput>(task.state)
        assertEquals("What color?", (task.state as TaskState.NeedsInput).question)
    }

    @Test fun transitionsToDone() {
        val task = AgentTask(id = "t1", goal = "do something")
        task.complete("All done.")
        assertIs<TaskState.Done>(task.state)
        assertEquals("All done.", (task.state as TaskState.Done).summary)
    }

    @Test fun transitionsToFailed() {
        val task = AgentTask(id = "t1", goal = "do something")
        task.fail("Something broke")
        assertIs<TaskState.Failed>(task.state)
    }

    @Test fun summarisesForPrompt() {
        val task = AgentTask(id = "t1", goal = "do something")
        task.complete("done")
        val summary = task.promptSummary()
        assert(summary.contains("t1"))
        assert(summary.contains("done"))
    }
}
