// Settings page for the Cinder plugin — the channel and the local service.
//
// ADR-0027 decision 2③ and its 2026-10-22 usability revision, regrouped
// 2026-11-26: the page used to be one flat column of fields and buttons where
// a text field, a button and two paragraphs of status text sat at the same
// level. Three titled groups now say what belongs to what:
//
//   CHANNEL         the cinder-mcp binary — every data call goes through it
//   LOCAL SERVICE   optional: moving a note between projects
//   AI PROVIDER     a page of its own (Settings ▸ Tools ▸ Cinder ▸ AI Provider)
//
// The provider form is NOT here: it is configuration with its own fields,
// buttons and status, and a sub-page keeps this one answerable at a glance.
// Nothing here downloads or builds a server — the plugin only launches a
// binary the user already has.
package dev.repotory.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.options.Configurable
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import java.awt.FlowLayout
import javax.swing.JButton
import javax.swing.JComponent
import javax.swing.JPanel

class RepotoryConfigurable : Configurable {
    private var serverUrlField: JBTextField? = null
    private var binaryField: JBTextField? = null
    private var mcpBinaryField: JBTextField? = null
    private var statusLabel: JBLabel? = null

    override fun getDisplayName(): String = "Cinder"

    override fun createComponent(): JComponent {
        val url = JBTextField(RepotorySettings.serverUrl(), 32)
        serverUrlField = url
        val binary = JBTextField(RepotorySettings.serverBinaryPath(), 32)
        binaryField = binary
        val mcpBinary = JBTextField(RepotorySettings.mcpBinaryPath(), 32)
        mcpBinaryField = mcpBinary
        val status = JBLabel(" ")
        statusLabel = status

        val locate = JButton("Locate…")
        locate.addActionListener {
            chooseServerBinary()?.let { binary.text = it.absolutePath }
        }
        val binaryRow = JPanel(BorderLayout(6, 0))
        binaryRow.add(binary, BorderLayout.CENTER)
        binaryRow.add(locate, BorderLayout.EAST)

        val locateMcp = JButton("Locate…")
        locateMcp.addActionListener {
            chooseMcpBinary()?.let {
                mcpBinary.text = it.absolutePath
                // The MCP client is a long-lived process: a new binary path
                // only takes effect after it is restarted.
                repotoryMcp.shutdown()
                status.text = "MCP binary set. It will be used on the next call."
            }
        }
        val mcpRow = JPanel(BorderLayout(6, 0))
        mcpRow.add(mcpBinary, BorderLayout.CENTER)
        mcpRow.add(locateMcp, BorderLayout.EAST)

        // The MCP channel is the plugin's PRIMARY dependency since ADR-0035:
        // every data call goes through it. "Is it there?" is therefore the
        // first question the settings page answers — and it answers it with
        // the LIST of places it looked, because "not found" is only
        // actionable when the user can see where it looked.
        val checkMcp = JButton("Check MCP channel")
        checkMcp.addActionListener {
            status.text = describeMcpResolution(
                McpBinaryResolver.resolveHere(mcpBinary.text?.trim().orEmpty()),
            )
        }
        val mcpActions = JPanel(FlowLayout(FlowLayout.LEFT, 0, 0))
        mcpActions.isOpaque = false
        mcpActions.add(checkMcp)

        val test = JButton("Test connection")
        test.addActionListener {
            val target = url.text?.trim().orEmpty()
            // /health answering means the one HTTP dependency left is there:
            // moving a note between projects. Everything else — ask, search,
            // read, write, organize — goes through MCP and needs no server.
            status.text = when {
                target.isEmpty() -> "Enter a server address first."
                !repotoryClient.ping(target) -> "No response from $target. Is the Cinder server running?"
                else -> "Connected: $target — the project move will work."
            }
        }

        val start = JButton("Start local service")
        start.addActionListener {
            val target = url.text?.trim().orEmpty()
            if (target.isEmpty()) {
                status.text = "Enter a server address first."
                return@addActionListener
            }
            val override = binary.text?.trim()?.takeIf { it.isNotEmpty() }?.let { expandHome(it) }
            status.text = "Starting local service…"
            val app = ApplicationManager.getApplication()
            app.executeOnPooledThread {
                val outcome = repotoryServer.start(target, override)
                app.invokeLater { status.text = describeStart(outcome, target) }
            }
        }
        val serviceActions = JPanel(FlowLayout(FlowLayout.LEFT, 6, 0))
        serviceActions.isOpaque = false
        serviceActions.add(test)
        serviceActions.add(start)

        return FormBuilder.createFormBuilder()
            .addComponent(sectionLabel("Channel"))
            .addComponent(hint("Every search, read and write goes through the cinder-mcp binary. " +
                "Leave the path empty to detect it from PATH, Homebrew/Scoop and ~/.local/bin."))
            .addLabeledComponent(JBLabel("MCP binary:"), mcpRow, 1, false)
            .addComponent(mcpActions)
            .addVerticalGap(JBUI.scale(14))
            .addComponent(sectionLabel("Local service (optional)"))
            .addComponent(hint("Only one thing still speaks HTTP: moving a note between projects. " +
                "Ask, search, read, write, organize and the AI provider all go through MCP."))
            .addLabeledComponent(JBLabel("Server address:"), url, 1, false)
            .addLabeledComponent(JBLabel("Server binary:"), binaryRow, 1, false)
            .addComponent(serviceActions)
            .addVerticalGap(JBUI.scale(6))
            .addComponent(status)
            .addVerticalGap(JBUI.scale(14))
            .addComponent(sectionLabel("AI provider"))
            .addComponent(hint("The endpoint, the key and the model have their own page — " +
                "they are the same settings the desktop app uses."))
            .addComponent(providerLink())
            .addComponentFillVertically(JPanel(), 0)
            .panel
    }

