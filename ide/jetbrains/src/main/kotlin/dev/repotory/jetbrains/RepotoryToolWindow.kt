// Knowledge-search tool window for the Repotory plugin.
//
// ADR-0027 decision 2①: notes + todos, searched through the shared
// service layer. The plugin does NOT reimplement ranking, gating or snippet
// extraction — it renders what `SearchAll` returns.
//
// ADR-0027 decision 2②: a result opens the Repotory SPA in the system
// browser, deep-linked to that result. The plugin does not embed the SPA:
// one rendering of the knowledge UI, the desktop app's.
//
// Layout (2026-10-29 revision) — a native IDE reader, no T-shape:
//
//   ┌──────────────────────────────────────────────────────────┐
//   │ ▪ Repotory                          [● online] [↻] [▶]     │  identity + service
//   ├──────────────────────────────────────────────────────────┤
//   │ 🔍 [search…………………………]                [Notes][Pages]    │  query + space
//   ├──────────────────────┬───────────────────────────────────┤
//   │ results               │ [✎][+][⚡][⧉][↗]                │  per-item actions
//   │ (kind · title ·       │ the selected knowledge, in full   │
//   │  matched excerpt)     │                                   │
//   ├──────────────────────┴───────────────────────────────────┤
//   │ status line                                                │
//   └──────────────────────────────────────────────────────────┘
//
// Why it changed: the previous panel stacked a fixed cream brand band over
// a fixed-width column and a row of text buttons. Every one of those had a
// width of its own, so a narrow tool window stretched the band, clipped the
// buttons and let the status message push the buttons off the panel — the
// "T" deformed instead of reflowing. The new shell gives every region a
// flexible width: the header is a slim native strip, the two columns are a
// draggable split pane, and every action is an icon in a platform toolbar.
//
// Connection (2026-10-29 revision): the panel PROBES the service on open and
// shows the answer as a status pill. When nothing answers, it starts the
// local service ONCE, by itself, and retries the query — the commonest
// first-run state (the plugin is installed, no server is running) used to
// need a click on a button that only appeared after a failed search.
//
// The full body of a note is fetched on selection (GetNote over the same
// RPC bridge); todos carry their whole content in the excerpt, and a note
// whose read fails falls back to the excerpt with a label that says so.
//
// The action toolbar is the maintenance surface (2026-10-23): Edit and New
// open the shared NoteEditorDialog, AI Tidy asks the service for a
// reorganization PROPOSAL that opens the SAME dialog — machine-generated
// wording is never saved silently; the user's save is the gate.
//
// Three runtime properties this file is responsible for:
//   · typing debounces (300 ms) and every search carries a sequence number, so
//     a slow early response can never overwrite the results of a later query;
//   · nothing fails silently — a transport error, a 4xx/5xx and an unavailable
//     web UI each become visible text in the panel, never just an empty list;
//   · the EDT never waits on the network: search and note reads run on
//     pooled threads, the pane is repainted from invokeLater.
package dev.repotory.jetbrains

import com.intellij.icons.AllIcons
import com.intellij.ide.BrowserUtil
import com.intellij.notification.NotificationAction
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.Disposable
import com.intellij.openapi.actionSystem.ActionGroup
import com.intellij.openapi.actionSystem.ActionManager
import com.intellij.openapi.actionSystem.ActionToolbar
import com.intellij.openapi.actionSystem.ActionUpdateThread
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.DefaultActionGroup
import com.intellij.openapi.actionSystem.ToggleAction
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.ide.CopyPasteManager
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.project.DumbAware
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.openapi.wm.ex.ToolWindowManagerEx
import com.intellij.ui.JBColor
import com.intellij.ui.SearchTextField
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBList
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.content.ContentFactory
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import java.awt.Color
import java.awt.Component
import java.awt.Cursor
import java.awt.Dimension
import java.awt.Font
import java.awt.Graphics
import java.awt.Graphics2D
import java.awt.RenderingHints
import java.awt.datatransfer.StringSelection
import java.awt.event.ComponentAdapter
import java.awt.event.ComponentEvent
import java.awt.event.MouseAdapter
import java.awt.event.MouseEvent
import java.awt.image.BufferedImage
import javax.imageio.ImageIO
import javax.swing.BorderFactory
import javax.swing.DefaultListModel
import javax.swing.Icon
import javax.swing.ImageIcon
import javax.swing.JEditorPane
import javax.swing.JLabel
import javax.swing.JList
import javax.swing.JPanel
import javax.swing.JSplitPane
import javax.swing.JTextArea
import javax.swing.ListSelectionModel
import javax.swing.Timer
import javax.swing.JViewport
import javax.swing.event.DocumentEvent
import javax.swing.event.DocumentListener
import kotlin.math.ceil

/** Trailing-edge debounce: the query runs 300 ms after the last keystroke. */
private const val DEBOUNCE_MS = 300

/** Narrower than this the list becomes unusable, so the split refuses to go below it. */
private val LIST_MIN_WIDTH = JBUI.scale(200)

/** Where the split puts the divider the first time the panel opens. */
private val LIST_DEFAULT_WIDTH = JBUI.scale(300)

/** The notification group the plugin posts its service problems to. */
private const val NOTIFICATION_GROUP = "Repotory"

/**
 * The state of the connection to the Repotory service.
 *
 * It is a closed set because the panel shows it as a pill and every state
 * needs its own wording: "offline" and "starting" mean different things to
 * the user, and collapsing them into "not connected" is how a reader ends up
 * waiting for a service that was never coming.
 */
private enum class LinkState {
    ONLINE,
    OFFLINE,
    STARTING,
    CHECKING,
}

/** Kind badge colours, paired light/dark so they follow the IDE theme. */
private val BADGE_NOTE = JBColor(Color(0x1D, 0x4E, 0xD8), Color(0x60, 0xA5, 0xFA))
private val BADGE_TODO = JBColor(Color(0x8B, 0x6F, 0x3A), Color(0xD9, 0xB7, 0x73))
private val BADGE_WIKI = JBColor(Color(0x6F, 0x42, 0xC1), Color(0xD2, 0xA8, 0xFF))

