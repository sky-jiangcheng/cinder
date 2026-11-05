// The note editor dialog — the JetBrains half of the maintenance surface
// the VS Code webview and the desktop app also ship.
//
// One dialog serves three entries: editing an existing note, creating a new
// note under the current project, and reviewing an AI tidy PROPOSAL
// (same fields, plus a "review, then save" banner). The AI proposal path is
// deliberately the SAME dialog as the manual edit: machine-generated wording
// is never saved silently — it lands in the editor and the user's save is
// the gate, exactly as in the desktop app.
//
// Swing, not the IDE's block editor: this is a small form, and the preview
// below the textarea reuses the reader's MarkdownLite renderer so what you
// see while editing is what the detail pane will show.
package dev.repotory.jetbrains

import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.DialogWrapper
import com.intellij.ui.JBColor
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBScrollPane
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import java.awt.Dimension
import java.awt.Font
import javax.swing.BorderFactory
import javax.swing.JComboBox
import javax.swing.JComponent
import javax.swing.JEditorPane
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.JTextArea
import javax.swing.JTextField
import javax.swing.event.DocumentEvent
import javax.swing.event.DocumentListener

private val NOTE_KINDS = listOf("knowledge", "log", "idea", "other")

/**
 * @param draft the initial fields (id = 0 for a new note)
 * @param projects the project dropdown options; empty hides the dropdown
 * @param proposal true when the draft holds an unconfirmed AI tidy result
 * @param warning non-fatal caveat from the AI call (truncation etc.)
 */
