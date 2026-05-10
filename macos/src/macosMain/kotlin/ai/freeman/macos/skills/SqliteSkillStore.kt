package ai.freeman.macos.skills

import ai.freeman.skills.StoredSkill
import ai.freeman.skills.SkillStore
import java.sql.Connection
import java.sql.DriverManager

class SqliteSkillStore(dbPath: String) : SkillStore {

    private val conn: Connection = DriverManager.getConnection("jdbc:sqlite:$dbPath")

    init {
        conn.createStatement().use { stmt ->
            stmt.executeUpdate("""
                CREATE TABLE IF NOT EXISTS skills (
                    id           INTEGER PRIMARY KEY AUTOINCREMENT,
                    name         TEXT UNIQUE NOT NULL,
                    trigger      TEXT NOT NULL,
                    instructions TEXT NOT NULL
                )
            """.trimIndent())
            stmt.executeUpdate("""
                CREATE VIRTUAL TABLE IF NOT EXISTS skills_fts
                USING fts5(trigger, instructions, content='skills', content_rowid='id', tokenize='porter unicode61')
            """.trimIndent())
            stmt.executeUpdate("""
                CREATE TRIGGER IF NOT EXISTS skills_ai AFTER INSERT ON skills BEGIN
                    INSERT INTO skills_fts(rowid, trigger, instructions) VALUES (new.id, new.trigger, new.instructions);
                END
            """.trimIndent())
            stmt.executeUpdate("""
                CREATE TRIGGER IF NOT EXISTS skills_au AFTER UPDATE ON skills BEGIN
                    INSERT INTO skills_fts(skills_fts, rowid, trigger, instructions) VALUES ('delete', old.id, old.trigger, old.instructions);
                    INSERT INTO skills_fts(rowid, trigger, instructions) VALUES (new.id, new.trigger, new.instructions);
                END
            """.trimIndent())
        }
    }

    override fun upsert(skill: StoredSkill) {
        conn.prepareStatement("""
            INSERT INTO skills (name, trigger, instructions)
            VALUES (?, ?, ?)
            ON CONFLICT(name) DO UPDATE SET
                trigger      = excluded.trigger,
                instructions = excluded.instructions
        """.trimIndent()).use { ps ->
            ps.setString(1, skill.name)
            ps.setString(2, skill.trigger)
            ps.setString(3, skill.instructions)
            ps.executeUpdate()
        }
    }

    override fun search(query: String, limit: Int): List<StoredSkill> {
        if (query.isBlank()) return emptyList()
        val ftsQuery = query.replace(Regex("[^\\w\\s]"), "").trim()
            .split(Regex("\\s+")).joinToString(" OR ")
        if (ftsQuery.isBlank()) return emptyList()

        return conn.prepareStatement("""
            SELECT s.name, s.trigger, s.instructions
            FROM skills s
            JOIN skills_fts f ON s.id = f.rowid
            WHERE skills_fts MATCH ?
            ORDER BY bm25(skills_fts)
            LIMIT ?
        """.trimIndent()).use { ps ->
            ps.setString(1, ftsQuery)
            ps.setInt(2, limit)
            val rs = ps.executeQuery()
            buildList {
                while (rs.next()) add(StoredSkill(
                    name         = rs.getString("name"),
                    trigger      = rs.getString("trigger"),
                    instructions = rs.getString("instructions"),
                ))
            }
        }
    }

    override fun close() = conn.close()
}