/**
 * The knowledge spaces the reader searches.
 *
 * Notes/todos and wiki pages have INDEPENDENT id sequences, so the two are
 * never mixed in one result list: a row saying "12" would be ambiguous, and
 * the deep link would open the wrong thing. One space on screen at a time.
 */
private enum class Source(val label: String, val hint: String) {
    NOTES("Notes", "Search notes and todos"),
    WIKI("Pages", "Search the approved knowledge pages"),
}

class RepotoryToolWindowFactory : ToolWindowFactory, DumbAware {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val panel = RepotoryPanel()
        val content = ContentFactory.getInstance().createContent(panel, "", false)
        content.setDisposer(panel)
        toolWindow.contentManager.addContent(content)
    }
}

class RepotoryPanel : JPanel(), Disposable {
    private val model = DefaultListModel<RepotoryHit>()
    private val list = JBList(model)
    private val detail = JEditorPane()
    private val status = StatusField()
    private val search = SearchTextField()
    private val pill = ConnectionPill()

    private val serviceToolbar: ActionToolbar
    private val sourceToolbar: ActionToolbar
    private val detailToolbar: ActionToolbar

    private var source = Source.NOTES
    private var disposed = false

    /**
     * Bumped for every search that is started or invalidated. A response whose
     * sequence number no longer matches is stale and is dropped: without it, a
     * slow "dep" response could land after a fast "deploy hook" one and repaint
     * the list with older results.
     */
    private var searchSeq = 0

    /** Same guard for the detail pane: a slow note read must not overwrite a newer selection. */
    private var detailSeq = 0

    /**
     * True once the panel has offered to locate the `repotory-mcp` binary.
     *
     * The offer is a MODAL dialog, and a modal nobody asked for interrupts
     * the work. It is made once: after the user declines, the panel keeps
     * working and keeps saying what is missing — without asking again on
     * every keystroke.
     */
    private var mcpHelpOffered = false

    /** True while a start is in flight, so a second click cannot double-spawn. */
    private var starting = false

    /** Keystrokes restart this timer; only the last one survives to fire. */
    private val debounce = Timer(DEBOUNCE_MS) { runSearch() }

    init {
        // JPanel ships with a FlowLayout; BorderLayout has to replace it
        // before the regions are added, and removeAll() clears the
        // default layout manager's single placeholder component.
        removeAll()
        layout = BorderLayout(0, 0)
        debounce.isRepeats = false

        serviceToolbar = buildToolbar("RepotoryService", serviceActions())
        sourceToolbar = buildToolbar("RepotorySource", sourceActions())
        detailToolbar = buildToolbar("RepotoryDetail", detailActions())

        add(headerStrip(), BorderLayout.NORTH)
        add(readerBody(), BorderLayout.CENTER)
        add(statusBar(), BorderLayout.SOUTH)

        pill.onClick = { reconnect() }
        search.toolTipText = Source.NOTES.hint
        // The pill must be honest before the first search: an open panel that
        // says "checking…" forever is worse than one that says "offline".
        probeConnection()
    }

    // ------------------------------------------------------------- header strip

    /**
     * The top band: the mark, the wordmark, the connection pill and the two
     * service actions.
     *
     * A slim native strip, not a printed card. The old cream band was the
     * brand's own colour, which meant it ignored the IDE theme AND had a
     * fixed height that a narrow window stretched; a strip with flexible
     * regions reflows instead.
     */
    private fun headerStrip(): JPanel {
        val header = JPanel(BorderLayout(JBUI.scale(8), 0))
        header.border = JBUI.Borders.compound(
            JBUI.Borders.customLineBottom(JBColor.border()),
            JBUI.Borders.empty(5, 8),
        )

        val brand = JPanel(BorderLayout(JBUI.scale(6), 0))
        brand.isOpaque = false
        markLabel(JBUI.scale(18))?.let { brand.add(it, BorderLayout.WEST) }
        val wordmark = JLabel("Repotory")
        wordmark.font = wordmark.font.deriveFont(Font.BOLD)
        brand.add(wordmark, BorderLayout.CENTER)

        val service = JPanel(BorderLayout(JBUI.scale(6), 0))
        service.isOpaque = false
        service.add(serviceToolbar.component, BorderLayout.WEST)
        service.add(pill, BorderLayout.EAST)

        header.add(brand, BorderLayout.WEST)
        header.add(service, BorderLayout.EAST)
        return header
    }

    /** The brand mark as an ImageIcon, scaled from the bundled PNG (Swing has no SVG). */
    private fun markLabel(size: Int): JLabel? {
        val stream = javaClass.getResourceAsStream("/icons/repotory-mark.png") ?: return null
        val image: BufferedImage = ImageIO.read(stream) ?: return null
        val scaled = image.getScaledInstance(size, size, java.awt.Image.SCALE_SMOOTH)
        return JLabel(ImageIcon(scaled))
    }

    // ------------------------------------------------------------- toolbars

    /**
     * A platform toolbar for [group].
     *
     * The platform draws the buttons (hover, disabled, keyboard), so the
     * panel does not have to keep its own copies of that behaviour.
     */
    private fun buildToolbar(place: String, group: ActionGroup): ActionToolbar {
        val toolbar = ActionManager.getInstance().createActionToolbar(place, group, true)
        toolbar.targetComponent = this
        val component = toolbar.component
        component.isOpaque = false
        component.border = JBUI.Borders.empty()
        return toolbar
    }

    /** Refresh (re-probe and re-search) and Start (bring the local service up). */
    private fun serviceActions(): ActionGroup = DefaultActionGroup(
        object : AnAction("Refresh", "Re-check the service and search again", AllIcons.Actions.Refresh), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun actionPerformed(e: AnActionEvent) = reconnect()
        },
        object : AnAction("Start local service", "Start the local Repotory service", AllIcons.Actions.Execute), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun update(e: AnActionEvent) {
                // Always available: the local service is an OPTIONAL
                // enhancement (deep links, moving a note between projects),
                // not the panel's data channel.
                e.presentation.isEnabled = !starting
            }

