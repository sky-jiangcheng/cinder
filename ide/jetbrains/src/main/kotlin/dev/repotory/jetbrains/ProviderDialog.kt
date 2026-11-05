// The AI provider sheet — the JetBrains half of the provider configuration
// the VS Code webview also ships (ADR-0044 decision 5).
//
// It is a DIALOG, not a card of the feed: Swing has no inline editing
// surface for a form, and a modal form is the platform's own answer — the
// same reason the note editor is a dialog.
//
// The VALUES are not the plugin's: the sheet reads and writes the local
// service's `ai_chat_*` configuration, so the desktop app and this panel
// always use the same endpoint. The plugin keeps no second copy (two copies
// would drift), and the API key is never shown — the server masks it, and an
// empty field means "leave the stored key alone".
//
// Every call BLOCKS (it is HTTP to the local service), and the EDT never
// waits on a socket: each button runs on a pooled thread and the sheet is
// updated from invokeLater.
package dev.repotory.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.DialogWrapper
import com.intellij.ui.JBColor
import com.intellij.ui.components.JBLabel
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import java.awt.FlowLayout
import java.awt.Font
import java.awt.GridLayout
import javax.swing.JButton
import javax.swing.JComboBox
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.JPasswordField
import javax.swing.JTextField

/**
 * The provider sheet.
 *
 * @param session the conversation model; it owns the transport, so the sheet
 *   never talks to the service itself
 */
class ProviderDialog(project: Project?, private val session: ChatSession) : DialogWrapper(project) {

    private val baseField = JTextField(32)
    private val keyField = JPasswordField(32)
    private val modelField = JTextField(20)
    private val modelCombo = JComboBox<String>()
    private val status = JBLabel(" ")
    private val listButton = JButton("List models")
    private val testButton = JButton("Test")
    private val saveButton = JButton("Save")

    init {
        title = "Cinder: AI provider"
        // The sheet saves on its own button, so the dialog's own button only
        // closes it. Calling it "Save" would promise a write that the button
        // does not do.
        setOKButtonText("Close")
        init()
        load()
    }

    override fun createCenterPanel(): JComponent {
        val root = JPanel(BorderLayout(0, JBUI.scale(8)))
        root.border = JBUI.Borders.empty(10, 12)

        root.add(
            JBLabel(
                "Cinder keeps these in its local service. The desktop app and this panel " +
                    "use the same ones.",
            ),
            BorderLayout.NORTH,
        )

        val form = JPanel(GridLayout(0, 1, 0, JBUI.scale(6)))
        form.add(field("Base URL", baseField, "http://127.0.0.1:1234/v1"))
        form.add(field("API key", keyField, "leave empty to keep the stored key"))
        form.add(field("Model", modelField, "the model id to ask"))
        form.add(modelsRow())
        root.add(form, BorderLayout.CENTER)

        val buttons = JPanel(FlowLayout(FlowLayout.LEFT, JBUI.scale(6), 0))
        buttons.isOpaque = false
        listButton.addActionListener { listModels() }
        testButton.addActionListener { test() }
        saveButton.addActionListener { save() }
        buttons.add(listButton)
        buttons.add(testButton)
        buttons.add(saveButton)

        status.foreground = JBColor.foreground()
        status.border = JBUI.Borders.emptyTop(JBUI.scale(4))

        // BorderLayout takes ONE component per region, so the buttons and the
        // verdict line share a south panel — otherwise the second add()
        // silently replaces the first.
        val south = JPanel(BorderLayout(0, 0))
        south.isOpaque = false
        south.add(buttons, BorderLayout.NORTH)
        south.add(status, BorderLayout.SOUTH)
        root.add(south, BorderLayout.SOUTH)

        return root
    }

    /** One labelled field, with a placeholder that says what goes in it. */
    private fun field(label: String, input: JComponent, hint: String): JComponent {
        val head = JBLabel(label)
        head.font = head.font.deriveFont(Font.BOLD, JBUI.scaleFontSize(11f).toFloat())
        val box = JPanel(BorderLayout(JBUI.scale(8), 0))
        box.isOpaque = false
        box.add(head, BorderLayout.WEST)
        if (input is JTextField) input.toolTipText = hint
        box.add(input, BorderLayout.CENTER)
        return box
    }

    /**
     * The model picker.
     *
     * It is hidden until the endpoint has answered: an empty dropdown is a
     * control that looks broken, and the model can always be typed by hand.
     */
    private fun modelsRow(): JComponent {
        val head = JBLabel("Models")
        head.font = head.font.deriveFont(Font.BOLD, JBUI.scaleFontSize(11f).toFloat())
        val box = JPanel(BorderLayout(JBUI.scale(8), 0))
        box.isOpaque = false
        box.add(head, BorderLayout.WEST)
        modelCombo.isVisible = false
        modelCombo.addActionListener {
            val picked = modelCombo.selectedItem as? String ?: return@addActionListener
            if (picked.isNotBlank()) modelField.text = picked
        }
        box.add(modelCombo, BorderLayout.CENTER)
        return box
    }

    // ---------------------------------------------------------------- actions

    /** Read the server's settings into the form. */
    private fun load() {
        busy(true)
        status.text = "Reading the settings from the local service…"
        background({ session.loadProvider() }) { outcome ->
            busy(false)
            outcome.settings?.let { settings ->
                baseField.text = settings.baseUrl
                modelField.text = settings.model
                keyField.text = ""
            }
            status.text = outcome.message
            status.foreground = if (outcome.ok) JBColor.foreground() else JBColor.RED
        }
    }

    /** Ask the endpoint which models it offers. */
    private fun listModels() {
        busy(true)
        status.text = "Asking the endpoint for its models…"
        background({ session.listProviderModels(baseField.text, keyField.text) }) { outcome ->
            busy(false)
            if (outcome.models.isEmpty()) {
                modelCombo.isVisible = false
            } else {
                modelCombo.removeAllItems()
                outcome.models.forEach { modelCombo.addItem(it) }
                modelCombo.isVisible = true
            }
            status.text = outcome.message
            status.foreground = if (outcome.ok) JBColor.foreground() else JBColor.RED
        }
    }

    /** Prove the endpoint and the model answer before anything is saved. */
    private fun test() {
        busy(true)
        status.text = "Sending one test message…"
        background({ session.testProvider(baseField.text, modelField.text, keyField.text) }) { outcome ->
            busy(false)
            status.text = outcome.message
            status.foreground = if (outcome.ok) JBColor.foreground() else JBColor.RED
        }
    }

    /** Write the settings to the local service. */
    private fun save() {
        busy(true)
        status.text = "Saving…"
        background({ session.saveProvider(baseField.text, modelField.text, keyField.text) }) { outcome ->
            busy(false)
            status.text = outcome.message
            status.foreground = if (outcome.ok) JBColor.foreground() else JBColor.RED
        }
    }

    /** The three actions cannot overlap: one request at a time. */
    private fun busy(busy: Boolean) {
        listButton.isEnabled = !busy
        testButton.isEnabled = !busy
        saveButton.isEnabled = !busy
    }

    /** Run work off the EDT and report its outcome back on the EDT. */
    private fun <T> background(work: () -> T, then: (T) -> Unit) {
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val result = work()
            app.invokeLater { then(result) }
        }
    }
}
