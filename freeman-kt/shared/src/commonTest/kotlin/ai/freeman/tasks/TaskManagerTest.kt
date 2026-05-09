package ai.freeman.tasks

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class TaskManagerTest {
    @Test fun startAndRetrieveTask() {
        val mgr = TaskManager()
        val id = mgr.start("build a calculator")
        val task = mgr.get(id)
        assertNotNull(task)
        assertEquals("build a calculator", task.goal)
    }

    @Test fun multipleTasksRunConcurrently() {
        val mgr = TaskManager()
        mgr.start("task one")
        mgr.start("task two")
        assertEquals(2, mgr.all().size)
    }

    @Test fun cancelRemovesTask() {
        val mgr = TaskManager()
        val id = mgr.start("something")
        mgr.cancel(id)
        assertNull(mgr.get(id))
    }

    @Test fun promptSummaryCoversAllTasks() {
        val mgr = TaskManager()
        mgr.start("task one")
        mgr.start("task two")
        val summary = mgr.promptSummary()
        assert(summary.contains("task one"))
        assert(summary.contains("task two"))
    }

    @Test fun replyToNeedsInputTask() {
        val mgr = TaskManager()
        val id = mgr.start("something")
        mgr.get(id)!!.requestInput("What color?")
        mgr.replyToTask(id, "blue")
        assertEquals(TaskState.Running, mgr.get(id)!!.state)
    }
}
