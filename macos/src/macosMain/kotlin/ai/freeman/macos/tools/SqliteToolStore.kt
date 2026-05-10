package ai.freeman.macos.tools

import ai.freeman.tools.StoredTool
import ai.freeman.tools.ToolStore
import java.sql.Connection
import java.sql.DriverManager

class SqliteToolStore(dbPath: String) : ToolStore {

    private val conn: Connection = DriverManager.getConnection("jdbc:sqlite:$dbPath")

    init {
        conn.createStatement().use { stmt ->
            stmt.executeUpdate("""
                CREATE TABLE IF NOT EXISTS tools (
                    name        TEXT PRIMARY KEY,
                    description TEXT NOT NULL,
                    params_json TEXT NOT NULL DEFAULT '{"type":"object"}',
                    body        TEXT NOT NULL,
                    runtime     TEXT NOT NULL DEFAULT 'bash',
                    timeout     INTEGER NOT NULL DEFAULT 30
                )
            """.trimIndent())
        }
    }

    override fun upsert(tool: StoredTool) {
        conn.prepareStatement("""
            INSERT INTO tools (name, description, params_json, body, runtime, timeout)
            VALUES (?, ?, ?, ?, ?, ?)
            ON CONFLICT(name) DO UPDATE SET
                description = excluded.description,
                params_json = excluded.params_json,
                body        = excluded.body,
                runtime     = excluded.runtime,
                timeout     = excluded.timeout
        """.trimIndent()).use { ps ->
            ps.setString(1, tool.name)
            ps.setString(2, tool.description)
            ps.setString(3, tool.paramsJson)
            ps.setString(4, tool.body)
            ps.setString(5, tool.runtime)
            ps.setInt(6, tool.timeoutSeconds)
            ps.executeUpdate()
        }
    }

    override fun loadAll(): List<StoredTool> =
        conn.createStatement().use { stmt ->
            val rs = stmt.executeQuery("SELECT name, description, params_json, body, runtime, timeout FROM tools")
            buildList {
                while (rs.next()) add(StoredTool(
                    name            = rs.getString("name"),
                    description     = rs.getString("description"),
                    paramsJson      = rs.getString("params_json"),
                    body            = rs.getString("body"),
                    runtime         = rs.getString("runtime"),
                    timeoutSeconds  = rs.getInt("timeout"),
                ))
            }
        }

    override fun close() = conn.close()
}
