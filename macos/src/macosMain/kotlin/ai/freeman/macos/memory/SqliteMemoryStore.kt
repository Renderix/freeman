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
                    id         INTEGER PRIMARY KEY AUTOINCREMENT,
                    role       TEXT    NOT NULL,
                    content    TEXT    NOT NULL,
                    session_id TEXT    NOT NULL,
                    created_at INTEGER NOT NULL
                )
            """.trimIndent())
            stmt.executeUpdate("""
                CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts
                USING fts5(content, content='memories', content_rowid='id', tokenize='porter unicode61')
            """.trimIndent())
            // Keep FTS index in sync with the base table
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
            "INSERT INTO memories (role, content, session_id, created_at) VALUES (?, ?, ?, ?)"
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
        // BM25 ranking via FTS5 — bm25() returns negative scores, ORDER BY ascending = most relevant first
        val sql = """
            SELECT m.id, m.role, m.content, m.session_id, m.created_at
            FROM memories m
            JOIN memories_fts f ON m.id = f.rowid
            WHERE memories_fts MATCH ?
            ORDER BY bm25(memories_fts)
            LIMIT ?
        """.trimIndent()
        return conn.prepareStatement(sql).use { ps ->
            ps.setString(1, sanitizeFtsQuery(query))
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
                    )
                )
            }
        }
    }

    override fun close() = conn.close()

    // Strip FTS5 special chars to avoid parse errors on raw user input
    private fun sanitizeFtsQuery(q: String): String =
        q.replace(Regex("[\"*^()]"), "").trim().let { clean ->
            if (clean.isBlank()) "" else clean.split(Regex("\\s+")).joinToString(" OR ")
        }
}
