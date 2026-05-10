package ai.freeman.macos.config

import ai.freeman.config.FreemanConfig
import com.charleskorn.kaml.Yaml
import com.charleskorn.kaml.YamlConfiguration

object ConfigLoader {
    private val yaml = Yaml(
        configuration = YamlConfiguration(strictMode = false)
    )

    fun load(path: String): FreemanConfig {
        val file = java.io.File(path)
        if (!file.exists()) {
            println("[Freeman] No config found at $path — using defaults")
            return FreemanConfig()
        }
        return yaml.decodeFromString(FreemanConfig.serializer(), file.readText())
    }
}
