// Package main — OK Agent IntelliJ/WebStorm plugin entry point.
//
// Architecture: the plugin starts "ok serve" as a child process and
// communicates via HTTP/SSE on localhost:8787 — same backend as the
// VS Code extension, TUI, and desktop app.  All IDE-specific logic
// lives in the plugin; the controller is unchanged.
//
// Build:
//   ./gradlew buildPlugin
//
// Install:
//   ./gradlew runIde   (development sandbox)
//   or install the .zip from build/distributions/ via Settings → Plugins → ⚙ → Install from Disk.
package ai.ok.agent

import com.intellij.openapi.project.Project
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.ui.content.ContentFactory

class OkToolWindowFactory : ToolWindowFactory {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val panel = OkAgentPanel(project)
        val content = ContentFactory.getInstance().createContent(panel, "", false)
        toolWindow.contentManager.addContent(content)
    }
}
