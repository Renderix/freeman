package ai.freeman.android.memory

import ai.freeman.memory.Memory
import ai.freeman.memory.MemoryStore
import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper

class SqliteMemoryStore(context: Context) : MemoryStore {

    private val db: SQLiteDatabase = MemoryDbHelper(context).writableDatabase

    override fun save(memory: Memory) {
        db.insert("memories", null, ContentValues().apply {
            put("role", memory.role)
            put("content", memory.content)
            put("session_id", memory.sessionId)
            put("created_at", memory.createdAt)
            put("recall_count", 0)
        })
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

        val results = db.rawQuery(sql, arrayOf(ftsQuery, limit.toString())).use { cursor ->
            buildList {
                while (cursor.moveToNext()) add(
                    Memory(
                        id = cursor.getLong(0),
                        role = cursor.getString(1),
                        content = cursor.getString(2),
                        sessionId = cursor.getString(3),
                        createdAt = cursor.getLong(4),
                        recallCount = cursor.getInt(5),
                    )
                )
            }
        }

        if (results.isNotEmpty()) {
            val ids = results.joinToString(",") { it.id.toString() }
            db.execSQL("UPDATE memories SET recall_count = recall_count + 1 WHERE id IN ($ids)")
        }

        return results
    }

    override fun prune(olderThanDays: Int, minRecallCount: Int) {
        val cutoff = System.currentTimeMillis() - olderThanDays.toLong() * 24 * 60 * 60 * 1000
        db.execSQL(
            "DELETE FROM memories WHERE created_at < ? AND recall_count < ?",
            arrayOf(cutoff, minRecallCount)
        )
    }

    override fun close() = db.close()

    private fun sanitizeFtsQuery(q: String): String =
        q.replace(Regex("[\"*^()]"), "").trim().let { clean ->
            if (clean.isBlank()) "" else clean.split(Regex("\\s+")).joinToString(" OR ")
        }

    private class MemoryDbHelper(context: Context) :
        SQLiteOpenHelper(context, "freeman_memory.db", null, 2) {

        override fun onCreate(db: SQLiteDatabase) {
            db.execSQL("""
                CREATE TABLE memories (
                    id           INTEGER PRIMARY KEY AUTOINCREMENT,
                    role         TEXT    NOT NULL,
                    content      TEXT    NOT NULL,
                    session_id   TEXT    NOT NULL,
                    created_at   INTEGER NOT NULL,
                    recall_count INTEGER NOT NULL DEFAULT 0
                )
            """.trimIndent())
            db.execSQL("""
                CREATE VIRTUAL TABLE memories_fts
                USING fts5(content, content='memories', content_rowid='id', tokenize='porter unicode61')
            """.trimIndent())
            db.execSQL("""
                CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
                    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
                END
            """.trimIndent())
            db.execSQL("""
                CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
                    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.id, old.content);
                END
            """.trimIndent())
            db.execSQL("""
                CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
                    INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.id, old.content);
                    INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
                END
            """.trimIndent())
        }

        override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {
            if (oldVersion < 2) {
                db.execSQL("ALTER TABLE memories ADD COLUMN recall_count INTEGER NOT NULL DEFAULT 0")
            }
        }
    }
}
