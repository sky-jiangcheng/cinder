// The environment report — the JetBrains half of the diagnostics the VS Code
// extension shows as a quick pick.
//
// It answers one question: "why does this not work?" — and it answers it with
// FACTS, not with advice. Three rows, in the order a failure usually happens:
//
//   1. repotory-mcp — the panel's data channel. Found? Where?
//   2. the local service — the HTTP channel (project resolution, deep links,
//      the provider settings). Reachable?
//   3. the workspace — does the folder resolve to a project?
//
// Every row states what it found. A row that cannot be checked says why, so
// the report never looks complete when it is not.
package dev.repotory.jetbrains

import com.google.gson.JsonObject
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.DialogWrapper
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import java.awt.Font
import javax.swing.JButton
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.JScrollPane
import javax.swing.JTextArea

/**
 * The diagnostics report.
 *
 * @param project the IDE project, for the workspace path and the settings page
 */
class DiagnosticsDialog(private val project: Project?) : DialogWrapper(project) {

    private val report = JTextArea()
    private val recheckButton = JButton("Re-check")

    init {
        title = "Repotory: environment"
        setOKButtonText("Close")
        init()
        check()
    }

    override fun createCenterPanel(): JComponent {
        val root = JPanel(BorderLayout(0, JBUI.scale(6)))
        root.border = JBUI.Borders.empty(10, 12)

        report.isEditable = false
        report.lineWrap = true
        report.wrapStyleWord = true
        report.font = Font(Font.MONOSPACED, Font.PLAIN, JBUI.scale(12))
        report.text = "Checking…"
        val scroll = JScrollPane(report)
        scroll.preferredSize = java.awt.Dimension(JBUI.scale(560), JBUI.scale(260))

        val buttons = JPanel()
        buttons.isOpaque = false
        recheckButton.addActionListener { check() }
        buttons.add(recheckButton)
        val settingsButton = JButton("Open settings")
        settingsButton.addActionListener {
            ShowSettingsUtil.getInstance().showSettingsDialog(project, "dev.repotory.jetbrains.settings")
        }
        buttons.add(settingsButton)

        root.add(scroll, BorderLayout.CENTER)
        root.add(buttons, BorderLayout.SOUTH)
        return root
    }

    /** Re-read everything and repaint the report. */
    private fun check() {
        recheckButton.isEnabled = false
        report.text = "Checking…"
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val text = buildReport()
            app.invokeLater {
                report.text = text
                report.caretPosition = 0
                recheckButton.isEnabled = true
            }
        }
    }

    /** The three checks, as one block of text. */
    private fun buildReport(): String {
        val serverUrl = RepotorySettings.serverUrl()
        val sb = StringBuilder()

        val resolution = McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        sb.appendLine("repotory-mcp")
        if (resolution.found) {
            sb.appendLine("  ✓ ${resolution.path}")
            sb.appendLine("    source: ${resolution.source}")
        } else {
            sb.appendLine("  ✗ ${resolution.problem}")
            resolution.attempts.take(8).forEach { sb.appendLine("    tried: $it") }
        }
        sb.appendLine()

        val reachable = repotoryClient.ping(serverUrl)
        sb.appendLine("local service")
        if (reachable) {
            sb.appendLine("  ✓ $serverUrl")
        } else {
            sb.appendLine("  ✗ not reachable at $serverUrl")
            sb.appendLine("    search and notes still work through repotory-mcp.")
        }
        sb.appendLine()

        sb.appendLine("workspace")
        val dir = project?.basePath?.takeIf { it.isNotBlank() }
        if (dir == null) {
            sb.appendLine("  · no folder is open — nothing to resolve")
        } else {
            sb.appendLine("  $dir")
            val resolved = resolveWorkspace(serverUrl, dir)
            when {
                resolved == null -> sb.appendLine("  ✗ the local service could not resolve it")
                resolved.projectName.isNullOrBlank() ->
                    sb.appendLine("  · not in the knowledge base yet — pick a project from the panel's chip")
                else -> {
                    sb.appendLine("  ✓ ${resolved.projectName}")
                    resolved.repository?.let { sb.appendLine("    repository: $it") }
                }
            }
        }
        return sb.toString().trimEnd()
    }

    /** The workspace's resolution, read with the server's own matcher. */
    private fun resolveWorkspace(serverUrl: String, dir: String): WorkspaceInfo? {
        return try {
            val payload = repotoryClient.rpc(serverUrl, "ResolveWorkspacePath", listOf(dir)) as? JsonObject
            val project = payload?.get("Project")?.takeIf { !it.isJsonNull }?.asJsonObject
            val repository = payload?.get("Repository")?.takeIf { !it.isJsonNull }?.asJsonObject
            WorkspaceInfo(
                projectName = project?.get("name")?.takeIf { !it.isJsonNull }?.asString,
                repository = repository?.get("path")?.takeIf { !it.isJsonNull }?.asString,
            )
        } catch (e: Exception) {
            null
        }
    }

    /** What the resolution said about the workspace. */
    private data class WorkspaceInfo(
        val projectName: String?,
        val repository: String?,
    )
}
