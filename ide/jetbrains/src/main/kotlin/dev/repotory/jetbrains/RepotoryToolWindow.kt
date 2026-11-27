// The Cinder chat console — the plugin's conversation panel.
//
// ADR-0036 (Accepted 2026-11-02) rewrote this panel: it used to be a
// SEARCHER (one query, a list of thumbnails, a detail pane) and it is now a
// CONSOLE. The layout is the one the VS Code extension ships, region for
// region:
//
//   ┌──────────────────────────────────────────────────────────┐
//   │ ▪ Cinder                                   ●  ☰  ＋  ⚙  │  ① header
//   ├──────────────────────────────────────────────────────────┤     (icon strip,
//   │ ⚠ announcement bar — one line, dismissible, only when     │  ② only when there
//   │   there is something to say                               │     is something to say)
//   ├──────────────────────────────────────────────────────────┤
//   │ ▸ 3 earlier turns                                        │  ④ folded history
//   │ [ASK] why does the gateway retry 503?                     │  ③ the feed
//   │       Because the backoff is capped at 30 s.              │     (the only
//   │       Sources (2) ▸                                       │      scroll area)
//   │ [NOTES] fts5 quirk                                       │
//   │       ┌ note #42 ──────────────────────────────┐          │
//   │       │ FTS5 quirk                             │          │
//   │       │ content tables answer…                 │          │
//   │       │ Ask about this · Edit · AI Tidy …    │          │
//   │       └────────────────────────────────────────┘          │
//   ├──────────────────────────────────────────────────────────┤
//   │ ┌ what do you want to know?                       ┌───┐ │  ⑤ the composer
//   │ │                                                │ ↑ │ │
//   │ └────────────────────────────────────────────────└───┘ │
//   │ (Ask)(Notes)(Pages)                     (⚿ pay-common)   │
//   └──────────────────────────────────────────────────────────┘
//
// The chrome is HIDDEN (2026-11-26, ADR-0044 revision): the header is a mark,
// a wordmark, one channel dot and three 24px icons; every setup-class action
// (locate the MCP binary, start the local service, configure the AI provider)
// lives in the settings tree the gear opens. The conversation row above the
// feed exists only in shared mode. Nothing else is drawn permanently — a
// 300px sidebar has room for the conversation or for chrome, not both.
// There is NO "open the desktop app" button: the panel is the product in this
// window, and a button whose only job is to send the reader elsewhere says
// the panel is a preview of the real thing.
//
// Why it changed: a result that REPLACES the previous one cannot be read as
// a sequence, and Cinder's value is in the sequence — a gotcha is the
// follow-up of a debugging session. The conversation feed keeps every turn,
// folds the old ones away, and opens an item IN PLACE (there is no second
// pane — ADR-0036 decision 7).
//
// The project lock is a DEFAULT, not a restriction: the chip opens and the
// panel goes back to cross-project. Ask and Pages really filter (the project
// id goes to the tool); Notes cannot (the notes tool has no project
// parameter), so the panel filters the hits itself and says how many it
// dropped.
//
// Three runtime properties this file is responsible for:
//   · nothing fails silently — a transport error, a refused tool and an
//     unreachable service each become visible text in the announcement bar,
//     never just an empty answer;
//   · the EDT never waits on the network: every call runs on a pooled thread
//     and the panel is repainted from invokeLater;
//   · the fold state belongs to the UI: a collapsed turn stays collapsed
//     while other turns arrive.
package dev.repotory.jetbrains

import com.intellij.ide.BrowserUtil
import com.intellij.notification.NotificationAction
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.Disposable
import com.intellij.openapi.actionSystem.ActionUpdateThread
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.project.DumbAware
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.DialogWrapper
import com.intellij.openapi.ui.Messages
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.openapi.wm.ex.ToolWindowManagerEx
import com.intellij.ui.JBColor
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.content.ContentFactory
import com.intellij.util.ui.JBUI
import com.intellij.util.ui.UIUtil
import java.awt.BorderLayout
import java.awt.Color
import java.awt.Component
import java.awt.Dimension
import java.awt.Font
import java.awt.Graphics
import java.awt.Graphics2D
import java.awt.FlowLayout
import java.awt.GridLayout
import java.awt.RenderingHints
import java.awt.event.FocusAdapter
import java.awt.event.FocusEvent
import java.awt.event.KeyAdapter
import java.awt.event.KeyEvent
import java.awt.image.BufferedImage
import java.util.concurrent.TimeUnit
import javax.imageio.ImageIO
import javax.swing.BoxLayout
import javax.swing.ImageIcon
import javax.swing.DefaultListCellRenderer
import javax.swing.JButton
import javax.swing.JComboBox
import javax.swing.JComponent
import javax.swing.JList
import javax.swing.Timer
import javax.swing.JEditorPane
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.JTextArea
import javax.swing.SwingUtilities
import javax.swing.UIManager
import javax.swing.event.HyperlinkEvent
import kotlin.math.ceil

/** The notification group the plugin posts its service problems to. */
private const val NOTIFICATION_GROUP = "Cinder"

class RepotoryToolWindowFactory : ToolWindowFactory, DumbAware {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val panel = RepotoryPanel(project)
        val content = ContentFactory.getInstance().createContent(panel, "", false)
        content.setDisposer(panel)
        toolWindow.contentManager.addContent(content)
    }
}

/**
 * The chat console.
 *
 * The panel owns the FOLD STATE and the chrome; the session owns the data.
 * Every click in the feed is an `<a href="repotory:…">`, dispatched by the
 * hyperlink listener — Swing has no DOM, so the href is the event bus.
 */
class RepotoryPanel(private val project: Project?) : JPanel(), Disposable {

    private val transport = McpChatTransport(project)
    private val session = ChatSession(transport, ::post)
    private val shared = SharedChatSession(transport) {
        ApplicationManager.getApplication().invokeLater { if (!disposed) renderShared() }
    }
    private val sharedPolling = SharedChatPolling(
        shared,
        execute = { work -> background(work) },
        onUi = { work -> ApplicationManager.getApplication().invokeLater(work) },
        schedule = { delay, work ->
            val timer = Timer(delay) { work() }.apply { isRepeats = false; start() }
            val stop: () -> Unit = { timer.stop() }
            stop
        },
    )
    private val sectionPicker = JComboBox(arrayOf("Local Ask / Notes / Pages", "Warden shared conversations"))
    private val threadPicker = JComboBox<ThreadChoice>()
    private val sharedControls = JPanel(BorderLayout(4, 4))
    private val sharedStatus = JLabel("Not synced")
    private var sharedSection = false
    private var updatingThreads = false
    private var displayedThread: String? = null
    private var localDraft = ""
    private val sharedDrafts = mutableMapOf<String?, String>()
    private val modeToggles = mutableMapOf<ChatMode, ModePill>()

