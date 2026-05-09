plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "ai.freeman.android"
    compileSdk = 35
    defaultConfig {
        applicationId = "ai.freeman.android"
        minSdk = 29        // Android 10 — required for LiteRT-LM
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}

dependencies {
    implementation(project(":shared"))
    implementation(libs.kotlinx.coroutines.core)
    implementation(libs.onnxruntime.android)
    // sherpa-onnx Java API JAR + native .so via jniLibs/ — download via scripts/setup_kotlin_libs.sh
    implementation(fileTree("libs") { include("*.jar") })
}