            override fun actionPerformed(e: AnActionEvent) = startLocalService()
        },
    )

    /**
     * The space switch: notes on one side, approved wiki pages on the other.
     *
     * Two toggles rather than one combined list, because the two spaces have
     * independent id sequences — a merged list would make "page 12" ambiguous.
     * ToggleAction is used instead of a pair of JToggleButtons because the
     * platform keeps exactly one of them lit: a plain toggle button switches
     * itself OFF when it is clicked again, which would leave the reader with
     * no source at all.
     */
    private fun sourceActions(): ActionGroup = DefaultActionGroup(
        object : ToggleAction(Source.NOTES.label, Source.NOTES.hint, null), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun isSelected(e: AnActionEvent): Boolean = source == Source.NOTES

            override fun setSelected(e: AnActionEvent, state: Boolean) {
                if (state) switchSource(Source.NOTES)
            }
        },
        object : ToggleAction(Source.WIKI.label, Source.WIKI.hint, null), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun isSelected(e: AnActionEvent): Boolean = source == Source.WIKI

            override fun setSelected(e: AnActionEvent, state: Boolean) {
                if (state) switchSource(Source.WIKI)
            }
        },
    )

    /**
     * The per-item actions: edit, new, AI tidy, copy, open in Repotory.
     *
     * They live above the detail pane because they act on the SELECTED item,
     * and the selected item is what the detail pane shows.
     */
    private fun detailActions(): ActionGroup = DefaultActionGroup(
        object : AnAction("Edit", "Edit the selected note", AllIcons.Actions.Edit), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun update(e: AnActionEvent) {
                e.presentation.isEnabled = selectedNote() != null
            }

            override fun actionPerformed(e: AnActionEvent) = editSelected()
        },
        object : AnAction("New note", "Add a note under the same project", AllIcons.General.Add), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun update(e: AnActionEvent) {
                e.presentation.isEnabled = selectedNote() != null
            }

            override fun actionPerformed(e: AnActionEvent) = newNoteUnderSelectedProject()
        },
        object : AnAction("AI Tidy", "Let the AI restructure this note — review the proposal, then save", AllIcons.Actions.Lightning), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun update(e: AnActionEvent) {
                e.presentation.isEnabled = selectedNote() != null
            }

            override fun actionPerformed(e: AnActionEvent) = organizeSelected()
        },
        object : AnAction("Copy", "Copy the full text to the clipboard", AllIcons.Actions.Copy), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun update(e: AnActionEvent) {
                e.presentation.isEnabled = list.selectedValue != null
            }

            override fun actionPerformed(e: AnActionEvent) = copySelected()
        },
        object : AnAction("Open in Repotory", "Open this item in the Repotory app", AllIcons.General.Web), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun update(e: AnActionEvent) {
                e.presentation.isEnabled = list.selectedValue != null
            }

            override fun actionPerformed(e: AnActionEvent) = openSelected()
        },
    )

    // ------------------------------------------------------------- reader body

    /** The query row and the two-column split: results on the left, the item on the right. */
    private fun readerBody(): JPanel {
        val body = JPanel(BorderLayout(0, 0))
        body.add(searchRow(), BorderLayout.NORTH)

        list.selectionMode = ListSelectionModel.SINGLE_SELECTION
        list.cellRenderer = ThumbRenderer()
        list.setFixedCellHeight(JBUI.scale(58))
        list.emptyText.text = "Search your Repotory knowledge base"
        list.addMouseListener(object : MouseAdapter() {
            override fun mouseClicked(e: MouseEvent) {
                if (e.clickCount == 2) openSelected()
            }
        })
        list.addListSelectionListener { event ->
            if (event.valueIsAdjusting) return@addListSelectionListener
            // The toolbar's enabled state follows the selection, so it has to
            // be told the selection changed.
            detailToolbar.updateActionsAsync()
            showDetail(list.selectedValue)
        }
        val listPane = JBScrollPane(list)
        listPane.border = JBUI.Borders.customLineRight(JBColor.border())
        listPane.minimumSize = Dimension(LIST_MIN_WIDTH, 0)

        detail.contentType = "text/html"
        detail.isEditable = false
        detail.editorKit = detailHtmlKit()
        detail.text = promoHtml()
        detail.border = JBUI.Borders.empty(10, 14)

        val detailArea = JPanel(BorderLayout(0, 0))
        detailArea.add(detailToolbar.component, BorderLayout.NORTH)
        detailArea.add(JBScrollPane(detail), BorderLayout.CENTER)
        detailArea.minimumSize = Dimension(JBUI.scale(220), 0)

        // A split pane, not a fixed-width column: the divider follows the
        // window when it is resized and can be dragged by the user, so the
        // two halves never squeeze each other into illegibility.
        val split = JSplitPane(JSplitPane.HORIZONTAL_SPLIT, listPane, detailArea)
        split.border = JBUI.Borders.empty()
        split.isOneTouchExpandable = true
        split.resizeWeight = 0.38
        split.dividerLocation = LIST_DEFAULT_WIDTH
        body.add(split, BorderLayout.CENTER)
        return body
    }

    /** The query field and the space switch, on one row. */
    private fun searchRow(): JPanel {
        val row = JPanel(BorderLayout(JBUI.scale(6), 0))
        row.border = JBUI.Borders.compound(
            JBUI.Borders.customLineBottom(JBColor.border()),
            JBUI.Borders.empty(4, 8),
        )
        search.addDocumentListener(object : DocumentListener {
            override fun insertUpdate(e: DocumentEvent) = scheduleSearch()
            override fun removeUpdate(e: DocumentEvent) = scheduleSearch()
            override fun changedUpdate(e: DocumentEvent) = scheduleSearch()
        })
        row.add(search, BorderLayout.CENTER)
        row.add(sourceToolbar.component, BorderLayout.EAST)
        return row
    }

    /**
     * Switches the space the reader searches.
     *
     * The rows on screen belong to the other space's id sequence, so they are
     * cleared before the new query runs — leaving them on screen next to a
     * result list of a different kind of thing is how "id 12" becomes
     * ambiguous.
     */
    private fun switchSource(next: Source) {
        if (source == next) return
        source = next
        model.clear()
        // The toggle button that is lit has to follow the switch.
        sourceToolbar.updateActionsAsync()
        // Both in-flight answers answer the OTHER space: they must not land.
        searchSeq++
        detailSeq++
        detail.text = promoHtml()
        detail.caretPosition = 0
        status.text = " "
        search.toolTipText = next.hint
        val query = search.text.trim()
        if (query.isNotEmpty()) runSearch()
    }

    // ------------------------------------------------------------- status bar

    private fun statusBar(): JPanel {
        val bar = JPanel(BorderLayout(0, 0))
        bar.border = JBUI.Borders.compound(
            JBUI.Borders.customLineTop(JBColor.border()),
            JBUI.Borders.empty(4, 8),
        )
        bar.add(status, BorderLayout.CENTER)
        return bar
    }

    /**
     * The status line's text field.
     *
     * It was a JLabel: a label cannot wrap, so a long message was either
     * clipped or — worse — claimed the whole width of a BorderLayout row and
     * pushed the buttons off the panel. This field wraps instead, and it
     * grows to fit the text it holds.
     *
     * JTextArea does not size itself to its content, so the row count is
     * derived from the text and the current width, and recomputed whenever
     * the panel is resized (a narrower tool window wraps into more lines).
     */
    private class StatusField : JTextArea() {
        init {
            isEditable = false
            isOpaque = false
            isFocusable = false
            lineWrap = true
            wrapStyleWord = true
            border = JBUI.Borders.empty(0)
            // The panel's other text is JLabel text; the default JTextArea
            // font would be the editor font (monospace) and look foreign.
            font = JBLabel().font
            rows = 1
            addComponentListener(object : ComponentAdapter() {
                override fun componentResized(e: ComponentEvent) {
                    fit()
                }
            })
        }

        override fun setText(t: String?) {
            super.setText(t)
            fit()
        }

        /** Rows the wrapped text needs at the width this field currently has. */
        private fun fit() {
            val needed = rowsFor(text)
            if (needed != rows) rows = needed
        }

        private fun rowsFor(value: String): Int {
            if (value.isBlank()) return 1
            // ~6px per character at the label font. Before the first layout
            // the width is still zero, so a scaled guess stands in.
            val usable = if (width > 0) width else JBUI.scale(280)
            val perLine = maxOf(16, usable / 6)
            return ceil(value.length.toDouble() / perLine).toInt().coerceIn(1, 3)
        }
    }

    // ------------------------------------------------------------- search

    private fun scheduleSearch() {
        if (disposed) return
        val query = search.text.trim()
        if (query.isEmpty()) {
            debounce.stop()
            // Invalidate any in-flight response: it answers a query the user
            // has already erased, and repainting the list with it would look
            // like a bug.
            searchSeq++
            model.clear()
            status.text = " "
            return
        }
        status.text = "Searching…"
        debounce.restart()
    }

    private fun runSearch() {
        if (disposed) return
        val query = search.text.trim()
        if (query.isEmpty()) return
        val seq = ++searchSeq
        // Captured before the request goes out: the answer belongs to the
        // space it was asked for, and a switch in between must not relabel it.
        val space = source
        status.text = "Searching \"$query\"…"
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            // MCP is the plugin's data channel (ADR-0035): searching does
            // not need a running service at all.
            val outcome = if (space == Source.WIKI) {
                repotoryMcp.searchWiki(query)
            } else {
                repotoryMcp.search(query)
            }
            app.invokeLater {
                // A newer keystroke (or a cleared box) supersedes this response.
                if (disposed || seq != searchSeq) return@invokeLater
                when (outcome) {
                    is SearchOutcome.Hits -> {
                        model.clear()
                        outcome.hits.forEach { model.addElement(it) }
                        // Default selection: the first hit, so the detail pane
                        // is never empty while results are on screen.
                        if (outcome.hits.isNotEmpty()) list.selectedIndex = 0
                        pill.state = LinkState.ONLINE
                        status.text = if (outcome.hits.isEmpty()) {
                            "No matches for \"$query\""
                        } else {
                            "${outcome.hits.size} result(s) for \"$query\""
                        }
                    }
                    is SearchOutcome.Failure -> {
                        model.clear()
                        // The CHANNEL is down (ADR-0035). The usual cause is a
                        // missing repotory-mcp binary, and that has a fix: a
                        // path. The local service is not involved any more —
                        // searching never needed it.
                        if (repotoryMcp.problem != null) {
                            pill.state = LinkState.OFFLINE
                            status.text = outcome.message
                            offerMcpBinaryOnce()
                        } else {
                            status.text = outcome.message
                        }
                    }
                }
            }
        }
    }

    // ------------------------------------------------------------- detail pane

    /**
     * Expands the selected hit. Notes get their full body over GetNote;
     * wiki pages over GetWikiPage; todos (and failed reads) show the matched
     * excerpt, labelled.
     *
     * The pane shows the excerpt immediately — a reader that blanks out
     * while the note loads reads as a bug — and swaps in the full body when
     * it arrives. detailSeq drops a read that a newer selection overtook.
     */
    private fun showDetail(hit: RepotoryHit?) {
        val seq = ++detailSeq
        if (hit == null) {
            detail.text = promoHtml()
            detail.caretPosition = 0
            return
        }
        detail.text = loadingHtml(hit)
        detail.caretPosition = 0
        if (hit.type == "wiki") {
            // An approved page: the body plus the graph around it (ADR-0032).
            val app = ApplicationManager.getApplication()
            app.executeOnPooledThread {
                val page = repotoryMcp.wikiPage(hit.id)
                app.invokeLater {
                    if (disposed || seq != detailSeq) return@invokeLater
                    detail.text = if (page != null) wikiPageHtml(page) else excerptHtml(hit, full = true)
                    detail.caretPosition = 0
                }
            }
            return
        }
        if (hit.type != "note") {
            detail.text = excerptHtml(hit)
            return
        }
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val note = repotoryMcp.note(hit.id)
            app.invokeLater {
                if (disposed || seq != detailSeq) return@invokeLater
                detail.text = if (note != null) noteHtml(note) else excerptHtml(hit, full = true)
                detail.caretPosition = 0
            }
        }
    }

    // ------------------------------------------------------------- connection

    /**
     * Asks the service whether it is there, and paints the answer on the pill.
     *
     * The probe is asked on its own thread and answered on the EDT: the panel
     * must never wait on a socket.
     */
    private fun probeConnection() {
        if (disposed) return
        pill.state = LinkState.CHECKING
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            // The channel the panel actually depends on is MCP (ADR-0035),
            // not the local service: search, read, write and organize all
            // work with no server running. The probe asks whether the MCP
            // binary is there — cheap, and it spawns nothing.
            val resolution = McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
            app.invokeLater {
                if (disposed) return@invokeLater
                if (resolution.found) {
                    pill.state = LinkState.ONLINE
                } else {
                    pill.state = LinkState.OFFLINE
                    status.text = resolution.problem ?: "repotory-mcp was not found."
                }
            }
        }
    }

    /**
     * The pill was clicked (or Refresh was pressed): check the service and,
     * if it is up, run the current query again.
     */
    private fun reconnect() {
        if (disposed) return
        pill.state = LinkState.CHECKING
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val resolution = McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
            app.invokeLater {
                if (disposed) return@invokeLater
                if (resolution.found) {
                    pill.state = LinkState.ONLINE
                    val query = search.text.trim()
                    if (query.isNotEmpty()) runSearch() else status.text = "Repotory MCP channel ready."
                } else {
                    pill.state = LinkState.OFFLINE
                    status.text = resolution.problem ?: "repotory-mcp was not found."
                    offerMcpBinaryOnce()
                }
            }
        }
    }

    /**
     * Offer to locate the `repotory-mcp` binary — once per panel.
     *
     * The MCP channel is the plugin's primary dependency since ADR-0035, so a
     * missing binary is the one failure the user can fix by pointing at a
     * file. The notice lists every directory that was tried: "not found"
     * without the list is not actionable.
     *
     * A BALLOON, not a modal dialog: the channel can be down on every
     * keystroke while the user types, and a dialog nobody asked for would
     * interrupt that work. This is the same posture the VS Code extension
     * takes — a notification with buttons, not a blocking dialog.
     */
    private fun offerMcpBinaryOnce() {
        if (mcpHelpOffered) return
        mcpHelpOffered = true
        val manager = NotificationGroupManager.getInstance()
        if (!manager.isGroupRegistered(NOTIFICATION_GROUP)) return
        val resolution = McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        val notification = manager.getNotificationGroup(NOTIFICATION_GROUP).createNotification(
            "Repotory: no MCP binary",
            "The plugin could not find repotory-mcp — it is what the panel talks to.\n" +
                resolution.attempts.take(8).joinToString("\n"),
            NotificationType.WARNING,
        )
        notification.addAction(
            NotificationAction.createSimple("Locate…") {
                chooseMcpBinary()?.let {
                    RepotorySettings.setMcpBinaryPath(it.absolutePath)
                    // The client points at the old path (or never started):
                    // a new setting has to take effect now, not at the next
                    // IDE restart.
                    repotoryMcp.shutdown()
                    reconnect()
                }
            },
        )
        notification.addAction(
            NotificationAction.createSimple("Open settings") {
                ShowSettingsUtil.getInstance().showSettingsDialog(null, "dev.repotory.jetbrains.settings")
            },
        )
        notification.notify(null)
    }

    /**
     * Starts the local Repotory service and retries the query.
     *
     * The local service is an OPTIONAL enhancement since ADR-0035 decision 6:
     * the panel's data comes over MCP, and the service is only needed for the
     * two things that stay on HTTP — deep links into the web UI, and moving a
     * note between projects.
     *
     * The spawn waits on the binary and on /health, so it runs on a pooled
     * thread; the EDT only gets the outcome. Nothing is downloaded: when no
     * binary exists, the user is offered the file picker or the release page.
     */
    private fun startLocalService() {
        if (disposed || starting) return
        starting = true
        pill.state = LinkState.STARTING
        status.text = "Starting the local Repotory service…"
        val serverUrl = RepotorySettings.serverUrl()
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val outcome = repotoryServer.start(serverUrl) { message ->
                app.invokeLater {
                    if (!disposed) status.text = message
                }
            }
            app.invokeLater {
                if (disposed) return@invokeLater
                starting = false
                when (outcome) {
                    is StartOutcome.Started -> {
                        if (!outcome.healthy) {
                            // The process is up but /health never answered:
                            // say so instead of promising a service that is
                            // not there yet.
                            pill.state = LinkState.STARTING
                            status.text = "The server process is running but is not answering yet — " +
                                "click the status pill to retry."
                        } else {
                            pill.state = LinkState.ONLINE
                            status.text = if (outcome.webUi) {
                                "Service on port ${outcome.port}. Searching again…"
                            } else {
                                "Service on port ${outcome.port} (API-only — “Open in Repotory” would 404). " +
                                    "Searching again…"
                            }
                            runSearch()
                        }
                    }
                    StartOutcome.AlreadyRunning -> {
                        pill.state = LinkState.ONLINE
                        status.text = "The service is already running. Searching again…"
                        runSearch()
                    }
                    is StartOutcome.BinaryMissing -> {
                        pill.state = LinkState.OFFLINE
                        status.text = "No repotory-server binary found — set its path in " +
                            "Settings | Tools | Repotory."
                        // The user PRESSED the button, so a dialog is welcome
                        // here — this is not the panel interrupting itself. It
                        // offers the file picker and the release page, the
                        // same two ways out the VS Code extension gives.
                        if (offerServerBinaryHelp(outcome.searched)) {
                            startLocalService()
                        }
                    }
                    is StartOutcome.Failed -> {
                        pill.state = LinkState.OFFLINE
                        status.text = outcome.message
                    }
                }
            }
        }
    }


    // ------------------------------------------------------------- actions

    /** The selected hit, when it is a note (a todo has no note row to edit). */
    private fun selectedNote(): RepotoryHit? = list.selectedValue?.takeIf { it.type == "note" }

    private fun copySelected() {
        val hit = list.selectedValue ?: return
        val app = ApplicationManager.getApplication()
        // The full body is one loopback call away, but the EDT must not wait
        // for it: copy on a pooled thread, then report on the EDT.
        app.executeOnPooledThread {
            val body = when (hit.type) {
                "note" -> repotoryMcp.note(hit.id)?.body
                "wiki" -> repotoryMcp.wikiPage(hit.id)?.content
                else -> null
            }
            val text = buildString {
                appendLine(hit.title.ifBlank { "(untitled)" })
                appendLine()
                append(body ?: snippetPlain(hit.snippet, 2000))
            }
            app.invokeLater {
                if (disposed) return@invokeLater
                CopyPasteManager.getInstance().setContents(StringSelection(text))
                status.text = "Copied to clipboard"
            }
        }
    }

    // ------------------------------------------------------- maintenance

    /**
     * Edit the selected note. The hit is re-read (it carries only the
     * excerpt), the shared editor dialog opens, and an OK saves fields first
     * and the project change second — a failed move must not lose the
     * edit the user just made.
     */
    private fun editSelected() {
        val hit = selectedNote() ?: return
        status.text = "Loading note #${hit.id}…"
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val note = repotoryMcp.note(hit.id)
            val projects = repotoryMcp.projects()
            app.invokeLater {
                if (disposed) return@invokeLater
                if (note == null) {
                    status.text = "Note #${hit.id} could not be read — nothing to edit"
                    return@invokeLater
                }
                openEditor(
                    NoteDraft(
                        id = note.id,
                        projectId = note.projectId,
                        title = note.title,
                        tags = note.tags,
                        kind = note.kind.ifBlank { "knowledge" },
                        content = note.body,
                        pinned = note.pinned,
                    ),
                    projects, proposal = false, warning = "",
                )
            }
        }
    }

    /** New note under the SAME project as the selected note. */
    private fun newNoteUnderSelectedProject() {
        val hit = selectedNote() ?: return
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val projects = repotoryMcp.projects()
            app.invokeLater {
                if (disposed) return@invokeLater
                openEditor(
                    NoteDraft(id = 0, projectId = hit.projectId, title = "", tags = "", kind = "knowledge", content = ""),
                    projects, proposal = false, warning = "",
                )
            }
        }
    }

    /**
     * AI tidy: ask the service for a reorganization PROPOSAL and open the
     * SAME editor with it. Nothing is written by the service; the user's save
     * is the gate. The stored note is re-read so kind and pinned carry into
     * the draft (the proposal covers title/tags/content only).
     */
    private fun organizeSelected() {
        val hit = selectedNote() ?: return
        status.text = "AI tidying note #${hit.id}… (this can take a while)"
        val app = ApplicationManager.getApplication()
        val reportFailure = { message: String ->
            app.invokeLater {
                if (disposed) return@invokeLater
                status.text = message
            }
        }
        app.executeOnPooledThread {
            val proposal = try {
                repotoryMcp.organizeNote(hit.id)
            } catch (e: McpException) {
                reportFailure(e.message ?: "整理失败")
                return@executeOnPooledThread
            } catch (e: Exception) {
                reportFailure("整理失败：${e.message ?: e.javaClass.simpleName}")
                return@executeOnPooledThread
            }
            val note = repotoryMcp.note(hit.id)
            val projects = repotoryMcp.projects()
            app.invokeLater {
                if (disposed) return@invokeLater
                openEditor(
                    NoteDraft(
                        id = if (proposal.noteId > 0) proposal.noteId else hit.id,
                        projectId = note?.projectId ?: hit.projectId,
                        title = proposal.title,
                        tags = proposal.tags,
                        kind = note?.kind?.ifBlank { "knowledge" } ?: "knowledge",
                        content = proposal.content,
                        pinned = note?.pinned ?: false,
                    ),
                    projects, proposal = true, warning = proposal.warning,
                )
            }
        }
    }

    /** Opens the shared editor; an OK saves through [saveDraft]. */
    private fun openEditor(
        original: NoteDraft,
        projects: List<ProjectOption>,
        proposal: Boolean,
        warning: String,
    ) {
        val dialog = NoteEditorDialog(null, original, projects, proposal, warning)
        if (!dialog.showAndGet()) {
            status.text = "Edit cancelled — nothing saved"
            return
        }
        saveDraft(original, dialog.editedDraft())
    }

    /**
     * Persists an edited draft: UpdateNoteFull, then MoveNote when the project
     * changed. The detail pane is repainted from the freshly stored note, so
     * what is on screen is what is in the database.
     */
    private fun saveDraft(original: NoteDraft, edited: NoteDraft) {
        val serverUrl = RepotorySettings.serverUrl()
        status.text = if (edited.id == 0L) "Creating note…" else "Saving note #${edited.id}…"
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            if (edited.id == 0L) {
                val created = try {
                    repotoryMcp.createNote(edited)
                } catch (e: Exception) {
                    app.invokeLater {
                        if (disposed) return@invokeLater
                        status.text = "Create failed: ${e.message ?: e.javaClass.simpleName}"
                    }
                    return@executeOnPooledThread
                }
                app.invokeLater {
                    if (disposed) return@invokeLater
                    // The new note is not in the current result list; show it
                    // in full so the user sees exactly what was stored.
                    status.text = "Created note #${created.id}"
                    detail.text = noteHtml(created)
                    detail.caretPosition = 0
                }
                return@executeOnPooledThread
            }
            val updated = try {
                repotoryMcp.updateNote(edited)
            } catch (e: Exception) {
                app.invokeLater {
                    if (disposed) return@invokeLater
                    status.text = "Save failed: ${e.message ?: e.javaClass.simpleName}"
                }
                return@executeOnPooledThread
            }
            // MoveNote stays on HTTP on purpose (ADR-0035 decision 2): moving
            // a note between projects is a human decision, not an agent's. It
            // is the ONE write that still needs the local service — and when
            // the service is down the fields ARE saved, so the wording must
            // say "the move failed", never "the save failed".
            val moveError = if (original.projectId != 0L && edited.projectId != original.projectId) {
                try {
                    repotoryClient.moveNote(serverUrl, edited.id, edited.projectId)
                    null
                } catch (e: Exception) {
                    e.message ?: e.javaClass.simpleName
                }
            } else {
                null
            }
            // The MCP tool answers with the note as stored, so the pane is
            // repainted from what was actually written — one round trip less
            // than reading it back.
            val fresh = updated
            // The MCP surface cannot CLEAR a title (an empty value means
            // "keep what is there"), so a title the user emptied survives the
            // write. Saying so beats reporting a success that did not do
            // what was asked.
            val titleKept = edited.title.isEmpty() && updated.title.isNotEmpty()
            app.invokeLater {
                if (disposed) return@invokeLater
                status.text = when {
                    moveError != null ->
                        "Fields saved, but the project move failed: $moveError " +
                            "(moving a note between projects needs the local Repotory service)."
                    titleKept ->
                        "Saved note #${edited.id}, but the title could not be cleared — " +
                            "the MCP write path keeps the old title."
                    else -> "Saved note #${edited.id}"
                }
                detail.text = noteHtml(fresh)
                detail.caretPosition = 0
            }
        }
    }

    private fun openSelected() {
        val hit = list.selectedValue ?: return
        val serverUrl = RepotorySettings.serverUrl()
        val url = repotoryClient.deepLink(serverUrl, hit)
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            // Asked before every jump rather than cached: the answer is cheap
            // (one loopback GET) and it changes the moment the user restarts
            // the server with --web-dist.
            val servesUi = repotoryClient.servesWebUi(serverUrl)
            app.invokeLater {
                if (disposed) return@invokeLater
                if (servesUi) {
                    status.text = "Opened $url"
                    BrowserUtil.browse(url)
                } else {
                    status.text = "Repotory is running API-only — this link would land on a 404"
                    offerFallback(url)
                }
            }
        }
    }

    /**
     * The headless server serves its web UI only when it was started with
     * `--web-dist` (cmd/server/main.go); without it every non-/api path is
     * a 404 and the deep link goes nowhere. A server the plugin started carries
     * its sibling `web/` automatically (RepotoryServer.kt); for one the user
     * started by hand, the plugin cannot add the flag — but it can stop the
     * user from landing on a bare 404 with no explanation.
     */
    private fun offerFallback(url: String) {
        val choice = Messages.showDialog(
            "Repotory answered, but it is not serving its web UI, so this link lands on a 404 page.\n\n" +
                "Either restart the running server with:\n" +
                "  repotory-server --port 18765 --web-dist <release bundle>/web\n" +
                "or stop it and press the start button, which launches the release bundle " +
                "together with its web/ directory.\n\n" +
                "URL: $url",
            "Repotory: web UI not available",
            arrayOf("Open Anyway", "Copy URL", "Cancel"),
            2,
            Messages.getWarningIcon(),
        )
        when (choice) {
            0 -> BrowserUtil.browse(url)
            1 -> {
                CopyPasteManager.getInstance().setContents(StringSelection(url))
                status.text = "URL copied to clipboard"
            }
            else -> status.text = "Cancelled — the link would have 404ed"
        }
    }

    override fun dispose() {
        disposed = true
        debounce.stop()
    }
}