    /**
     * The conversation picker, built once and hosted in a DIALOG.
     *
     * It used to sit permanently above the feed (a section combo, a thread
     * combo, New/Refresh and a status line), which spent the top of a narrow
     * tool window on chrome. It is now reached from the header's
     * "Conversations" action and from the topic bar, so the conversation keeps
     * the room.
     */
    private lateinit var pickerPanel: JPanel

    /**
     * One line above the feed naming the conversation in use.
     *
     * It exists ONLY in shared mode. The local console is the default and its
     * name is the panel's name; a permanent "Local console · Ask / Notes /
     * Pages" row said nothing the mode group and the header do not already
     * say (the VS Code panel has no such row at all).
     */
    private val topicLabel = JLabel("")

    /** The bar that hosts [topicLabel]; hidden while the local console is on. */
    private val topicBar = JPanel(BorderLayout(JBUI.scale(6), 0))

    /** Once-per-panel offer to open the AI provider settings (a 404 is fixable). */
    private var providerHelpOffered = false

    private data class ThreadChoice(val id: String?, val label: String) {
        override fun toString(): String = label
    }
    private val feed = JEditorPane()
    private val feedScroll = JBScrollPane(feed)
    private val announcement = AnnouncementBar()
    private val statusDot = StatusDot()

    /** The composer's text field. */
    private val inputField = ChatTextArea()

    /** The composer's scroll pane — it grows with the question. */
    private val fieldScroll = JBScrollPane(inputField)

    /** The composer's frame: the input and the send key in one rounded box. */
    private val fieldBox = ComposerField()

    /** The scope chip: which project the panel is locked to. */
    private val scopeChipButton = ChipButton("All projects", "Which project the panel is scoped to")

    /**
     * The collapsed turns, the expanded source lists and the opened cards.
     *
     * They live here, not in the session: a collapsed turn must stay
     * collapsed while other turns arrive (ADR-0036 decision 3). A re-render
     * reads them; it never resets them.
     */
    private val collapsedTurns = mutableSetOf<String>()
    private val expandedSources = mutableSetOf<String>()
    private val expandedCards = mutableSetOf<String>()

    /** Turns whose inline reasoning is open. Folded by default (Thinking ▸). */
    private val expandedThoughts = mutableSetOf<String>()

    /** Whether the folded history is open. */
    private var showEarlier = false

    /** The mode the composer sends to. */
    private var mode = ChatMode.ASK

    private var disposed = false

    /**
     * True once the panel has offered to locate the `repotory-mcp` binary.
     *
     * The offer is a notification, not a modal dialog, and it is made once:
     * after the user declines, the panel keeps working and keeps saying what
     * is missing — without asking again on every question.
     */
    private var mcpHelpOffered = false

    init {
        // JPanel ships with a FlowLayout; BorderLayout has to replace it
        // before the regions are added, and removeAll() clears the
        // default layout manager's single placeholder component.
        removeAll()
        layout = BorderLayout(0, 0)

        // Built before the header so the history button has something to
        // show; it is added to a dialog on demand, never to the panel.
        pickerPanel = conversationPicker()

        add(headerStrip(), BorderLayout.NORTH)
        add(conversationArea(), BorderLayout.CENTER)
        add(inputArea(), BorderLayout.SOUTH)

        statusDot.onClick = { reconnect() }
        feed.addHyperlinkListener { event -> onHyperlink(event) }
        announcement.onDismiss = { announcement.dismiss() }
        renderScope()
        renderMode()

        probeConnection()
        // The session comes up AFTER the panel is on screen: posting into a
        // half-built panel is the race the VS Code side had to solve with its
        // "ready" handshake, and the scope resolution needs the network, so it
        // cannot run on the EDT either.
        background { session.init() }
        addHierarchyListener { event ->
            if (event.changeFlags and java.awt.event.HierarchyEvent.SHOWING_CHANGED.toLong() != 0L) {
                sharedPolling.setVisible(isShowing && sharedSection)
            }
        }
    }

    // ----------------------------------------------------- shared conversations

