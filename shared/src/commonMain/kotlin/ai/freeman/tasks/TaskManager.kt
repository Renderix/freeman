package ai.freeman.tasks

import java.util.concurrent.ConcurrentHashMap

class TaskManager {
    private val tasks = ConcurrentHashMap<String, AgentTask>()
    private var counter = 0

    fun start(goal: String): String {
        val id = "task-${++counter}"
        tasks[id] = AgentTask(id = id, goal = goal)
        return id
    }

    fun get(id: String): AgentTask? = tasks[id]

    fun all(): List<AgentTask> = tasks.values.toList()

    fun cancel(id: String) { tasks.remove(id) }

    fun replyToTask(id: String, answer: String) {
        val task = tasks[id] ?: return
        if (task.state is TaskState.NeedsInput) {
            tasks[id] = AgentTask(id = id, goal = task.goal)
        }
    }

    fun promptSummary(): String {
        if (tasks.isEmpty()) return "no background tasks"
        return tasks.values.joinToString("\n") { it.promptSummary() }
    }
}
