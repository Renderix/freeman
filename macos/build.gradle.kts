plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.graalvm.native)
}

kotlin {
    jvm("macos")

    sourceSets {
        val macosMain by getting {
            dependencies {
                implementation(project(":shared"))
                implementation(libs.kotlinx.coroutines.core)
                implementation(libs.ktor.client.okhttp)
                implementation(libs.ktor.client.content.negotiation)
                implementation(libs.ktor.serialization.json)
                implementation(libs.onnxruntime)
                // sherpa-onnx JVM JAR — download via scripts/setup_kotlin_libs.sh
                implementation(fileTree("libs") { include("*.jar") })
            }
        }
        val macosTest by getting {
            dependencies { implementation(kotlin("test")) }
        }
    }
}

// Configure the KMP JVM jar to include a Main-Class manifest entry
tasks.named<Jar>("macosJar") {
    manifest { attributes["Main-Class"] = "ai.freeman.macos.MainKt" }
    // Bundle runtime classpath so `java -jar` works without -cp
    from(configurations.named("macosRuntimeClasspath").map { cfg ->
        cfg.map { if (it.isDirectory) it else zipTree(it) }
    })
    duplicatesStrategy = DuplicatesStrategy.EXCLUDE
}

graalvmNative {
    toolchainDetection.set(true)   // Gradle downloads GraalVM automatically — no manual install
    binaries {
        create("main") {
            javaLauncher.set(javaToolchains.launcherFor {
                languageVersion.set(JavaLanguageVersion.of(21))
                vendor.set(JvmVendorSpec.matching("GraalVM"))
            })
            imageName.set("freeman")
            mainClass.set("ai.freeman.macos.MainKt")
            buildArgs.addAll(
                "--no-fallback",
                "-H:ConfigurationFileDirectories=native-image/",
                "-H:+ReportExceptionStackTraces",
            )
        }
    }
}
