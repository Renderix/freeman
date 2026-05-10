package ai.freeman.skills

interface SkillStore {
    fun upsert(skill: StoredSkill)
    fun search(query: String, limit: Int = 3): List<StoredSkill>
    fun close()
}

data class StoredSkill(
    val name: String,
    val trigger: String,      // when to apply this skill
    val instructions: String, // what to do
)
