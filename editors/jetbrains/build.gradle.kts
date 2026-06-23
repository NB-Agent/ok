import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType
import org.jetbrains.intellij.platform.gradle.models.ProductRelease

plugins {
    id("org.jetbrains.intellij.platform") version "2.1.0"
    kotlin("jvm") version "1.9.22"
}

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        create(IntelliJPlatformType.IC, "2024.1")
        bundledPlugin("com.intellij.java")
        testFramework()
    }
}

intellijPlatform {
    pluginConfiguration {
        name = "OK Agent"
        version = project.version.toString()
    }
    instrumentCode = true
}

tasks {
    buildSearchableOptions { enabled = false }
}
