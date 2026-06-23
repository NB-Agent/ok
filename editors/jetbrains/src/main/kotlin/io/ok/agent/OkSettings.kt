package io.ok.agent

import com.intellij.openapi.options.Configurable
import com.intellij.openapi.options.ConfigurationException
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.JTextField
import java.awt.GridBagConstraints
import java.awt.GridBagLayout

/**
 * Persistent settings for the OK Agent plugin.
 */
class OkSettings {
    companion object {
        val instance: OkSettings by lazy { OkSettings() }
    }

    /** Port for the local ok serve process. */
    var port: Int = 8787
        get() = field
        set(value) { field = value }

    /** Path to the ok binary (empty = use PATH). */
    var binaryPath: String = ""
        get() = field
        set(value) { field = value }
}

/**
 * Settings UI for configuring the OK Agent plugin.
 */
class OkSettingsConfigurable : Configurable {
    private var panel: JPanel? = null
    private var portField: JTextField? = null
    private var binaryPathField: JTextField? = null

    override fun getDisplayName(): String = "OK Agent"

    override fun createComponent(): JComponent {
        val settings = OkSettings.instance
        val panel = JPanel(GridBagLayout())
        val c = GridBagConstraints()

        c.fill = GridBagConstraints.HORIZONTAL
        c.gridx = 0; c.gridy = 0
        c.anchor = GridBagConstraints.WEST
        panel.add(JLabel("OK Server Port:"), c)

        c.gridx = 1
        portField = JTextField(settings.port.toString(), 10)
        panel.add(portField, c)

        c.gridx = 0; c.gridy = 1
        panel.add(JLabel("OK Binary Path:"), c)

        c.gridx = 1
        binaryPathField = JTextField(settings.binaryPath, 20)
        binaryPathField!!.toolTipText = "Leave empty to use PATH"
        panel.add(binaryPathField, c)

        this.panel = panel
        return panel
    }

    override fun isModified(): Boolean {
        val s = OkSettings.instance
        return portField?.text?.toIntOrNull() != s.port ||
               binaryPathField?.text != s.binaryPath
    }

    override fun apply() {
        val s = OkSettings.instance
        s.port = portField?.text?.toIntOrNull() ?: s.port
        s.binaryPath = binaryPathField?.text ?: s.binaryPath
    }

    override fun reset() {
        val s = OkSettings.instance
        portField?.text = s.port.toString()
        binaryPathField?.text = s.binaryPath
    }
}