    /**
     * The entry to the provider page.
     *
     * The endpoint, the key and the model live on a page of their own — they
     * are configuration with their own fields, buttons and status, and folding
     * them into this page is what made it unreadable. The link is here because
     * "where do I change the model?" is a question this page must answer.
     */
    private fun providerLink(): JButton = JButton("AI Provider settings…").apply {
        toolTipText = "Base URL, API key and model of the endpoint that answers"
        addActionListener {
            ShowSettingsUtil.getInstance().showSettingsDialog(null, RepotoryProviderConfigurable::class.java)
        }
    }

    private fun sectionLabel(text: String): JBLabel = JBLabel(text.uppercase()).apply {
        font = font.deriveFont(java.awt.Font.BOLD, font.size2D - 1f)
        foreground = PanelPalette.muted
    }

    private fun hint(text: String): JBLabel =
        JBLabel("<html><body style='width:520px'>" + text + "</body></html>").apply {
            foreground = PanelPalette.muted
        }

    override fun isModified(): Boolean =
        serverUrlField?.text?.trim() != RepotorySettings.serverUrl() ||
            binaryField?.text?.trim().orEmpty() != RepotorySettings.serverBinaryPath() ||
            mcpBinaryField?.text?.trim().orEmpty() != RepotorySettings.mcpBinaryPath()

    override fun apply() {
        RepotorySettings.setServerUrl(serverUrlField?.text?.trim().orEmpty())
        RepotorySettings.setServerBinaryPath(binaryField?.text?.trim().orEmpty())
        val mcpPath = mcpBinaryField?.text?.trim().orEmpty()
        if (mcpPath != RepotorySettings.mcpBinaryPath()) {
            RepotorySettings.setMcpBinaryPath(mcpPath)
            // The running MCP client points at the OLD binary; a changed
            // setting has to take effect, not wait for the next IDE restart.
            repotoryMcp.shutdown()
        }
    }

    override fun reset() {
        serverUrlField?.text = RepotorySettings.serverUrl()
        binaryField?.text = RepotorySettings.serverBinaryPath()
        mcpBinaryField?.text = RepotorySettings.mcpBinaryPath()
        statusLabel?.text = " "
    }

    override fun disposeUIResources() {
        serverUrlField = null
        binaryField = null
        mcpBinaryField = null
        statusLabel = null
    }

