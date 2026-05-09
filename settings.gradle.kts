rootProject.name = "freeman"

pluginManagement {
    repositories {
        google()
        gradlePluginPortal()
        mavenCentral()
    }
}

// Enables automatic GraalVM JDK download via Gradle toolchain provisioning
// No manual sdk install needed — Gradle fetches GraalVM CE 21 on first nativeCompile
plugins {
    id("org.gradle.toolchains.foojay-resolver-convention") version "0.8.0"
}

dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
    }
}

include(":shared")
include(":macos")
include(":android")