/**
 * The connection pill: a coloured dot, the state, and a click that reconnects.
 *
 * It replaces the old "start the service" button, which only appeared after
 * a search had already failed. The pill is always visible, so the reader
 * always knows whether the service is there — and clicking it is the way to
 * try again.
 */
private class ConnectionPill : JPanel(BorderLayout(JBUI.scale(5), 0)) {
    private val dotIcon = ColorDotIcon(JBUI.scale(8), JBColor.foreground())
    private val dot = JLabel(dotIcon)
    private val label = JLabel("checking…")

    /** Called when the user clicks the pill. */
    var onClick: (() -> Unit)? = null

    var state: LinkState = LinkState.CHECKING
        set(value) {
            field = value
            render()
        }

    init {
        isOpaque = false
        add(dot, BorderLayout.WEST)
        add(label, BorderLayout.CENTER)
        cursor = Cursor.getPredefinedCursor(Cursor.HAND_CURSOR)
        addMouseListener(object : MouseAdapter() {
            override fun mouseClicked(e: MouseEvent) {
                onClick?.invoke()
            }
        })
        render()
    }

    private fun render() {
        val (color, text, tip) = when (state) {
            LinkState.ONLINE -> Triple(
                JBColor(Color(0x3F, 0x8F, 0x4E), Color(0x73, 0xBD, 0x79)),
                "online",
                "The Repotory MCP channel is ready — searching, reading and writing work now. " +
                    "The local service is only needed for deep links and moving a note between projects. " +
                    "Click to re-check.",
            )
            LinkState.OFFLINE -> Triple(
                JBColor(Color(0xC0, 0x3A, 0x2B), Color(0xE8, 0x6C, 0x5A)),
                "offline",
                "The Repotory MCP channel is not available — repotory-mcp was not found. " +
                    "Click to locate it.",
            )
            LinkState.STARTING -> Triple(
                JBColor(Color(0xB4, 0x8A, 0x2F), Color(0xE2, 0xC0, 0x6A)),
                "starting…",
                "The local Repotory service is starting. Click to re-check.",
            )
            LinkState.CHECKING -> Triple(
                JBColor.foreground(),
                "checking…",
                "Checking whether repotory-mcp is available.",
            )
        }
        dotIcon.color = color
        label.text = text
        label.foreground = color
        toolTipText = tip
        border = JBUI.Borders.compound(
            BorderFactory.createLineBorder(color),
            JBUI.Borders.empty(2, 6),
        )
    }
}