    /**
     * The MCP resolution, spelled out.
     *
     * The found case says WHERE it found it (the source matters: a PATH hit
     * can be shadowed by an older install). The missing case lists every
     * directory that was tried — the same shape as the VS Code extension's
     * diagnostics, so the two IDEs answer the same question identically.
     */
    private fun describeMcpResolution(resolution: McpBinaryResolution): String {
        if (resolution.found) {
            return "repotory-mcp found at ${resolution.path} (via ${resolution.source})."
        }
        val tried = resolution.attempts.take(12).joinToString("<br>")
        val more = if (resolution.attempts.size > 12) "<br>… and ${resolution.attempts.size - 12} more" else ""
        return "<html>" + (resolution.problem ?: "repotory-mcp not found") + "<br>Looked in:<br>" + tried + more + "</html>"
    }

    private fun describeStart(outcome: StartOutcome, target: String): String = when (outcome) {
        is StartOutcome.Started ->
            if (!outcome.healthy) {
                "Started ${outcome.binary.file.name} on port ${outcome.port}, but the service is not " +
                    "answering yet. Wait a moment and press Test connection again."
            } else {
                "Started ${outcome.binary.file.name} on port ${outcome.port} — the service is answering."
            }
        StartOutcome.AlreadyRunning -> "$target already answers — reusing the running service."
        // No "download…" clause: this page has no download and no link, so the
        // sentence may only name what is on it. (It used to tell the reader to
        // "download a release bundle", which only the dead dialog it replaced
        // ever offered.)
        is StartOutcome.BinaryMissing ->
            "No repotory-server binary found. Use Locate… to pick one — it ships with the Cinder " +
                "release bundle, next to its web/ directory."
        is StartOutcome.Failed -> outcome.message
    }
}

/**
 * Persistent settings.
 *
 * The address is stored in the IDE's own properties store (not in Cinder's
 * database): the plugin is a client, and a client must not write into the
 * service's storage.
 */
object RepotorySettings {
    fun serverUrl(): String =
        com.intellij.openapi.application.ApplicationManager.getApplication()
            .getService(RepotorySettingsState::class.java)
            .serverUrl?.takeIf { it.isNotBlank() } ?: RepotoryClient.DEFAULT_SERVER_URL

    fun setServerUrl(value: String) {
        com.intellij.openapi.application.ApplicationManager.getApplication()
            .getService(RepotorySettingsState::class.java)
            .serverUrl = value.ifBlank { RepotoryClient.DEFAULT_SERVER_URL }
    }

    /** Empty means "auto-detect in PATH / common install directories". */
    fun serverBinaryPath(): String =
        com.intellij.openapi.application.ApplicationManager.getApplication()
            .getService(RepotorySettingsState::class.java)
            .serverBinaryPath.orEmpty()

    fun setServerBinaryPath(value: String) {
        com.intellij.openapi.application.ApplicationManager.getApplication()
            .getService(RepotorySettingsState::class.java)
            .serverBinaryPath = value.trim()
    }

    /**
     * The `repotory-mcp` binary, empty when it should be auto-detected.
     *
     * This is the plugin's primary dependency since ADR-0035: the MCP
     * channel carries every data call, so this path decides whether the
     * panel works at all.
     */
    fun mcpBinaryPath(): String =
        com.intellij.openapi.application.ApplicationManager.getApplication()
            .getService(RepotorySettingsState::class.java)
            .mcpBinaryPath.orEmpty()

    fun setMcpBinaryPath(value: String) {
        com.intellij.openapi.application.ApplicationManager.getApplication()
            .getService(RepotorySettingsState::class.java)
            .mcpBinaryPath = value.trim()
    }
}

/** The state bean behind [RepotorySettings]; IntelliJ persists it. */
@com.intellij.openapi.components.State(
    name = "RepotorySettings",
    storages = [com.intellij.openapi.components.Storage("repotory.xml")],
)
class RepotorySettingsState : com.intellij.openapi.components.BaseState() {
    // BaseState's string delegate is nullable (a state bean can be read
    // before it was ever written), so the property is declared String? and
    // RepotorySettings.serverUrl() falls back to the default.
    var serverUrl: String? by string(RepotoryClient.DEFAULT_SERVER_URL)
    var serverBinaryPath: String? by string("")
    var mcpBinaryPath: String? by string("")
}
