package io.ok.agent

import com.intellij.openapi.project.Project
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.ui.content.ContentFactory
import com.intellij.ui.jcef.JBCefBrowser
import com.intellij.ui.jcef.JBCefClient
import org.cef.browser.CefBrowser
import org.cef.browser.CefFrame
import org.cef.handler.CefLoadHandlerAdapter
import java.awt.BorderLayout
import javax.swing.JPanel

/**
 * Tool window factory for the OK Agent sidebar.
 * Creates a JCEF WebView chat panel that connects to ok serve.
 */
class OkToolWindowFactory : ToolWindowFactory {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val panel = OkChatPanel(project)
        val content = ContentFactory.getInstance().createContent(panel, null, false)
        toolWindow.contentManager.addContent(content)
    }
}

/**
 * The chat panel displayed in the OK Agent tool window.
 * Uses JCEF (JetBrains Chromium Embedded Framework) to render the chat UI
 * and streams responses from the local ok serve process via HTTP/SSE.
 */
class OkChatPanel(private val project: Project) : JPanel(BorderLayout()) {

    private val okPort: Int = OkSettings.instance.port
    private val browser: JBCefBrowser = JBCefBrowser.createBuilder()
        .setClient(JBCefClient.createDefault())
        .setUrl("http://127.0.0.1:$okPort")
        .build()

    init {
        add(browser.component, BorderLayout.CENTER)

        // Inject OK chat UI JavaScript bridge once the page loads.
        browser.jbCefClient.addLoadHandler(object : CefLoadHandlerAdapter() {
            override fun onLoadEnd(browser: CefBrowser, frame: CefFrame, httpStatusCode: Int) {
                if (frame.isMain) {
                    injectChatBridge(browser)
                }
            }
        })
    }

    /**
     * Injects a JavaScript bridge that allows the IDE to push context
     * (selected code, file path, diagnostics) into the OK chat webview.
     */
    private fun injectChatBridge(browser: CefBrowser) {
        val js = """
            (function() {
                // IDE → WebView bridge: receives context from IDE actions
                window.__okBridge = {
                    submitPrompt: function(prompt) {
                        var input = document.querySelector('textarea, input[type="text"], [contenteditable]');
                        if (input) {
                            if (input.tagName === 'TEXTAREA' || input.tagName === 'INPUT') {
                                input.value = prompt;
                                input.dispatchEvent(new Event('input', { bubbles: true }));
                            } else {
                                input.textContent = prompt;
                                input.dispatchEvent(new Event('input', { bubbles: true }));
                            }
                            // Try to find and click the submit button
                            var submit = document.querySelector('button[type="submit"], button:has(svg)');
                            if (submit) submit.click();
                        }
                    }
                };
                console.log('[OK Bridge] Ready');
            })();
        """.trimIndent()
        browser.cefBrowser.executeJavaScript(js, browser.cefBrowser.url, 0)
    }

    /**
     * Pushes a prompt from an IDE action (explain, fix, review) into the chat.
     */
    fun submitPrompt(prompt: String) {
        // JSON-encode the prompt for safe JS interpolation.
        // org.json / Gson / kotlinx.serialization would be ideal, but
        // we escape the minimum dangerous chars to keep the dep footprint zero.
        val escaped = prompt
            .replace("\\", "\\\\")
            .replace("'", "\\'")
            .replace("\"", "\\\"")
            .replace("\n", "\\n")
            .replace("\r", "\\r")
            .replace("\t", "\\t")
            .replace("\b", "\\b")
            .replace("\u0000", "")
            .replace("`", "\\`")
            .replace("\${", "\\\${")
            .replace("</script>", "<\\/script>")
        browser.cefBrowser.executeJavaScript(
            "if (window.__okBridge) window.__okBridge.submitPrompt('$escaped');",
            browser.cefBrowser.url, 0
        )
    }
}