    private fun conversationPicker(): JPanel {
        val panel = JPanel(BorderLayout(4, 4))
        panel.border = JBUI.Borders.empty(4, 8)
        panel.add(sectionPicker, BorderLayout.NORTH)
        sharedControls.add(threadPicker, BorderLayout.NORTH)
        val actions = JPanel(GridLayout(1, 3, 4, 0))
        actions.add(JButton("New conversation").apply { addActionListener { newSharedConversation() } })
        actions.add(JButton("Refresh").apply { addActionListener { sharedPolling.refreshNow() } })
        sharedControls.add(actions, BorderLayout.CENTER)
        sharedControls.add(sharedStatus, BorderLayout.SOUTH)
        sharedControls.isVisible = false
        panel.add(sharedControls, BorderLayout.CENTER)
        // Swing labels interpret a leading <html> unless explicitly disabled.
        threadPicker.renderer = object : DefaultListCellRenderer() {
            override fun getListCellRendererComponent(list: JList<*>?, value: Any?, index: Int,
                isSelected: Boolean, cellHasFocus: Boolean): Component {
                putClientProperty("html.disable", true)
                return super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus)
            }
        }
        threadPicker.addActionListener {
            if (!updatingThreads) {
                shared.select((threadPicker.selectedItem as? ThreadChoice)?.id)
                renderShared()
                sharedPolling.refreshNow()
            }
        }
        sectionPicker.addActionListener {
            if (sharedSection) sharedDrafts[displayedThread] = inputField.text else localDraft = inputField.text
            sharedSection = sectionPicker.selectedIndex == 1
            sharedControls.isVisible = sharedSection
            displayedThread = shared.snapshot().selectedId
            inputField.text = if (sharedSection) sharedDrafts[displayedThread].orEmpty() else localDraft
            scopeChipButton.isVisible = !sharedSection
            renderTopicLabel()
            renderMode()
            renderShared()
            renderFeed()
            sharedPolling.setVisible(isShowing && sharedSection)
        }
        return panel
    }

    /** The header's "Conversations" action: the picker, in a dialog. */
    private fun showConversations() {
        renderShared()
        PanelDialog("Cinder: Conversations", pickerPanel).show()
    }

    /** The header's "New" action and the picker's New button share this. */
    private fun newSharedConversation() {
        val title = Messages.showInputDialog(
            project,
            "Conversation title (shared with Cinder):",
            "New shared conversation",
            null,
        )?.trim().orEmpty()
        if (title.isNotEmpty()) background { shared.create(title) }
    }

    /**
     * One line above the feed naming the conversation in use — shared mode only.
     *
     * The picker moved into a dialog, so the panel must still say which
     * conversation an answer will land in: "New conversation" must never
     * change the destination silently. The local console needs no such line —
     * it IS the panel, and the mode group already names its three faces.
     */
    private fun renderTopicLabel() {
        val state = shared.snapshot()
        if (!sharedSection) {
            topicLabel.text = ""
            topicBar.isVisible = false
            return
        }
        val title = state.selected?.title?.takeIf { it.isNotBlank() }
        topicLabel.text = "Shared · " + (title ?: "no conversation selected")
        topicBar.isVisible = true
    }

    private fun renderShared() {
        val state = shared.snapshot()
        renderTopicLabel()
        if (sharedSection && displayedThread != state.selectedId) {
            sharedDrafts[displayedThread] = inputField.text
            displayedThread = state.selectedId
            inputField.text = sharedDrafts[displayedThread].orEmpty()
        }
        val choices = listOf(ThreadChoice(null, "Choose conversation · all workspaces")) +
            state.threads.sortedWith(compareByDescending<SharedThread> { it.updatedAt }.thenBy { it.id })
                .map { ThreadChoice(it.id, "${it.title} · ${it.path.ifBlank { "All projects" }}") }
        updatingThreads = true
        try {
            val existing = (0 until threadPicker.itemCount).map { threadPicker.getItemAt(it) }
            if (choices != existing) {
                threadPicker.removeAllItems()
                choices.forEach { threadPicker.addItem(it) }
            }
            threadPicker.selectedItem = choices.firstOrNull { it.id == state.selectedId }
        } finally {
            updatingThreads = false
        }
        sharedStatus.text = when {
            state.syncError.isNotEmpty() -> "Not synced — check/upgrade Cinder"
            state.pending.any { it.error.isNotBlank() } -> "Request failed — retained locally; retry in the feed"
            state.synced -> "Shared history refreshed · knowledge-base Q&A"
            else -> "Not synced yet · select a conversation and refresh"
        }
        if (sharedSection) renderFeed()
    }

    // ------------------------------------------------------------- header strip

    /**
     * The top band: the mark, the wordmark, the channel dot and three icon
     * buttons — the same strip the VS Code panel draws (ADR-0044 decision 2).
     *
     * It used to be two platform `ActionToolbar`s whose actions carry no icon,
     * so Swing drew them as TEXT buttons: "Refresh", "Start local service",
     * "Conversations", "New", "AI provider", "Desktop app" plus a bordered
     * `online` pill. Six words and a box, on the narrowest strip in the
     * product, said nothing a 24px glyph cannot. The actions did not change —
     * only where "Start local service" and "Refresh" live: setup and health
     * belong on the settings page, and the dot re-checks the channel.
     *
     * The "Desktop app" button is GONE (2026-11-26): the panel is the product
     * in this window, and a button whose only job is to send the reader
     * somewhere else says the panel is a preview of the real thing.
     */
    private fun headerStrip(): JPanel {
        val header = JPanel(BorderLayout(JBUI.scale(6), 0))
        header.border = JBUI.Borders.compound(
            JBUI.Borders.customLineBottom(JBColor.border()),
            JBUI.Borders.empty(3, 6),
        )

        val brand = JPanel(BorderLayout(JBUI.scale(6), 0))
        brand.isOpaque = false
        markLabel(JBUI.scale(16))?.let { brand.add(it, BorderLayout.WEST) }
        // The slogan line is gone (ADR-0044 decision 4): the panel says what
        // it does, not what the product is.
        val name = JLabel("Cinder")
        name.font = name.font.deriveFont(Font.BOLD)
        brand.add(name, BorderLayout.CENTER)

        // The strip: the channel dot, then the three actions the VS Code
        // panel draws — history, new conversation, settings.
        val strip = JPanel()
        strip.isOpaque = false
        strip.layout = BoxLayout(strip, BoxLayout.X_AXIS)
        strip.add(statusDot)
        strip.add(IconButton(PanelIcons.history(), "Conversations — local console or a shared one") {
            showConversations()
        })
        strip.add(IconButton(PanelIcons.plus(), "New shared conversation") { newSharedConversation() })
        strip.add(IconButton(PanelIcons.gear(), "Settings — channel, local service, AI provider") { openSettings() })

        header.add(brand, BorderLayout.WEST)
        header.add(strip, BorderLayout.EAST)
        return header
    }

    // ------------------------------------------------------- the header actions

    /** The gear: the plugin's settings page, with the provider page a step in. */
    private fun openSettings() {
        ShowSettingsUtil.getInstance().showSettingsDialog(project, RepotoryConfigurable::class.java)
    }

    /**
     * The AI provider page.
     *
     * It used to be a `DialogWrapper` reachable from the header (ADR-0044
     * decision 5). It is now a page of the settings tree: the endpoint, the
     * key and the model are configuration, and configuration has one home —
     * the same home as the MCP binary and the server address, so "where do I
     * change that?" has one answer. See ADR-0044's 2026-11-26 revision.
     */
    private fun openProviderSettings() {
        ShowSettingsUtil.getInstance().showSettingsDialog(project, RepotoryProviderConfigurable::class.java)
    }

    /** The brand mark as an ImageIcon, scaled from the bundled PNG (Swing has no SVG). */
    private fun markLabel(size: Int): JLabel? {
        val stream = javaClass.getResourceAsStream("/icons/repotory-mark.png") ?: return null
        val image: BufferedImage = ImageIO.read(stream) ?: return null
        val scaled = image.getScaledInstance(size, size, java.awt.Image.SCALE_SMOOTH)
        return JLabel(ImageIcon(scaled))
    }

    // --------------------------------------------------------- conversation area

    /**
     * The conversation bar and the feed.
     *
     * The feed is the ONLY scroll area (ADR-0036 decision 1): there is no
     * detail pane beside it, so a narrow tool window never squeezes a column
     * of text into something unreadable. The bar above it is hidden unless a
     * shared conversation is on screen — the local console needs no label.
     */
    private fun topicBar(): JPanel {
        topicBar.border = JBUI.Borders.empty(3, 8)
        topicBar.isOpaque = false
        topicLabel.font = topicLabel.font.deriveFont(Font.PLAIN, topicLabel.font.size2D - 0.5f)
        topicLabel.foreground = PanelPalette.muted
        topicBar.add(topicLabel, BorderLayout.CENTER)
        val switch = JButton("Switch…").apply {
            toolTipText = "Choose the local console or a shared conversation"
            addActionListener { showConversations() }
        }
        topicBar.add(switch, BorderLayout.EAST)
        topicBar.isVisible = false
        return topicBar
    }

    private fun conversationArea(): JPanel {
        val area = JPanel(BorderLayout(0, 0))
        val top = JPanel(BorderLayout())
        top.add(topicBar(), BorderLayout.NORTH)
        top.add(announcement, BorderLayout.SOUTH)
        area.add(top, BorderLayout.NORTH)

        feed.contentType = "text/html"
        feed.isEditable = false
        feed.editorKit = detailHtmlKit()
        feed.border = JBUI.Borders.empty()
        feed.text = ChatConsoleHtml.render(emptyList(), false, emptySet(), emptySet(), emptySet(), emptySet())
        feed.caretPosition = 0

        // The scroll pane may take an empty border; its VIEWPORT may not:
        // JViewport.setBorder() is contractually unsupported and THROWS on
        // every call, which aborted the whole panel's construction — the tool
        // window came up dead behind an IDE internal error (thrown since the
        // ADR-0036 rewrite; the panel had never actually finished building).
        // A viewport draws no border of its own, so deleting the call changes
        // nothing visually — the call could never have had an effect.
        feedScroll.border = JBUI.Borders.empty()
        area.add(feedScroll, BorderLayout.CENTER)
        return area
    }

    // ------------------------------------------------------------- input area

    /**
     * The composer: the question with its send key inside the box, the three
     * modes and the scope chip on the row under it.
     *
     * It is ALWAYS there (ADR-0036 decision 6). The shape is the VS Code
     * panel's (ADR-0044 decision 3): a bordered input with the send key
     * inside it, a pill row under it — not a text field with a "Send" button
     * beside it and three chunky toggles below.
     */
    private fun inputArea(): JPanel {
        val area = JPanel(BorderLayout(0, 0))
        area.border = JBUI.Borders.compound(
            JBUI.Borders.customLineTop(JBColor.border()),
            JBUI.Borders.empty(6, 8, 7, 8),
        )

        val composer = JPanel(BorderLayout(0, JBUI.scale(5)))
        composer.isOpaque = false
        inputField.placeholder = mode.placeholder
        inputField.onSubmit = { submit() }
        inputField.onResize = { fitInput() }
        inputField.addComponentListener(object : java.awt.event.ComponentAdapter() {
            override fun componentResized(e: java.awt.event.ComponentEvent) = fitInput()
        })
        inputField.addFocusListener(object : FocusAdapter() {
            override fun focusGained(e: FocusEvent) = run { fieldBox.focused = true }
            override fun focusLost(e: FocusEvent) = run { fieldBox.focused = false }
        })
        fieldScroll.border = JBUI.Borders.empty(3, 2)
        fieldScroll.isOpaque = false
        fieldScroll.viewport.isOpaque = false
        fieldBox.add(fieldScroll, BorderLayout.CENTER)
        // The send key keeps its 26px size and sits at the field's bottom
        // edge: a BorderLayout.EAST component would be stretched to the
        // field's height, and the accent square would become an accent wall
        // the moment the question wraps to five lines.
        val sendCorner = JPanel(BorderLayout())
        sendCorner.isOpaque = false
        sendCorner.border = JBUI.Borders.empty(0, 1, 3, 3)
        sendCorner.add(sendButton(), BorderLayout.SOUTH)
        fieldBox.add(sendCorner, BorderLayout.EAST)
        composer.add(fieldBox, BorderLayout.CENTER)

        val row = JPanel(BorderLayout(JBUI.scale(6), 0))
        row.isOpaque = false
        row.add(modeButtons(), BorderLayout.WEST)
        row.add(scopeChip(), BorderLayout.EAST)
        composer.add(row, BorderLayout.SOUTH)

        area.add(composer, BorderLayout.CENTER)
        fitInput()
        return area
    }

    /**
     * Grow the composer with its content.
     *
     * The multi-line box rises with the question (ADR-0036 decision 6) — up to
     * a ceiling, after which it scrolls instead of eating the feed.
     */
    private fun fitInput() {
        inputField.fitHeight()
        val lineHeight = inputField.getFontMetrics(inputField.font).height
        fieldScroll.preferredSize = Dimension(0, inputField.rows * lineHeight + JBUI.scale(8))
        fieldScroll.revalidate()
    }

    /**
     * The three knowledge faces, as one chosen at a time.
     *
     * They sit in ONE [SegmentTrack] because they are one choice: three
     * separately bordered pills read as three unrelated switches, and on a
     * narrow tool window they also used to take the whole row.
     */
    private fun modeButtons(): JPanel {
        val track = SegmentTrack()
        for (candidate in ChatMode.entries) {
            val button = ModePill(candidate.label, candidate.placeholder)
            modeToggles[candidate] = button
            button.chosen = candidate == mode
            button.addActionListener { switchMode(candidate) }
            track.add(button)
        }
        return track
    }

    /** The scope chip: which project the panel is locked to. */
    private fun scopeChip(): JPanel {
        val panel = JPanel(FlowLayout(FlowLayout.RIGHT, 0, 0))
        panel.isOpaque = false
        scopeChipButton.addActionListener { onScopeChipClicked() }
        panel.add(scopeChipButton)
        return panel
    }

    /**
     * The chip was clicked.
     *
     * With a resolved project it toggles the lock. Without one it opens the
     * project picker: the chip used to be DISABLED there, which left the
     * reader with nothing to do at all (ADR-0044 decision 6). The candidates
     * come from the server's own resolution, so the list is the projects the
     * folder could plausibly belong to.
     */
    private fun onScopeChipClicked() {
        if (session.currentScope().projectId > 0) {
            session.toggleScope()
            return
        }
        val candidates = session.candidates()
        if (candidates.isEmpty()) {
            session.toggleScope()
            return
        }
        val names = candidates.map { it.name }.toTypedArray()
        // showChooseDialog answers with the SELECTED INDEX (-1 when the
        // reader cancels), not with the value.
        val index = Messages.showChooseDialog(
            project,
            "Which project does this folder belong to?",
            "Cinder: Choose a project",
            null,
            names,
            names.first(),
        )
        if (index < 0 || index >= candidates.size) return
        background { session.bindProject(candidates[index].id) }
    }

    /**
     * The send key, inside the input box.
     *
     * A 26px accent square with a white arrow — the VS Code panel's `.send`
     * sits at the same corner of the same box. A labelled "Send" button under
     * the field read as a form submit and took its own row of width.
     */
    private fun sendButton(): JButton = IconButton(
        PanelIcons.send(),
        "Send (Enter). Shift+Enter for a new line.",
        accent = true,
        size = 26,
    ) { submit() }

    /** Ask the current mode the question in the composer, and clear it. */
    private fun submit() {
        val text = inputField.text.trim()
        if (text.isEmpty()) return
        if (sharedSection) {
            val job = shared.prepareSend(text)
            if (job == null) {
                showAnnouncement(ChatLevel.WARN, "Select or explicitly create a shared conversation first; " +
                    "wait for any active request in it to finish. Your draft is kept.")
                return
            }
            inputField.text = ""
            background { shared.send(job) }
        } else {
            val requestedMode = mode
            inputField.text = ""
            background { session.run(text, requestedMode) }
        }
        inputField.fitHeight()
    }

    // ------------------------------------------------------------- the session

    /**
     * The session's output, marshalled onto the EDT.
     *
     * The session runs on pooled threads, so every event has to come back to
     * the UI thread before it touches a component.
     */
    private fun post(event: ChatEvent) {
        val app = ApplicationManager.getApplication()
        if (app.isDispatchThread) {
            applyEvent(event)
        } else {
            app.invokeLater { if (!disposed) applyEvent(event) }
        }
    }

    private fun applyEvent(event: ChatEvent) {
        when (event) {
            is ChatEvent.Turn -> renderFeed()
            is ChatEvent.Announce -> {
                showAnnouncement(event.level, event.message)
                // A model-endpoint failure is the one error the reader can fix
                // from this panel, so it points at the provider settings page
                // instead of leaving a dead-end message.
                if (event.level == ChatLevel.ERROR && needsProviderHint(event.message)) {
                    offerProviderHelpOnce()
                }
            }
            is ChatEvent.Scope -> renderScope()
            is ChatEvent.Mode -> renderMode()
            // The candidates arrived: the chip now has something to offer, so
            // it says "choose a project" instead of "no project".
            is ChatEvent.Candidates -> renderScope()
        }
    }

    /** True when an error names the model endpoint rather than the data. */
    private fun needsProviderHint(message: String): Boolean =
        message.contains("AI endpoint", ignoreCase = true) ||
            message.contains("No AI endpoint", ignoreCase = true) ||
            message.contains("404")

    /**
     * Offer the provider settings page once per panel.
     *
     * A notification, not a modal: the endpoint can fail on every question, and
     * an unrequested dialog would interrupt the work each time. The reader can
     * still reach the dialog from the header at any moment.
     */
    private fun offerProviderHelpOnce() {
        if (providerHelpOffered) return
        providerHelpOffered = true
        val manager = NotificationGroupManager.getInstance()
        if (!manager.isGroupRegistered(NOTIFICATION_GROUP)) return
        manager.getNotificationGroup(NOTIFICATION_GROUP).createNotification(
            "Cinder: AI provider",
            "The answer failed at the model endpoint. Configure the Base URL and model on the " +
                "AI Provider settings page.",
            NotificationType.WARNING,
        ).apply {
            addAction(NotificationAction.createSimple("AI Provider settings…") { openProviderSettings() })
            notify(null)
        }
    }

    /** Switch the knowledge face the composer talks to. */
    private fun switchMode(next: ChatMode) {
        if (sharedSection) sectionPicker.selectedIndex = 0
        if (mode == next) return
        mode = next
        session.setMode(next)
        renderMode()
    }

    private fun renderMode() {
        inputField.placeholder = if (sharedSection) "Ask the knowledge base in this shared conversation…" else mode.placeholder
        modeToggles.forEach { (candidate, button) -> button.chosen = candidate == mode }
    }

    /**
     * The scope chip is never disabled, so it must not become a wall of text
     * either: a long project name used to push the Send button out of the row
     * in a narrow tool window. The tooltip keeps the full name.
     */
    private fun scopeLabel(raw: String): String = if (raw.length <= 18) raw else raw.take(17) + "…"

    /**
     * The chip's wording.
     *
     * The lock is a default, not a restriction, so the chip says which of the
     * two it is — and a workspace that resolves to no project says so instead
     * of pretending it is scoped.
     *
     * The chip is NEVER disabled: an unresolved workspace used to disable it,
     * which left the reader with nothing to do (ADR-0044 decision 6). It now
     * offers the projects the folder could belong to.
     */
    private fun renderScope() {
        val chip = scopeChipButton
        val scope = session.currentScope()
        when {
            scope.projectId <= 0 -> {
                val hasCandidates = session.candidates().isNotEmpty()
                chip.text = if (hasCandidates) "Choose project" else "No project"
                chip.toolTipText = if (hasCandidates) {
                    "This workspace does not resolve to a Cinder project yet — click to pick one."
                } else {
                    "This workspace does not resolve to a Cinder project yet — " +
                        "the panel searches across all projects."
                }
                chip.locked = false
                chip.isEnabled = true
            }
            scope.locked -> {
                chip.text = scopeLabel(scope.projectName)
                chip.toolTipText = "Locked to ${scope.projectName} — click to search across all projects."
                chip.locked = true
                chip.isEnabled = true
            }
            else -> {
                chip.text = "All projects"
                chip.toolTipText = "Searching across all projects — click to lock back to ${scope.projectName}."
                chip.locked = false
                chip.isEnabled = true
            }
        }
    }

    /**
     * The announcement bar: one line, with a level badge, dismissible.
     *
     * It replaces the old status line (ADR-0036 decision 5): a failure that
     * sits at the bottom of the window, under everything, is a failure the
     * reader never sees.
     */
    private fun showAnnouncement(level: ChatLevel, message: String) {
        announcement.show(level, message)
    }

    // ---------------------------------------------------------------- the feed

    /** Repaint the feed from the session's turns and the panel's fold state. */
    private fun renderFeed() {
        if (disposed) return
        val html = if (sharedSection) ChatConsoleHtml.renderShared(shared.snapshot()) else ChatConsoleHtml.render(
            turns = session.turns(),
            showEarlier = showEarlier,
            collapsedTurns = collapsedTurns,
            expandedSources = expandedSources,
            expandedCards = expandedCards,
            expandedThoughts = expandedThoughts,
        )
        val bar = feedScroll.verticalScrollBar
        val wasAtBottom = bar.value + bar.visibleAmount >= bar.maximum - 16
        val previous = bar.value
        feed.text = html
        feed.caretPosition = 0
        feedScroll.revalidate()
        // The feed is rebuilt from scratch, so the scroll position has to be
        // put back: a reader who scrolled up to re-read an old answer must not
        // be yanked to the bottom by the next turn arriving.
        SwingUtilities.invokeLater {
            if (disposed) return@invokeLater
            feedScroll.validate()
            bar.value = if (wasAtBottom) bar.maximum else minOf(previous, bar.maximum)
        }
    }

    /**
     * Dispatch a click in the feed.
     *
     * Every action in the feed is an `<a href="repotory:…">`; Swing's
     * LinkController hands the raw href over as the event's description. A
     * link inside a note body is an ordinary URL and opens in the browser.
     */
    private fun onHyperlink(event: HyperlinkEvent) {
        if (event.eventType != HyperlinkEvent.EventType.ACTIVATED) return
        val href = event.description ?: return
        if (href.startsWith(ChatConsoleHtml.LINK_SCHEME)) {
            handleFeedAction(href.removePrefix(ChatConsoleHtml.LINK_SCHEME))
            return
        }
        if (href.startsWith("http://") || href.startsWith("https://")) {
            BrowserUtil.browse(href)
        }
    }

    /** One action of the feed. */
    private fun handleFeedAction(target: String) {
        val parts = target.trim('/').split('/')
        when (parts.firstOrNull()) {
            "shared" -> {
                if (!sharedSection || parts.getOrNull(1) != "retry") return
                val job = shared.prepareRetry(parts.getOrNull(2) ?: return) ?: return
                background { shared.send(job) }
            }
            "earlier" -> {
                showEarlier = !showEarlier
                renderFeed()
            }
            // The idle state's examples. The two mode rows switch the face
            // they name; the question row asks — its label IS a question, and
            // a click that only moved the caret left the reader to retype it.
            "example" -> when (parts.getOrNull(1)) {
                "notes" -> {
                    switchMode(ChatMode.NOTES)
                    inputField.requestFocusInWindow()
                }
                "pages" -> {
                    switchMode(ChatMode.PAGES)
                    inputField.requestFocusInWindow()
                }
                else -> askNow(ChatConsoleHtml.EXAMPLE_ASK)
            }
            "turn" -> {
                val turnId = parts.getOrNull(1) ?: return
                when (parts.getOrNull(2)) {
                    "toggle" -> {
                        if (!collapsedTurns.remove(turnId)) collapsedTurns.add(turnId)
                    }
                    "sources" -> {
                        if (!expandedSources.remove(turnId)) expandedSources.add(turnId)
                    }
                    "think" -> {
                        if (!expandedThoughts.remove(turnId)) expandedThoughts.add(turnId)
                    }
                }
                renderFeed()
            }
            "card" -> {
                val turnId = parts.getOrNull(1) ?: return
                val cardType = parts.getOrNull(2) ?: return
                val cardId = parts.getOrNull(3)?.toLongOrNull() ?: return
                when (parts.getOrNull(4)) {
                    "toggle" -> toggleCard(turnId, cardType, cardId)
                    "ask" -> askAboutCard(turnId, cardType, cardId)
                    "edit" -> editCard(turnId, cardType, cardId)
                    "create" -> newNoteUnder(turnId, cardType, cardId)
                    "organize" -> organizeCard(turnId, cardType, cardId)
                }
            }
        }
    }

    /**
     * Open a card in place, or close it again.
     *
     * The card is marked open FIRST and repainted, so the reader sees
     * "Loading…" instead of a card that does not react; the body arrives
     * with the turn update.
     */
    private fun toggleCard(turnId: String, cardType: String, cardId: Long) {
        val key = "$turnId/$cardType/$cardId"
        if (!expandedCards.remove(key)) {
            expandedCards.add(key)
            background { session.expandCard(turnId, cardType, cardId) }
        }
        renderFeed()
    }

    // ---------------------------------------------------------------- actions

    /**
     * The card a feed action addresses.
     *
     * Notes and todos have INDEPENDENT id sequences, so the same number can
     * name two different cards in one turn: the card is addressed by its id
     * AND its type.
     */
    private fun card(turnId: String, cardType: String, cardId: Long): ChatCard? =
        session.turns().firstOrNull { it.id == turnId }
            ?.cards?.firstOrNull { it.hit.type == cardType && it.hit.id == cardId }

    /**
     * Ask a question about this card — now, in this feed.
     *
     * The panel has no second window to fall back on: the body is already on
     * screen (clicking the title loads it), and the conversation about it
     * starts here too. The click IS the request: the composer is filled first
     * (so the turn shows what was asked) and then submitted through the same
     * path Enter uses. A button labelled "Ask about this" that only fills a
     * field is asking the reader to press Enter for it.
     *
     * When the shared turn is busy, submit() declines and keeps the draft, so
     * a click is never silently dropped.
     */
    private fun askAboutCard(turnId: String, cardType: String, cardId: Long) {
        val hit = card(turnId, cardType, cardId)?.hit ?: return
        askNow(followUpQuestion(hit))
    }

    /**
     * Ask [text] now: the composer first (so the turn records the question),
     * then the same submit path Enter uses.
     *
     * Two places name a question — a card's "Ask about this" and the idle
     * panel's question example — and they must behave alike: a click that
     * names a question asks it. When the shared turn is busy, submit() keeps
     * the draft and says so, so the click is never silently dropped.
     */
    private fun askNow(text: String) {
        switchMode(ChatMode.ASK)
        inputField.text = text
        submit()
        inputField.requestFocusInWindow()
    }

    /**
     * Edit a note. The hit carries only the excerpt, so the note is re-read,
     * the shared editor dialog opens, and an OK saves fields first and the
     * project change second — a failed move must not lose the edit the user
     * just made.
     */
    private fun editCard(turnId: String, cardType: String, cardId: Long) {
        val hit = card(turnId, cardType, cardId)?.hit ?: return
        if (hit.type != "note") return
        background({
            val note = repotoryMcp.note(hit.id)
            val projects = repotoryMcp.projects()
            note to projects
        }) { (note, projects) ->
            if (note == null) {
                showAnnouncement(ChatLevel.ERROR, "Note #${hit.id} could not be read — nothing to edit")
            } else {
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
                    turnId = turnId, cardType = hit.type, cardId = cardId,
                )
            }
        }
    }

    /** New note under the SAME project as the card. */
    private fun newNoteUnder(turnId: String, cardType: String, cardId: Long) {
        val hit = card(turnId, cardType, cardId)?.hit ?: return
        background({ repotoryMcp.projects() }) { projects ->
            openEditor(
                NoteDraft(id = 0, projectId = hit.projectId, title = "", tags = "", kind = "knowledge", content = ""),
                projects, proposal = false, warning = "",
                turnId = turnId, cardType = hit.type, cardId = cardId,
            )
        }
    }

    /**
     * AI tidy: ask the service for a reorganization PROPOSAL and open the
     * SAME editor with it. Nothing is written by the service; the user's save
     * is the gate. The stored note is re-read so kind and pinned carry into
     * the draft (the proposal covers title/tags/content only).
     */
    private fun organizeCard(turnId: String, cardType: String, cardId: Long) {
        val hit = card(turnId, cardType, cardId)?.hit ?: return
        if (hit.type != "note") return
        showAnnouncement(ChatLevel.INFO, "AI tidying note #${hit.id}… (this can take a while)")
        background({
            val proposal = repotoryMcp.organizeNote(hit.id)
            val note = repotoryMcp.note(hit.id)
            val projects = repotoryMcp.projects()
            Triple(proposal, note, projects)
        }) { (proposal, note, projects) ->
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
                turnId = turnId, cardType = hit.type, cardId = cardId,
            )
        }
    }

    /**
     * Opens the shared editor; an OK saves through [saveDraft].
     *
     * The editor is a DIALOG, not a card of its own: Swing has no inline
     * editing surface, and a modal form is the platform's own answer. The
     * card behind it is re-read after the save, so the feed shows what was
     * actually written.
     */
    private fun openEditor(
        original: NoteDraft,
        projects: List<ProjectOption>,
        proposal: Boolean,
        warning: String,
        turnId: String?,
        cardType: String?,
        cardId: Long?,
    ) {
        val dialog = NoteEditorDialog(project, original, projects, proposal, warning)
        if (!dialog.showAndGet()) {
            showAnnouncement(ChatLevel.INFO, "Edit cancelled — nothing saved")
            return
        }
        saveDraft(original, dialog.editedDraft(), turnId, cardType, cardId)
    }

    /**
     * Persists an edited draft: UpdateNoteFull, then MoveNote when the project
     * changed. The card is repainted from the freshly stored note, so what is
     * on screen is what is in the database.
     */
    private fun saveDraft(
        original: NoteDraft,
        edited: NoteDraft,
        turnId: String?,
        cardType: String?,
        cardId: Long?,
    ) {
        val serverUrl = RepotorySettings.serverUrl()
        showAnnouncement(
            ChatLevel.INFO,
            if (edited.id == 0L) "Creating note…" else "Saving note #${edited.id}…",
        )
        background({
            try {
                if (edited.id == 0L) {
                    val created = repotoryMcp.createNote(edited)
                    SaveOutcome(saved = created)
                } else {
                    val updated = repotoryMcp.updateNote(edited)
                    // MoveNote stays on HTTP on purpose (ADR-0035 decision 2): moving
                    // a note between projects is a human decision, not an agent's.
                    // It is the ONE write that still needs the local service — and
                    // when the service is down the fields ARE saved, so the wording
                    // must say "the move failed", never "the save failed".
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
                    SaveOutcome(saved = updated, moveError = moveError)
                }
            } catch (e: Exception) {
                SaveOutcome(failure = e.message?.takeIf { it.isNotBlank() } ?: e.javaClass.simpleName)
            }
        }) { outcome ->
            val saved = outcome.saved
            if (saved == null) {
                showAnnouncement(
                    ChatLevel.ERROR,
                    "${if (edited.id == 0L) "Create" else "Save"} failed: ${outcome.failure}",
                )
                return@background
            }
            // The MCP surface cannot CLEAR a title (an empty value means
            // "keep what is there"), so a title the user emptied survives the
            // write. Saying so beats reporting a success that did not do
            // what was asked.
            val titleKept = edited.title.isEmpty() && saved.title.isNotEmpty()
            showAnnouncement(
                when {
                    outcome.moveError != null -> ChatLevel.WARN
                    titleKept -> ChatLevel.WARN
                    else -> ChatLevel.INFO
                },
                when {
                    outcome.moveError != null ->
                        "Fields saved, but the project move failed: ${outcome.moveError} " +
                            "(moving a note between projects needs the local Cinder service)."
                    titleKept ->
                        "Saved note #${edited.id}, but the title could not be cleared — " +
                            "the MCP write path keeps the old title."
                    edited.id == 0L -> "Created note #${saved.id}"
                    else -> "Saved note #${saved.id}"
                },
            )
            if (turnId != null && cardType != null && cardId != null) {
                if (edited.id == 0L) {
                    // The new note joined the turn as a card of its own, so the
                    // reader sees what was just written.
                    background { session.addCard(turnId, hitOf(saved)) }
                } else {
                    // The card shows what was actually written.
                    background { session.expandCard(turnId, cardType, cardId, force = true) }
                }
            }
        }
    }

    /** What one save produced: the stored note, or the reason it did not happen. */
    private data class SaveOutcome(
        val saved: NoteDetail? = null,
        val moveError: String? = null,
        val failure: String? = null,
    )

    /**
     * The card a freshly created note becomes.
     *
     * It is a card like any other: its body loads in place through
     * `repotory_notes_read`, so nothing here points anywhere else.
     */
    private fun hitOf(note: NoteDetail): RepotoryHit = RepotoryHit(
        type = "note",
        id = note.id,
        projectId = note.projectId,
        title = note.title,
        snippet = snippetPlain(note.body, 140),
    )

    // ------------------------------------------------------------- connection

    /**
     * Asks the service whether it is there, and paints the answer on the pill.
     *
     * The probe is asked on its own thread and answered on the EDT: the panel
     * must never wait on a socket.
     */
    private fun probeConnection() {
        if (disposed) return
        statusDot.state = LinkState.CHECKING
        background({
            // The channel the panel actually depends on is MCP (ADR-0035),
            // not the local service: ask, search, read, write and organize all
            // work with no server running. The probe asks whether the MCP
            // binary is there — cheap, and it spawns nothing.
            McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        }) { resolution ->
            if (resolution.found) {
                statusDot.state = LinkState.ONLINE
            } else {
                statusDot.state = LinkState.OFFLINE
                showAnnouncement(ChatLevel.WARN, resolution.problem ?: "repotory-mcp was not found.")
                // The startup warning carries the offer (dirs tried + Locate… +
                // Open settings); the dot's own click opens the picker instead,
                // so a SECOND click is not a no-op.
                offerMcpBinaryOnce()
            }
        }
    }

    /**
     * The pill was clicked (or Refresh was pressed): check the channel again.
     */
    private fun reconnect() {
        if (disposed) return
        statusDot.state = LinkState.CHECKING
        background({
            McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        }) { resolution ->
            if (resolution.found) {
                statusDot.state = LinkState.ONLINE
                showAnnouncement(ChatLevel.INFO, "repotory-mcp found — the panel will use it for the next call.")
            } else {
                statusDot.state = LinkState.OFFLINE
                showAnnouncement(ChatLevel.WARN, resolution.problem ?: "repotory-mcp was not found.")
                // The tooltip says "click to locate it", so the click opens the
                // picker. It used to re-raise the once-per-panel balloon, which
                // made every click after the first do nothing at all.
                locateMcpBinary()
            }
        }
    }

    /** Pick a `repotory-mcp` binary, apply it, and re-check the channel. */
    private fun locateMcpBinary() {
        chooseMcpBinary()?.let {
            RepotorySettings.setMcpBinaryPath(it.absolutePath)
            // The client points at the old path (or never started): a new
            // setting has to take effect now, not at the next IDE restart.
            repotoryMcp.shutdown()
            reconnect()
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
     * A NOTIFICATION, not a modal dialog: the channel can be down on every
     * question the user asks, and a dialog nobody asked for would interrupt
     * that work.
     */
    private fun offerMcpBinaryOnce() {
        if (mcpHelpOffered) return
        mcpHelpOffered = true
        val manager = NotificationGroupManager.getInstance()
        if (!manager.isGroupRegistered(NOTIFICATION_GROUP)) return
        val resolution = McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        val notification = manager.getNotificationGroup(NOTIFICATION_GROUP).createNotification(
            "Cinder: no MCP binary",
            "The plugin could not find repotory-mcp — it is what the panel talks to.\n" +
                resolution.attempts.take(8).joinToString("\n"),
            NotificationType.WARNING,
        )
        notification.addAction(
            NotificationAction.createSimple("Locate…") { locateMcpBinary() },
        )
        notification.addAction(
            NotificationAction.createSimple("Open settings") {
                ShowSettingsUtil.getInstance().showSettingsDialog(null, "dev.repotory.jetbrains.settings")
            },
        )
        notification.notify(null)
    }


    // ------------------------------------------------------------- the plumbing

    /** Run work off the EDT. */
    private fun background(work: () -> Unit) {
        if (disposed) return
        ApplicationManager.getApplication().executeOnPooledThread { work() }
    }

    /** Run work off the EDT and report its outcome back on the EDT. */
    private fun <T> background(work: () -> T, then: (T) -> Unit) {
        if (disposed) return
        val app = ApplicationManager.getApplication()
        app.executeOnPooledThread {
            val result = work()
            app.invokeLater { if (!disposed) then(result) }
        }
    }

    override fun dispose() {
        disposed = true
        sharedPolling.close()
    }
}

/**
 * A dialog that hosts an existing Swing panel — the conversation picker.
 *
 * The picker used to be a permanent band above the feed; it is now shown on
 * demand (header action or the topic bar's "Switch…"). A dialog is the
 * platform's own shape for "a small form over the work".
 */
private class PanelDialog(titleText: String, private val content: JComponent) : DialogWrapper(true) {
    init {
        title = titleText
        init()
    }

    override fun createCenterPanel(): JComponent = content
}

/**
 * The composer's text field: a multi-line box that grows with its content and
 * shows the mode's placeholder while it is empty.
 *
 * Enter sends, Shift+Enter breaks the line — the same convention the VS Code
 * console uses, so the muscle memory carries over.
 */
private class ChatTextArea : JTextArea() {
    /** The hint shown while the field is empty. */
    var placeholder: String = ""

    /** Called when the reader presses Enter. */
    var onSubmit: (() -> Unit)? = null

    /** Called when the field's height changed and the panel has to follow. */
    var onResize: (() -> Unit)? = null

    init {
        lineWrap = true
        wrapStyleWord = true
        rows = 1
        font = Font(Font.MONOSPACED, Font.PLAIN, JBUI.scale(12))
        border = JBUI.Borders.empty(2, 4)
        // The composer's frame paints the rounded fill and border; an opaque
        // field would square off the corners it is drawn inside.
        isOpaque = false
        addKeyListener(object : KeyAdapter() {
            override fun keyPressed(e: KeyEvent) {
                if (e.keyCode == KeyEvent.VK_ENTER && !e.isShiftDown) {
                    e.consume()
                    onSubmit?.invoke()
                }
            }
        })
        addFocusListener(object : FocusAdapter() {
            override fun focusGained(e: FocusEvent) = fitHeight()
        })
        document.addDocumentListener(object : javax.swing.event.DocumentListener {
            override fun insertUpdate(e: javax.swing.event.DocumentEvent?) = fitHeight()
            override fun removeUpdate(e: javax.swing.event.DocumentEvent?) = fitHeight()
            override fun changedUpdate(e: javax.swing.event.DocumentEvent?) = fitHeight()
        })
    }

    /** Grow the field with its content, up to a readable ceiling. */
    fun fitHeight() {
        // Use the real width once the field has been laid out. The old 220px
        // floor under-counted rows in a narrow tool window, so the text was
        // clipped instead of wrapped.
        val width = if (width > 0) width else JBUI.scale(220)
        val metrics = getFontMetrics(font)
        val lines = text.split("\n").sumOf { line ->
            if (line.isEmpty()) 1 else maxOf(1, ceil(metrics.stringWidth(line).toDouble() / width).toInt())
        }
        val wanted = lines.coerceIn(1, 8)
        if (rows != wanted) {
            rows = wanted
            onResize?.invoke()
        }
    }

    override fun paintComponent(g: Graphics) {
        super.paintComponent(g)
        if (text.isNotEmpty() || placeholder.isEmpty()) return
        g.color = UIUtil.getContextHelpForeground()
        val metrics = g.fontMetrics
        g.drawString(placeholder, insets.left + 2, insets.top + metrics.ascent)
    }
}

/**
 * The announcement bar: one line, with a level badge, dismissible.
 *
 * It replaces the old status line (ADR-0036 decision 5): the status of the
 * panel is not a footnote under the conversation, it is a banner above it.
 * The badge is what separates "this failed" from "this is done" without
 * relying on the reader's colour vision.
 */
private class AnnouncementBar : JPanel(BorderLayout(JBUI.scale(6), 0)) {
    private val badge = JLabel()
    private val message = AnnouncementText()
    private val dismiss = IconButton(PanelIcons.close(JBUI.scale(12)), "Dismiss") { onDismiss?.invoke() }

    /** Called when the reader dismisses the banner. */
    var onDismiss: (() -> Unit)? = null

    init {
        isVisible = false
        border = JBUI.Borders.compound(
            JBUI.Borders.customLineBottom(JBColor.border()),
            JBUI.Borders.empty(5, 8),
        )
        message.foreground = JBColor.foreground()
        add(badge, BorderLayout.WEST)
        add(message, BorderLayout.CENTER)
        add(dismiss, BorderLayout.EAST)
    }

    /** Show a message, replacing whatever was there. */
    fun show(level: ChatLevel, text: String) {
        message.text = text
        badge.icon = LevelBadgeIcon(level)
        background = when (level) {
            ChatLevel.INFO -> JBColor.background()
            ChatLevel.WARN -> JBColor(Color(0xFF, 0xF6, 0xD5), Color(0x3B, 0x33, 0x14))
            ChatLevel.ERROR -> JBColor(Color(0xFF, 0xE6, 0xE6), Color(0x3B, 0x1F, 0x1F))
        }
        isVisible = true
        revalidate()
        repaint()
    }

    /** Take the banner down; the panel keeps its state, the banner does not. */
    fun dismiss() {
        message.text = ""
        isVisible = false
        revalidate()
        repaint()
    }
}

/**
 * The announcement bar's text.
 *
 * It wraps: the messages say things like "The Cinder MCP channel is not
 * reachable — the project lock needs it, so the panel searches across all
 * projects" (ADR-0062: the project lock runs over MCP, not the local
 * service), and a label that cannot wrap either clips that sentence or
 * claims the whole width of the panel and pushes the dismiss button off the
 * edge.
 */
private class AnnouncementText : JTextArea() {
    init {
        isEditable = false
        isOpaque = false
        isFocusable = false
        lineWrap = true
        wrapStyleWord = true
        border = JBUI.Borders.empty(0)
        font = UIManager.getFont("Label.font") ?: font
        rows = 1
        addComponentListener(object : java.awt.event.ComponentAdapter() {
            override fun componentResized(e: java.awt.event.ComponentEvent) = fit()
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
        // ~6px per character at the label font. Before the first layout the
        // width is still zero, so a scaled guess stands in.
        val usable = if (width > 0) width else JBUI.scale(280)
        val perLine = maxOf(16, usable / 6)
        return ceil(value.length.toDouble() / perLine).toInt().coerceIn(1, 3)
    }
}

/** Opens the Cinder tool window — the action behind its title bar button. */
class OpenRepotoryAction : AnAction(), DumbAware {
    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

    override fun update(e: AnActionEvent) {
        e.presentation.isEnabledAndVisible = e.project != null
    }

    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val toolWindow = ToolWindowManagerEx
            .getInstanceEx(project)
            .getToolWindow("Cinder") ?: return
        toolWindow.activate(null)
    }
}
