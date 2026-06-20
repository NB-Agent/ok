package ai.ok.agent

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.editor.Editor

/** Ask OK to explain the selected code. */
class OkExplainAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val editor: Editor = e.getRequiredData(CommonDataKeys.EDITOR)
        val selection = editor.selectionModel.selectedText ?: return
        val project = e.project ?: return
        OkClient.submit(project, "Explain this code concisely:\n```\n$selection\n```")
    }
}

/** Ask OK to fix problems in the current file. */
class OkFixAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val editor: Editor = e.getRequiredData(CommonDataKeys.EDITOR)
        val file = e.getRequiredData(CommonDataKeys.PSI_FILE)
        val project = e.project ?: return
        OkClient.submit(project, "Fix problems in ${file.name}:\n```\n${editor.document.text}\n```\nApply minimal fixes.")
    }
}

/** Ask OK to review VCS changes. */
class OkReviewAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        OkClient.submit(project, "/review")
    }
}
