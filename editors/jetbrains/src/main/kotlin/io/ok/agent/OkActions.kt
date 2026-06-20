package io.ok.agent

import com.intellij.codeInsight.intention.IntentionAction
import com.intellij.codeInsight.intention.PriorityAction
import com.intellij.codeInspection.LocalQuickFix
import com.intellij.codeInspection.ProblemDescriptor
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.diagnostic.Logger
import com.intellij.openapi.editor.Editor
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.TextRange
import com.intellij.openapi.vcs.changes.ChangeListManager
import com.intellij.openapi.wm.ToolWindowManager
import com.intellij.psi.PsiFile
import com.intellij.util.IncorrectOperationException
import java.net.HttpURLConnection
import java.net.URL
import java.io.OutputStreamWriter

private val LOG = Logger.getInstance("io.ok.agent")

/**
 * Explain selected code using OK. Opens the sidebar and submits the prompt.
 */
class ExplainAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val project = e.project ?: return
        val selection = editor.selectionModel.selectedText ?: return
        val prompt = "Explain this code concisely:\n```\n$selection\n```"
        submitToChat(project, prompt)
    }
}

/**
 * Fix problems in the current file using OK.
 */
class FixAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val file = e.getData(CommonDataKeys.PSI_FILE) ?: return
        val prompt = "Fix problems in ${file.name}. Apply minimal fixes."
        submitToChat(project, prompt)
    }
}

/**
 * Review uncommitted changes using OK.
 */
class ReviewAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        promptForReview(project)
    }
}

/**
 * Inline intention action: "Explain with OK" shown as a lightbulb in the editor.
 * Appears when code is selected or cursor is on a function/class.
 */
class OkExplainIntention : IntentionAction, PriorityAction {
    override fun getText(): String = "Explain with OK"
    override fun getFamilyName(): String = "OK Agent"
    override fun isAvailable(project: Project, editor: Editor?, file: PsiFile?): Boolean {
        if (editor == null) return false
        val selection = editor.selectionModel
        return selection.hasSelection() && selection.selectedText!!.length > 10
    }

    override fun invoke(project: Project, editor: Editor?, file: PsiFile?) {
        if (editor == null) return
        val selection = editor.selectionModel.selectedText ?: return
        val prompt = "Explain this code concisely:\n```\n$selection\n```"
        submitToChat(project, prompt)
    }

    override fun startInWriteAction(): Boolean = false
    override fun getPriority(): PriorityAction.Priority = PriorityAction.Priority.LOW
}

// ── Internal helpers ────────────────────────────────────────────────────────

/**
 * Finds the OK chat panel in the tool window and pushes a prompt via the
 * JavaScript bridge. Falls back to HTTP /submit if the bridge is unavailable.
 */
private fun submitToChat(project: Project, prompt: String) {
    // Try to push through the WebView bridge first
    val toolWindow = ToolWindowManager.getInstance(project).getToolWindow("OK Agent")
    if (toolWindow != null) {
        toolWindow.activate {
            val contentManager = toolWindow.contentManager
            if (contentManager.contentCount > 0) {
                val component = contentManager.getContent(0)?.component
                if (component is OkChatPanel) {
                    component.submitPrompt(prompt)
                    return@activate
                }
            }
            // Fallback: HTTP POST
            submitViaHttp(project, prompt)
        }
    } else {
        submitViaHttp(project, prompt)
    }
}

private fun submitViaHttp(project: Project, input: String) {
    ApplicationManager.getApplication().executeOnPooledThread {
        try {
            val port = OkSettings.instance.port
            val url = URL("http://127.0.0.1:$port/submit")
            val conn = url.openConnection() as HttpURLConnection
            conn.requestMethod = "POST"
            conn.doOutput = true
            conn.setRequestProperty("Content-Type", "application/json")
            conn.setRequestProperty("Accept", "application/json")
            conn.connectTimeout = 5000
            conn.readTimeout = 60000

            val body = """{"input": "${input.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t")}"}"""
            OutputStreamWriter(conn.outputStream).use { it.write(body) }

            conn.inputStream.bufferedReader().readText()
        } catch (ex: Exception) {
            LOG.warn("OK submit failed", ex)
            com.intellij.openapi.ui.Messages.showErrorDialog(
                project,
                "Failed to connect to OK: ${ex.message}\n\nEnsure 'ok serve' is running.",
                "OK Agent Error"
            )
        }
    }
}

private fun promptForReview(project: Project) {
    ApplicationManager.getApplication().executeOnPooledThread {
        try {
            val changes = getUncommittedChanges(project)
            if (changes.isBlank()) {
                com.intellij.openapi.ui.Messages.showInfoMessage(
                    project, "No uncommitted changes to review.", "OK Review"
                )
                return@executeOnPooledThread
            }
            submitToChat(project, "Review these changes concisely:\n```diff\n${changes.take(8000)}\n```")
        } catch (ex: Exception) {
            LOG.warn("Failed to get changes", ex)
        }
    }
}

private fun getUncommittedChanges(project: Project): String {
    return try {
        val changeListManager = ChangeListManager.getInstance(project)
        val changes = changeListManager.allChanges
        val sb = StringBuilder()
        for (change in changes.take(20)) {
            sb.appendLine("=== ${change.filePath} ===")
            change.virtualFile?.let { vf ->
                val content = com.intellij.openapi.editor.DocumentManager.getInstance()
                    .getDocument(vf)?.text ?: ""
                sb.appendLine(content.take(2000))
            }
            sb.appendLine()
        }
        sb.toString()
    } catch (ex: Exception) {
        LOG.warn("getUncommittedChanges", ex)
        ""
    }
}
