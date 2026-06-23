# OK Agent — JetBrains Plugin

## Prerequisites
- IntelliJ IDEA Ultimate / PyCharm / GoLand (2023.3+)
- Gradle 8.x

## Building

```bash
cd editors/jetbrains
./gradlew build
```

## Installing

Build → Plugin Jar → `File > Settings > Plugins > Install from Disk`

## Development

Open this directory in IntelliJ IDEA as a Gradle project.

## Architecture

```
editors/jetbrains/
├── build.gradle.kts       ← Gradle build config
├── settings.gradle.kts    ← Gradle settings
├── src/main/
│   ├── kotlin/io/ok/agent/
│   │   ├── OkToolWindow.kt    ← Main panel (WebView)
│   │   ├── OkActions.kt       ← Actions (Explain, Fix, Review)
│   │   └── OkSettings.kt      ← Settings panel
│   └── resources/
│       ├── META-INF/plugin.xml ← Plugin descriptor
│       └── icons/
└── README.md
```

## Status
✅ Skeleton — tool window + commands registered  
⏳ Pending: full WebView chat integration  