/** A filled dot; the colour is set by the pill when the state changes. */
private class ColorDotIcon(private val size: Int, var color: Color) : Icon {
    override fun getIconWidth(): Int = size

    override fun getIconHeight(): Int = size

    override fun paintIcon(c: Component, g: Graphics, x: Int, y: Int) {
        val g2 = g.create() as Graphics2D
        try {
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            g2.color = color
            g2.fillOval(x, y, size, size)
        } finally {
            g2.dispose()
        }
    }
}

/**
 * A label that shortens its text with an ellipsis instead of cutting it.
 *
 * A plain JLabel simply draws past its own bounds — the words are chopped
 * mid-word with no sign that something was cut. An ellipsis says "there is
 * more" instead.
 */
private class ElidingLabel(full: String) : JLabel() {
    private var fullText: String = full

    init {
        text = full
    }

    override fun setSize(w: Int, h: Int) {
        super.setSize(w, h)
        elide()
    }

    private fun elide() {
        if (width <= 0) return
        val metrics = getFontMetrics(font)
        if (metrics.stringWidth(fullText) <= width) {
            if (text != fullText) text = fullText
            return
        }
        var cut = fullText
        while (cut.isNotEmpty() && metrics.stringWidth(cut + "…") > width) {
            cut = cut.dropLast(1)
        }
        val elided = if (cut.isEmpty()) "…" else "$cut…"
        if (text != elided) text = elided
    }
}

