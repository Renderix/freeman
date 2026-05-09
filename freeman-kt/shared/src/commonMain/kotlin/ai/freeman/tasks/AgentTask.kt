package ai.freeman.tasks

class AgentTask(val id: String, val goal: String) {
    var state: TaskState = TaskState.Running
        private set

    fun requestInput(question: String) { state = TaskState.NeedsInput(question) }
    fun complete(summary: String)      { state = TaskState.Done(summary) }
    fun fail(message: String)          { state = TaskState.Failed(message) }

    fun promptSummary(): String = when (val s = state) {
        is TaskState.Running    -> "[$id] running: $goal"
        is TaskState.NeedsInput -> "[$id] waiting for answer: ${s.question}"
        is TaskState.Done       -> "[$id] done: ${s.summary}"
        is TaskState.Failed     -> "[$id] failed: ${s.message}"
    }
}
