// Settings page for the Cinder plugin.
//
// ADR-0027 decision 2③ and its 2026-10-22 usability revision: the server
// address (default http://127.0.0.1:18765), the optional path to a local
// `repotory-server` binary, a connection health check, and a one-click start
// for that local service. Nothing here downloads or builds a server: the
// plugin only launches a binary the user already has.
package dev.repotory.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.options.Configurable
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import java.awt.BorderLayout
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

        val test = JButton("Test connection")
        test.addActionListener {
            val target = url.text?.trim().orEmpty()
            // /health answering is not the same as the deployment being usable:
            // a server without --web-dist serves the API but 404s every result
            // link, so a bare "Connected" would be a green light for a broken
            // click. The probe distinguishes the two cases.
            status.text = when {
                target.isEmpty() -> "Enter a server address first."
                !repotoryClient.ping(target) -> "No response from $target. Is the Cinder server running?"
                repotoryClient.servesWebUi(target) -> "Connected: $target — API and web UI both reachable."
                else ->
                    "Connected: $target, but API-only: \"Open in Cinder\" would 404. " +
                        "Use Start local service (release bundle) or restart it with --web-dist."
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

        // The model endpoint the answers come from. It lives in the SERVICE's
        // settings (SharedConfig), so this is an entry to the same dialog the
        // panel's header opens — not a second copy of those fields.
        val provider = JButton("Configure AI provider…")
        provider.addActionListener { ProviderDialog().show() }
        val providerRow = JPanel(BorderLayout(6, 0))
        providerRow.add(provider, BorderLayout.WEST)
        val providerHint = JBLabel(
            "OpenAI-compatible Base URL + model. A 404 from the panel usually means " +
                "the Base URL is a full endpoint instead of the server root.",
        )

        return FormBuilder.createFormBuilder()
            .addLabeledComponent(JBLabel("MCP binary:"), mcpRow, 1, false)
            .addComponent(checkMcp)
            .addLabeledComponent(JBLabel("Server address:"), url, 1, false)
            .addLabeledComponent(JBLabel("Server binary:"), binaryRow, 1, false)
            .addComponent(test)
            .addComponent(start)
            .addComponent(status)
            .addSeparator()
            .addComponent(providerRow)
            .addComponent(providerHint)
            .addComponentFillVertically(JPanel(), 0)
            .panel
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
        val tried = resolution.attempts.take(12).joinToString("\n")
        val more = if (resolution.attempts.size > 12) "\n… and ${resolution.attempts.size - 12} more" else ""
        return (resolution.problem ?: "repotory-mcp not found") + "\nLooked in:\n" + tried + more
    }

    private fun describeStart(outcome: StartOutcome, target: String): String = when (outcome) {
        is StartOutcome.Started ->
            if (!outcome.healthy) {
                "Started ${outcome.binary.file.name} on port ${outcome.port}, but the service is not " +
                    "answering yet. Wait a moment and press Test connection again."
            } else if (outcome.webUi) {
                "Started ${outcome.binary.file.name} on port ${outcome.port} — API and web UI reachable."
            } else {
                "Started ${outcome.binary.file.name} on port ${outcome.port}, API-only " +
                    "(no sibling web/ directory): result links would 404."
            }
        StartOutcome.AlreadyRunning -> "$target already answers — reusing the running service."
        is StartOutcome.BinaryMissing ->
            "No repotory-server binary found. Use Locate… to pick one, or download a release bundle."
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