/**
 * One thumbnail: kind badge, title, matched excerpt.
 *
 * A JPanel (not a JLabel) so the badge and the title sit on one row with
 * different colours; opaque, because a transparent renderer shows the
 * list's own stripes through the card. Selection colours come from the
 * JList, so the row follows the IDE theme.
 */
private class ThumbRenderer : JPanel(BorderLayout(JBUI.scale(6), 0)), javax.swing.ListCellRenderer<RepotoryHit> {
    private val badge = JLabel()
    private val title = ElidingLabel("")
    private val snippet = ElidingLabel("")

    /** The list this row belongs to; its viewport says how wide the row may be. */
    private var owner: JList<out RepotoryHit>? = null

    init {
        isOpaque = true
        border = JBUI.Borders.empty(6, 10)
        val row = JPanel(BorderLayout(JBUI.scale(6), 0))
        row.isOpaque = false
        badge.font = badge.font.deriveFont(JBUI.scaleFontSize(9f).toFloat())
        badge.border = JBUI.Borders.empty(1, 4)
        row.add(badge, BorderLayout.WEST)
        title.font = title.font.deriveFont(Font.BOLD, JBUI.scaleFontSize(12f).toFloat())
        row.add(title, BorderLayout.CENTER)
        add(row, BorderLayout.NORTH)
        snippet.font = snippet.font.deriveFont(JBUI.scaleFontSize(11f).toFloat())
        add(snippet, BorderLayout.CENTER)
    }

