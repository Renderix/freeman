package ai.freeman.tasks

sealed class TaskState {
    data object Running : TaskState()
    data class NeedsInput(val question: String) : TaskState()
    data class Done(val summary: String) : TaskState()
    data class Failed(val message: String) : TaskState()
}
