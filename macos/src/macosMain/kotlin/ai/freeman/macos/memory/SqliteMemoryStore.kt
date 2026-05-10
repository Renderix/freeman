package ai.freeman.macos.memory

import ai.freeman.memory.Memory
import ai.freeman.memory.MemoryStore
import java.sql.Connection
import java.sql.DriverManager

class SqliteMemoryStore(dbPath: String) : MemoryStore {

    private val conn: Connection = DriverManager.getConnection("jdbc:sqlite:$dbPath")

    init {
        conn.createStatement().use { stmt ->
            stmt.executeUpdate("""
                CREATE TABLE IF NOT EXISTS memories (
                    id           INTEGER PRIMARY KEY AUTOINCREMENT,
                    role         TEXT    NOT NULL,
                    content      TEXT    NOT NULL,
                    session_id   TEXT    NOT NULL,
                    created_at   INTEGER NOT NULL,
                    recall_count INTEGER NOT NULL DEFAULT 0
                )
            """.trimIndent())
            // Add recall_count to existing DBs that predate this column
            runCatching {
                stmt.executeUpdate("ALTER TABLE memories ADD COLUMN recall_count INTEGER NOT NULL DEFAULT 0")
            }
            stmt.executeUpdate("""
                CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts
                USING fts5(content, content='memories', content_rowid='id', tokenize='porter unicode61')
            """.trimIndent())
            stmt.executeUpdate("""
                CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
                    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
                END
            """.trimIndent())
            stmt.executeUpdate("""
                CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
                    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.id, old.content);
                END
            """.trimIndent())
            stmt.executeUpdate("""
                CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
                    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.id, old.content);
                    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
                END
            """.trimIndent())
        }
    }

    override fun save(memory: Memory) {
        conn.prepareStatement(
            "INSERT INTO memories (role, content, session_id, created_at, recall_count) VALUES (?, ?, ?, ?, 0)"
        ).use { ps ->
            ps.setString(1, memory.role)
            ps.setString(2, memory.content)
            ps.setString(3, memory.sessionId)
            ps.setLong(4, memory.createdAt)
            ps.executeUpdate()
        }
    }

    override fun search(query: String, limit: Int): List<Memory> {
        if (query.isBlank()) return emptyList()
        val ftsQuery = sanitizeFtsQuery(query)
        if (ftsQuery.isBlank()) return emptyList()

        val sql = """
            SELECT m.id, m.role, m.content, m.session_id, m.created_at, m.recall_count
            FROM memories m
            JOIN memories_fts f ON m.id = f.rowid
            WHERE memories_fts MATCH ?
            ORDER BY bm25(memories_fts)
            LIMIT ?
        """.trimIndent()

        val results = conn.prepareStatement(sql).use { ps ->
            ps.setString(1, ftsQuery)
            ps.setInt(2, limit)
            val rs = ps.executeQuery()
            buildList {
                while (rs.next()) add(
                    Memory(
                        id = rs.getLong("id"),
                        role = rs.getString("role"),
                        content = rs.getString("content"),
                        sessionId = rs.getString("session_id"),
                        createdAt = rs.getLong("created_at"),
                        recallCount = rs.getInt("recall_count"),
                    )
                )
            }
        }

        if (results.isNotEmpty()) {
            val ids = results.joinToString(",") { it.id.toString() }
            conn.createStatement().use { stmt ->
                stmt.executeUpdate("UPDATE memories SET recall_count = recall_count + 1 WHERE id IN ($ids)")
            }
        }

        return results
    }

    override fun prune(olderThanDays: Int, minRecallCount: Int) {
        val cutoff = System.currentTimeMillis() - olderThanDays.toLong() * 24 * 60 * 60 * 1000
        conn.prepareStatement(
            "DELETE FROM memories WHERE created_at < ? AND recall_count < ?"
        ).use { ps ->
            ps.setLong(1, cutoff)
            ps.setInt(2, minRecallCount)
            val deleted = ps.executeUpdate()
            println("[Memory] Pruned $deleted rows older than $olderThanDays days with recall_count < $minRecallCount")
        }
    }

    override fun close() = conn.close()

    private fun sanitizeFtsQuery(q: String): String =
        q.replace(Regex("[\"*^()]"), "").trim().let { clean ->
            if (clean.isBlank()) "" else clean.split(Regex("\\s+")).joinToString(" OR ")
        }
}