class NoteEditorDialog(
    project: Project?,
    private val draft: NoteDraft,
    private val projects: List<ProjectOption>,
    private val proposal: Boolean,
    private val warning: String,
) : DialogWrapper(project) {

    private val titleField = JTextField(draft.title, 24)
    private val kindCombo = JComboBox(NOTE_KINDS.toTypedArray())
    private val tagsField = JTextField(draft.tags, 16)
    private val projectCombo = JComboBox(projects.map { it.name }.toTypedArray())
    private val contentArea = JTextArea(draft.content, 20, 60)
    private val preview = JEditorPane()

    /** The edited draft, valid after an OK close. */
    fun editedDraft(): NoteDraft = NoteDraft(
        id = draft.id,
        projectId = selectedProjectId(),
        title = titleField.text.trim(),
        tags = tagsField.text.trim(),
        kind = kindCombo.selectedItem as? String ?: "knowledge",
        content = contentArea.text,
        pinned = draft.pinned,
    )

    init {
        kindCombo.selectedItem = draft.kind.ifBlank { "knowledge" }
        // Pre-select the note's own project when the list carries it, so the
        // dropdown shows the truth rather than silently defaulting to the
        // first entry (which would MOVE the note on save).
        val index = projects.indexOfFirst { it.id == draft.projectId }
        if (index >= 0) projectCombo.selectedIndex = index
        title = if (draft.id == 0L) "Cinder: New Note" else "Cinder: Edit Note"
        setOKButtonText(if (draft.id == 0L) "Create" else "Save")
        init()
    }

    override fun createCenterPanel(): JComponent {
        val root = JPanel(BorderLayout(0, JBUI.scale(8)))
        root.border = JBUI.Borders.empty(10, 12)

        if (proposal) {
            root.add(proposalBanner(), BorderLayout.NORTH)
        }

        val form = JPanel(BorderLayout(0, JBUI.scale(6)))

        val metaRow = JPanel(BorderLayout(JBUI.scale(8), 0))
        val left = JPanel(BorderLayout(JBUI.scale(4), 0))
        left.add(JLabel("Title"), BorderLayout.WEST)
        left.add(titleField, BorderLayout.CENTER)
        val kindBox = JPanel(BorderLayout(JBUI.scale(4), 0))
        kindBox.add(JLabel("Kind"), BorderLayout.WEST)
        kindBox.add(kindCombo, BorderLayout.CENTER)
        metaRow.add(left, BorderLayout.CENTER)
        metaRow.add(kindBox, BorderLayout.EAST)
        form.add(metaRow, BorderLayout.NORTH)

        val tagsRow = JPanel(BorderLayout(JBUI.scale(8), 0))
        tagsRow.add(JLabel("Tags"), BorderLayout.WEST)
        tagsRow.add(tagsField, BorderLayout.CENTER)
        if (projects.isNotEmpty()) {
            val projectBox = JPanel(BorderLayout(JBUI.scale(4), 0))
            projectBox.add(JLabel("Project (项目)"), BorderLayout.WEST)
            projectBox.add(projectCombo, BorderLayout.CENTER)
            tagsRow.add(projectBox, BorderLayout.EAST)
        }
        form.add(tagsRow, BorderLayout.CENTER)

        form.add(JLabel("Content (Markdown)"), BorderLayout.SOUTH)
        root.add(form, BorderLayout.CENTER)

        // Font.MONOSPACED is a logical font NAME (String), not a style —
        // construct the font directly instead of deriveFont(int, float).
        contentArea.font = Font(Font.MONOSPACED, Font.PLAIN, JBUI.scale(12))
        contentArea.lineWrap = true
        contentArea.wrapStyleWord = true
        val contentScroll = JBScrollPane(contentArea)
        contentScroll.preferredSize = Dimension(JBUI.scale(560), JBUI.scale(300))
        root.add(contentScroll, BorderLayout.CENTER)

        // Live preview, same renderer as the reader pane: the tidy proposal
        // is reviewed here, so the diagram source must be visible as it will
        // read — and mermaid stays a labelled code block, as in the reader.
        preview.contentType = "text/html"
        preview.isEditable = false
        preview.editorKit = detailHtmlKit()
        preview.border = JBUI.Borders.empty(6, 8)
        renderPreview()
        contentArea.document.addDocumentListener(object : DocumentListener {
            override fun insertUpdate(e: DocumentEvent?) = renderPreview()
            override fun removeUpdate(e: DocumentEvent?) = renderPreview()
            override fun changedUpdate(e: DocumentEvent?) = renderPreview()
        })
        val previewBox = JPanel(BorderLayout(0, JBUI.scale(4)))
        val previewLabel = JLabel("Preview")
        previewLabel.font = previewLabel.font.deriveFont(Font.BOLD, JBUI.scaleFontSize(11f).toFloat())
        previewBox.add(previewLabel, BorderLayout.NORTH)
        previewBox.add(preview, BorderLayout.CENTER)
        root.add(previewBox, BorderLayout.SOUTH)

        return root
    }

    /**
     * The review banner for an AI proposal. It is the human gate made
     * visible: machine-generated wording is a draft until the user saves it.
     */
    private fun proposalBanner(): JPanel {
        val banner = JPanel(BorderLayout())
        banner.border = BorderFactory.createCompoundBorder(
            BorderFactory.createLineBorder(JBColor.border(), 1),
            JBUI.Borders.empty(6, 8),
        )
        val head = JBLabel("AI tidied draft — review, then save.")
        head.font = head.font.deriveFont(Font.BOLD, JBUI.scaleFontSize(12f).toFloat())
        banner.add(head, BorderLayout.NORTH)
        if (warning.isNotBlank()) {
            val warn = JBLabel(warning)
            warn.font = warn.font.deriveFont(JBUI.scaleFontSize(11f).toFloat())
            banner.add(warn, BorderLayout.SOUTH)
        }
        return banner
    }

    private fun renderPreview() {
        preview.text = if (contentArea.text.isBlank()) {
            "<html><body><p><i>Nothing to preview yet.</i></p></body></html>"
        } else {
            "<html><body>${MarkdownLite.toHtml(contentArea.text)}</body></html>"
        }
        preview.caretPosition = 0
    }

    private fun selectedProjectId(): Long {
        val index = projectCombo.selectedIndex
        return if (index in projects.indices) projects[index].id else draft.projectId
    }

    /** An empty body cannot be saved; the button says so instead of failing server-side. */
    override fun doOKAction() {
        if (contentArea.text.isBlank()) {
            contentArea.requestFocus()
            setErrorText("Content is required", contentArea)
            return
        }
        super.doOKAction()
    }
}