    override fun getListCellRendererComponent(
        list: JList<out RepotoryHit>?,
        value: RepotoryHit?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ): Component {
        val hit = value
        if (hit == null) {
            title.text = ""
            snippet.text = ""
            badge.text = ""
            return this
        }
        owner = list
        badge.text = badgeLabel(hit).uppercase()
        badge.foreground = when (hit.type) {
            "todo" -> BADGE_TODO
            "wiki" -> BADGE_WIKI
            else -> BADGE_NOTE
        }
        badge.border = BorderFactory.createCompoundBorder(
            BorderFactory.createLineBorder(badge.foreground, 1),
            JBUI.Borders.empty(1, 4),
        )
        title.text = hit.title.ifBlank { "(untitled)" }
        title.toolTipText = hit.title
        snippet.text = if (hit.snippet.isBlank()) " " else snippetPlain(hit.snippet)
        snippet.toolTipText = snippetPlain(hit.snippet, 600)

        if (isSelected) {
            background = list?.selectionBackground
            foreground = list?.selectionForeground
            title.foreground = list?.selectionForeground
            snippet.foreground = list?.selectionForeground
        } else {
            background = list?.background
            foreground = list?.foreground
            title.foreground = list?.foreground
            snippet.foreground = JBColor(Color(0x6A, 0x6A, 0x6A), Color(0x94, 0x94, 0x94))
        }
        return this
    }

    /**
     * The row is as wide as the pane shows it, not as wide as its text.
     *
     * A JList lays its cells out at the LIST's width, and the list's width
     * is its preferred width — which, for a row renderer, is the width of
     * the longest label. A long excerpt therefore made the list wider than
     * the pane, and the text ran under the split divider and was chopped in
     * half instead of ending in an ellipsis. Reporting the viewport's width
     * instead lays the row out exactly as wide as it is seen, so the
     * eliding labels do their job.
     */
    override fun getPreferredSize(): Dimension {
        val preferred = super.getPreferredSize()
        val visible = (owner?.parent as? JViewport)?.extentSize?.width ?: 0
        return if (visible > 0) Dimension(visible, preferred.height) else preferred
    }
}

/** "Open in Repotory" as a toolbar action, so the tool window has a title bar button. */
class OpenRepotoryAction : AnAction(), DumbAware {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabledAndVisible = e.project != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val toolWindow = ToolWindowManagerEx
            .getInstanceEx(project)
            .getToolWindow("Repotory") ?: return
        toolWindow.activate(null)
    }
}
