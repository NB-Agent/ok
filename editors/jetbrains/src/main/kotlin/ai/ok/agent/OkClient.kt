package ai.ok.agent

import com.intellij.openapi.project.Project
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import javax.swing.*
import javax.swing.BoxLayout

/** Simple HTTP client to the OK serve backend (localhost:8787). */
object OkClient {
    private val client = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(5))
        .build()
    private const val BASE = "http://127.0.0.1:8787"

    fun submit(project: Project, input: String) {
        try {
            val jsonInput = """{"input":"${input.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\t", "\\t")}"}"""
            val req = HttpRequest.newBuilder()
                .uri(URI.create("$BASE/submit"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(jsonInput))
                .timeout(Duration.ofSeconds(120))
                .build()
            client.sendAsync(req, HttpResponse.BodyHandlers.ofString())
                .thenAccept { resp ->
                    if (resp.statusCode() != 200) {
                        SwingUtilities.invokeLater {
                            JOptionPane.showMessageDialog(
                                null,
                                "OK returned ${resp.statusCode()}",
                                "OK Agent",
                                JOptionPane.WARNING_MESSAGE
                            )
                        }
                    }
                }
        } catch (ex: Exception) {
            SwingUtilities.invokeLater {
                JOptionPane.showMessageDialog(
                    null,
                    "Cannot reach OK server. Run 'ok serve'.",
                    "OK Agent",
                    JOptionPane.ERROR_MESSAGE
                )
            }
        }
    }
}

/** Sidebar panel — minimal UI while the full webview is WIP. */
class OkAgentPanel(private val project: Project) : JPanel() {
    init {
        layout = BoxLayout(this, BoxLayout.Y_AXIS)
        add(JLabel("OK Agent — ready"))
        add(JLabel("Use right-click → OK: Explain / Fix / Review"))
        add(JLabel("Or open the OK Agent tool window for chat"))
    }
}
