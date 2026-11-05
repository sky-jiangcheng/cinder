// The Cinder chat console — the plugin's conversation panel.
//
// ADR-0036 (Accepted 2026-11-02) rewrote this panel: it used to be a
// SEARCHER (one query, a list of thumbnails, a detail pane) and it is now a
// CONSOLE. The layout is the one the VS Code extension ships, region for
// region:
//
//   ┌──────────────────────────────────────────────────────────┐
//   │ ▪ Cinder  the local-first memory layer…    [● online][↻][▶]│  ① header
//   ├──────────────────────────────────────────────────────────┤
//   │ ⚠ announcement bar — one line, dismissible                │  ② announcement
//   ├──────────────────────────────────────────────────────────┤
//   │ ▸ 3 earlier turns                                        │  ④ folded history
//   │ [ASK] why does the gateway retry 503?                     │  ③ the feed
//   │       Because the backoff is capped at 30 s.              │     (the only
//   │       Sources (2) ▸                                       │      scroll area)
//   │ [NOTES] fts5 quirk                                       │
//   │       ┌ note #42 ──────────────────────────────┐          │
//   │       │ FTS5 quirk                             │          │
//   │       │ content tables answer…                 │          │
//   │       │ Open in Cinder · Copy · Edit …       │          │
//   │       └────────────────────────────────────────┘          │
//   ├──────────────────────────────────────────────────────────┤
//   │ [ what do you want to know?                       ]        │  ⑤ input area
//   │ [Ask][Notes][Pages]                    [🔒 pay-common][Send]│
//   └──────────────────────────────────────────────────────────┘
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
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.content.ContentFactory
import com.intellij.util.ui.JBUI
import com.intellij.util.ui.UIUtil
import java.awt.BorderLayout
import java.awt.Color
import java.awt.Component
import java.awt.Cursor
import java.awt.Dimension
import java.awt.Font
import java.awt.Graphics
import java.awt.Graphics2D
import java.awt.GridLayout
import java.awt.Insets
import java.awt.RenderingHints
import java.awt.datatransfer.StringSelection
import java.awt.event.FocusAdapter
import java.awt.event.FocusEvent
import java.awt.event.KeyAdapter
import java.awt.event.KeyEvent
import java.awt.image.BufferedImage
import java.util.concurrent.TimeUnit
import javax.imageio.ImageIO
import javax.swing.BorderFactory
import javax.swing.BoxLayout
import javax.swing.ButtonGroup
import javax.swing.Icon
import javax.swing.ImageIcon
import javax.swing.DefaultListCellRenderer
import javax.swing.JButton
import javax.swing.JComboBox
import javax.swing.JList
import javax.swing.Timer
import javax.swing.JEditorPane
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.JTextArea
import javax.swing.JToggleButton
import javax.swing.SwingUtilities
import javax.swing.UIManager
import javax.swing.event.HyperlinkEvent
import kotlin.math.ceil

/** The notification group the plugin posts its service problems to. */
private const val NOTIFICATION_GROUP = "Cinder"

/** Where the desktop app is downloaded from, when it is not installed. */
private const val RELEASES_URL = "https://github.com/sky-jiangcheng/cinder/releases"

/**
 * The state of the connection to the Cinder service.
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
    private val renameThread = JButton("Rename")
    private var sharedSection = false
    private var updatingThreads = false
    private var displayedThread: String? = null
    private var localDraft = ""
    private val sharedDrafts = mutableMapOf<String?, String>()
    private val modeToggles = mutableMapOf<ChatMode, JToggleButton>()

    private data class ThreadChoice(val id: String?, val label: String) {
        override fun toString(): String = label
    }
    private val feed = JEditorPane()
    private val feedScroll = JBScrollPane(feed)
    private val announcement = AnnouncementBar()
    private val pill = ConnectionPill()
    private val serviceToolbar: ActionToolbar

    /**
     * The panel's own action strip (ADR-0044 decision 2).
     *
     * The SAME four actions the VS Code panel draws in its header, in the
     * same order. There the host's title bar cropped the third one off the
     * panel edge, so the strip moved into the webview; Swing draws its own
     * chrome, so here it was always the panel's — but it has to be the same
     * four, or the two IDEs stop being the same product.
     */
    private val actionToolbar: ActionToolbar

    /** The composer's text field. */
    private val inputField = ChatTextArea()

    /** The composer's scroll pane — it grows with the question. */
    private val fieldScroll = JBScrollPane(inputField)

    /** The scope chip: which project the panel is locked to. */
    private val scopeChipButton = JButton("All projects")

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

    /** True while a start is in flight, so a second click cannot double-spawn. */
    private var starting = false

    init {
        // JPanel ships with a FlowLayout; BorderLayout has to replace it
        // before the regions are added, and removeAll() clears the
        // default layout manager's single placeholder component.
        removeAll()
        layout = BorderLayout(0, 0)

        serviceToolbar = buildToolbar("RepotoryService", serviceActions())
        actionToolbar = buildToolbar("RepotoryActions", actionActions())

        add(headerStrip(), BorderLayout.NORTH)
        add(conversationArea(), BorderLayout.CENTER)
        add(inputArea(), BorderLayout.SOUTH)

        pill.onClick = { reconnect() }
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
        actions.add(JButton("New conversation").apply {
            addActionListener {
                val title = Messages.showInputDialog(project, "Conversation title (shared with Warden):",
                    "New shared conversation", null)?.trim().orEmpty()
                if (title.isNotEmpty()) background { shared.create(title) }
            }
        })
        renameThread.addActionListener {
            val thread = shared.snapshot().selected ?: return@addActionListener
            val title = Messages.showInputDialog(project, "New conversation title:",
                "Rename shared conversation", null, thread.title, null)?.trim().orEmpty()
            if (title.isNotEmpty()) background { shared.rename(thread.id, title) }
        }
        actions.add(renameThread)
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
            renderMode()
            renderShared()
            renderFeed()
            sharedPolling.setVisible(isShowing && sharedSection)
        }
        return panel
    }

    private fun renderShared() {
        val state = shared.snapshot()
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
        renameThread.isEnabled = state.selected != null
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
     * The top band: the mark, the wordmark, the panel's own action strip and
     * the service actions.
     *
     * A slim native strip, not a printed card: every region has a flexible
     * width, so a narrow tool window reflows instead of clipping.
     *
     * The action strip is the SAME set the VS Code panel draws in its header
     * (ADR-0044 decision 2): search, the AI provider sheet, diagnostics and
     * the desktop app. The VS Code side had to move them out of the host's
     * title bar, where the third one was cropped off the panel edge; Swing
     * draws its own chrome, so here they were always the panel's own — but
     * they have to be the same four, or the two IDEs stop being the same
     * product.
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
        // The slogan line is gone (ADR-0044 decision 4): the panel says what
        // it does, not what the product is.
        val name = JLabel("Cinder")
        name.font = name.font.deriveFont(Font.BOLD)
        brand.add(name, BorderLayout.CENTER)

        // The right side of the strip: the service actions, the panel's own
        // actions and the status pill, side by side. They share ONE panel so
        // they stay together at the right edge — a BorderLayout.CENTER would
        // stretch the action strip and push the buttons away from the pill.
        val right = JPanel(BorderLayout(JBUI.scale(6), 0))
        right.isOpaque = false
        right.add(serviceToolbar.component, BorderLayout.WEST)
        val actions = JPanel(BorderLayout())
        actions.isOpaque = false
        actions.add(actionToolbar.component, BorderLayout.WEST)
        right.add(actions, BorderLayout.CENTER)
        right.add(pill, BorderLayout.EAST)

        header.add(brand, BorderLayout.WEST)
        header.add(right, BorderLayout.EAST)
        return header
    }

    /**
     * The panel's own action strip.
     *
     * The four actions the VS Code panel shows in its header, in the same
     * order, with the same wording in their tooltips.
     */
    private fun actionActions(): ActionGroup = DefaultActionGroup(
        object : AnAction("Search notes", "Search notes and todos", AllIcons.Actions.Search), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun actionPerformed(e: AnActionEvent) {
                switchMode(ChatMode.NOTES)
                inputField.requestFocusInWindow()
            }
        },
        object : AnAction("AI provider", "Configure the external AI provider", AllIcons.General.Settings), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun actionPerformed(e: AnActionEvent) = openProviderSheet()
        },
        object :
            AnAction("Diagnostics", "Check the Cinder environment", AllIcons.General.InspectionsWarning),
            DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun actionPerformed(e: AnActionEvent) = openDiagnostics()
        },
        object : AnAction("Desktop app", "Open the Cinder Desktop app", AllIcons.General.Web), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun actionPerformed(e: AnActionEvent) = openDesktopApp()
        },
    )

    // ------------------------------------------------------- the header actions

    /**
     * The provider sheet (ADR-0044 decision 5).
     *
     * It is a dialog because Swing has no inline form surface, and it reads
     * and writes the LOCAL SERVICE's settings — the plugin keeps no second
     * copy, so the desktop app and this panel always use the same endpoint.
     */
    private fun openProviderSheet() {
        ProviderDialog(project, session).show()
    }

    /**
     * The environment report.
     *
     * It answers "why does this not work?" with facts: the MCP binary, the
     * local service and the workspace's resolution — the three things that
     * decide whether the panel can work at all.
     */
    private fun openDiagnostics() {
        DiagnosticsDialog(project).show()
    }

    /**
     * Open the Cinder Desktop app.
     *
     * The desktop app is the FULL product; the panel is a thin client over
     * it. Nothing is downloaded: when the app is not installed, the offer is
     * the releases page.
     */
    private fun openDesktopApp() {
        val os = System.getProperty("os.name", "").lowercase()
        val command = when {
            os.contains("mac") -> listOf("open", "-a", "Cinder Desktop")
            os.contains("win") -> listOf("repotory.exe")
            else -> listOf("repotory")
        }
        background({
            try {
                val process = ProcessBuilder(command).redirectErrorStream(true).start()
                if (!process.waitFor(10, TimeUnit.SECONDS)) {
                    process.destroy()
                    false
                } else {
                    process.exitValue() == 0
                }
            } catch (e: Exception) {
                false
            }
        }) { started ->
            if (started) {
                showAnnouncement(ChatLevel.INFO, "Cinder Desktop app launched.")
            } else {
                val choice = Messages.showDialog(
                    "The Cinder Desktop app was not found. Install it from the releases page.",
                    "Cinder: desktop app not found",
                    arrayOf("Open the releases page", "Cancel"),
                    0,
                    Messages.getWarningIcon(),
                )
                if (choice == 0) BrowserUtil.browse(RELEASES_URL)
            }
        }
    }

    /** The brand mark as an ImageIcon, scaled from the bundled PNG (Swing has no SVG). */
    private fun markLabel(size: Int): JLabel? {
        val stream = javaClass.getResourceAsStream("/icons/repotory-mark.png") ?: return null
        val image: BufferedImage = ImageIO.read(stream) ?: return null
        val scaled = image.getScaledInstance(size, size, java.awt.Image.SCALE_SMOOTH)
        return JLabel(ImageIcon(scaled))
    }

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

    /** Refresh (re-probe the channel) and Start (bring the local service up). */
    private fun serviceActions(): ActionGroup = DefaultActionGroup(
        object : AnAction("Refresh", "Re-check the Cinder channel", AllIcons.Actions.Refresh), DumbAware {
            override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.EDT

            override fun actionPerformed(e: AnActionEvent) = reconnect()
        },
        object : AnAction("Start local service", "Start the local Cinder service", AllIcons.Actions.Execute), DumbAware {
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

    // --------------------------------------------------------- conversation area

    /**
     * The announcement bar and the feed.
     *
     * The feed is the ONLY scroll area (ADR-0036 decision 1): there is no
     * detail pane beside it, so a narrow tool window never squeezes a column
     * of text into something unreadable.
     */
    private fun conversationArea(): JPanel {
        val area = JPanel(BorderLayout(0, 0))
        val top = JPanel(BorderLayout())
        top.add(conversationPicker(), BorderLayout.NORTH)
        top.add(announcement, BorderLayout.SOUTH)
        area.add(top, BorderLayout.NORTH)

        feed.contentType = "text/html"
        feed.isEditable = false
        feed.editorKit = detailHtmlKit()
        feed.border = JBUI.Borders.empty()
        feed.text = ChatConsoleHtml.render(emptyList(), false, emptySet(), emptySet(), emptySet())
        feed.caretPosition = 0

        feedScroll.border = JBUI.Borders.empty()
        feedScroll.viewport.border = JBUI.Borders.empty()
        area.add(feedScroll, BorderLayout.CENTER)
        return area
    }

    // ------------------------------------------------------------- input area

    /**
     * The composer: the question, the three modes, the scope chip and Send.
     *
     * It is ALWAYS there (ADR-0036 decision 6): the old panel stacked "query on
     * top, results below, detail below that", and the bottom of the window was
     * a status line that had to be squeezed out of the way.
     */
    private fun inputArea(): JPanel {
        val area = JPanel(BorderLayout(0, 0))
        area.border = JBUI.Borders.compound(
            JBUI.Borders.customLineTop(JBColor.border()),
            JBUI.Borders.empty(6, 8),
        )

        val composer = JPanel(BorderLayout(0, JBUI.scale(5)))
        inputField.placeholder = mode.placeholder
        inputField.onSubmit = { submit() }
        inputField.onResize = { fitInput() }
        inputField.addComponentListener(object : java.awt.event.ComponentAdapter() {
            override fun componentResized(e: java.awt.event.ComponentEvent) = fitInput()
        })
        fieldScroll.border = BorderFactory.createCompoundBorder(
            BorderFactory.createLineBorder(JBColor.border()),
            JBUI.Borders.empty(1, 1),
        )
        composer.add(fieldScroll, BorderLayout.CENTER)

        val row = JPanel(BorderLayout(JBUI.scale(6), 0))
        row.isOpaque = false
        row.add(modeButtons(), BorderLayout.WEST)
        row.add(scopeChip(), BorderLayout.CENTER)
        row.add(sendButton(), BorderLayout.EAST)
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

    /** The three knowledge faces, as one selected at a time. */
    private fun modeButtons(): JPanel {
        val group = ButtonGroup()
        val panel = JPanel(GridLayout(1, 3, JBUI.scale(3), 0))
        panel.isOpaque = false
        for (candidate in ChatMode.entries) {
            val button = JToggleButton(candidate.label)
            modeToggles[candidate] = button
            button.isSelected = candidate == mode
            button.toolTipText = candidate.placeholder
            group.add(button)
            button.addActionListener {
                // A ButtonGroup lets the last toggle switch itself off; the
                // panel always has exactly one mode, so it goes back on.
                if (!button.isSelected) {
                    button.isSelected = true
                    return@addActionListener
                }
                switchMode(candidate)
            }
            panel.add(button)
        }
        return panel
    }

    /** The scope chip: which project the panel is locked to. */
    private fun scopeChip(): JPanel {
        val panel = JPanel(BorderLayout())
        panel.isOpaque = false
        scopeChipButton.toolTipText = ""
        scopeChipButton.addActionListener { onScopeChipClicked() }
        panel.add(scopeChipButton, BorderLayout.EAST)
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

    /** The send button. */
    private fun sendButton(): JButton {
        val button = JButton("Send")
        button.addActionListener { submit() }
        return button
    }

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
            is ChatEvent.Announce -> showAnnouncement(event.level, event.message)
            is ChatEvent.Scope -> renderScope()
            is ChatEvent.Mode -> renderMode()
            // The candidates arrived: the chip now has something to offer, so
            // it says "choose a project" instead of "no project".
            is ChatEvent.Candidates -> renderScope()
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
        modeToggles.forEach { (candidate, button) -> button.isSelected = candidate == mode }
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
                chip.isEnabled = true
            }
            scope.locked -> {
                chip.text = scopeLabel("🔒 " + scope.projectName)
                chip.toolTipText = "Locked to ${scope.projectName} — click to search across all projects."
                chip.isEnabled = true
            }
            else -> {
                chip.text = "All projects"
                chip.toolTipText = "Searching across all projects — click to lock back to ${scope.projectName}."
                chip.isEnabled = true
            }
        }
    }

    /** The scope chip's wording is rendered by [renderScope]. */

    /**
     * The announcement bar: one line, with a background, dismissible.
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
            // The idle state's examples: they switch the knowledge face and
            // put the cursor in the composer, so the reader's next move is
            // one keystroke away.
            "example" -> {
                switchMode(
                    when (parts.getOrNull(1)) {
                        "notes" -> ChatMode.NOTES
                        "pages" -> ChatMode.PAGES
                        else -> ChatMode.ASK
                    },
                )
                inputField.requestFocusInWindow()
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
                }
                renderFeed()
            }
            "card" -> {
                val turnId = parts.getOrNull(1) ?: return
                val cardType = parts.getOrNull(2) ?: return
                val cardId = parts.getOrNull(3)?.toLongOrNull() ?: return
                when (parts.getOrNull(4)) {
                    "toggle" -> toggleCard(turnId, cardType, cardId)
                    "open" -> openCard(turnId, cardType, cardId)
                    "copy" -> copyCard(turnId, cardType, cardId)
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
     * Deep-link a card into the Cinder web UI.
     *
     * Deep links are best-effort (ADR-0035 decision 6): a headless server
     * started without `--web-dist` answers the API but 404s every other path,
     * so opening the URL would drop the user on a dead page with no
     * explanation at all. The probe asks the server whether it serves the UI
     * first — the same question, the same way, the JetBrains plugin asks.
     */
    private fun openCard(turnId: String, cardType: String, cardId: Long) {
        val hit = card(turnId, cardType, cardId)?.hit ?: return
        val serverUrl = RepotorySettings.serverUrl()
        val url = repotoryClient.deepLink(serverUrl, hit)
        background({ repotoryClient.servesWebUi(serverUrl) }) { servesUi ->
            if (!servesUi) {
                offerFallback(url)
            } else {
                showAnnouncement(ChatLevel.INFO, "Opened $url")
                BrowserUtil.browse(url)
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
            "Cinder answered, but it is not serving its web UI, so this link lands on a 404 page.\n\n" +
                "Either restart the running server with:\n" +
                "  repotory-server --port 18765 --web-dist <release bundle>/web\n" +
                "or stop it and press the start button, which launches the release bundle " +
                "together with its web/ directory.\n\n" +
                "URL: $url",
            "Cinder: web UI not available",
            arrayOf("Open Anyway", "Copy URL", "Cancel"),
            2,
            Messages.getWarningIcon(),
        )
        when (choice) {
            0 -> BrowserUtil.browse(url)
            1 -> {
                CopyPasteManager.getInstance().setContents(StringSelection(url))
                showAnnouncement(ChatLevel.INFO, "URL copied to clipboard")
            }
            else -> showAnnouncement(ChatLevel.INFO, "Cancelled — the link would have 404ed")
        }
    }

    /** Copy the card's title and body to the clipboard. */
    private fun copyCard(turnId: String, cardType: String, cardId: Long) {
        val found = card(turnId, cardType, cardId) ?: return
        val hit = found.hit
        val body = when (val cardBody = found.body) {
            null -> hit.snippet
            is CardBody.NoteBody -> cardBody.note.body
            is CardBody.PageBody -> cardBody.page.content
            is CardBody.TextBody -> cardBody.text
        }
        val text = buildString {
            appendLine(hit.title.ifBlank { "(untitled)" })
            appendLine()
            append(body)
        }
        CopyPasteManager.getInstance().setContents(StringSelection(text))
        showAnnouncement(ChatLevel.INFO, "Copied to clipboard")
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
     * The route vocabulary belongs to the frontend (web/src/App.tsx): a note
     * opens the project page on the notes tab with ?noteId=.
     */
    private fun hitOf(note: NoteDetail): RepotoryHit = RepotoryHit(
        type = "note",
        id = note.id,
        projectId = note.projectId,
        title = note.title,
        snippet = snippetPlain(note.body, 140),
        route = "/project/${note.projectId}?noteId=${note.id}",
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
        pill.state = LinkState.CHECKING
        background({
            // The channel the panel actually depends on is MCP (ADR-0035),
            // not the local service: ask, search, read, write and organize all
            // work with no server running. The probe asks whether the MCP
            // binary is there — cheap, and it spawns nothing.
            McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        }) { resolution ->
            if (resolution.found) {
                pill.state = LinkState.ONLINE
            } else {
                pill.state = LinkState.OFFLINE
                showAnnouncement(ChatLevel.WARN, resolution.problem ?: "repotory-mcp was not found.")
            }
        }
    }

    /**
     * The pill was clicked (or Refresh was pressed): check the channel again.
     */
    private fun reconnect() {
        if (disposed) return
        pill.state = LinkState.CHECKING
        background({
            McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        }) { resolution ->
            if (resolution.found) {
                pill.state = LinkState.ONLINE
                showAnnouncement(ChatLevel.INFO, "Cinder MCP channel ready.")
            } else {
                pill.state = LinkState.OFFLINE
                showAnnouncement(ChatLevel.WARN, resolution.problem ?: "repotory-mcp was not found.")
                offerMcpBinaryOnce()
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
     * Starts the local Cinder service.
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
        showAnnouncement(ChatLevel.INFO, "Starting the local Cinder service…")
        val serverUrl = RepotorySettings.serverUrl()
        background({
            repotoryServer.start(serverUrl) { message ->
                ApplicationManager.getApplication().invokeLater {
                    if (!disposed) showAnnouncement(ChatLevel.INFO, message)
                }
            }
        }) { outcome ->
            starting = false
            when (outcome) {
                is StartOutcome.Started -> {
                    if (!outcome.healthy) {
                        // The process is up but /health never answered:
                        // say so instead of promising a service that is
                        // not there yet.
                        pill.state = LinkState.STARTING
                        showAnnouncement(
                            ChatLevel.WARN,
                            "The server process is running but is not answering yet — " +
                                "click the status pill to retry.",
                        )
                    } else {
                        pill.state = LinkState.ONLINE
                        showAnnouncement(
                            ChatLevel.INFO,
                            if (outcome.webUi) {
                                "Service on port ${outcome.port}."
                            } else {
                                "Service on port ${outcome.port} (API-only — “Open in Cinder” would 404)."
                            },
                        )
                    }
                }
                StartOutcome.AlreadyRunning -> {
                    pill.state = LinkState.ONLINE
                    showAnnouncement(ChatLevel.INFO, "The service is already running.")
                }
                is StartOutcome.BinaryMissing -> {
                    pill.state = LinkState.OFFLINE
                    showAnnouncement(
                        ChatLevel.WARN,
                        "No repotory-server binary found — set its path in Settings | Tools | Cinder.",
                    )
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
                    showAnnouncement(ChatLevel.ERROR, outcome.message)
                }
            }
        }
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
 * The announcement bar: one line, with a background, dismissible.
 *
 * It replaces the old status line (ADR-0036 decision 5): the status of the
 * panel is not a footnote under the conversation, it is a banner above it.
 */
private class AnnouncementBar : JPanel(BorderLayout(JBUI.scale(6), 0)) {
    private val message = AnnouncementText()
    private val dismiss = JButton("✕")

    /** Called when the reader dismisses the banner. */
    var onDismiss: (() -> Unit)? = null

    init {
        isVisible = false
        border = JBUI.Borders.compound(
            JBUI.Borders.customLineBottom(JBColor.border()),
            JBUI.Borders.empty(5, 8),
        )
        message.foreground = JBColor.foreground()
        dismiss.margin = Insets(0, 2, 0, 2)
        dismiss.toolTipText = "Dismiss"
        dismiss.addActionListener { onDismiss?.invoke() }
        add(message, BorderLayout.CENTER)
        add(dismiss, BorderLayout.EAST)
    }

    /** Show a message, replacing whatever was there. */
    fun show(level: ChatLevel, text: String) {
        message.text = text
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
 * It wraps: the messages say things like "The local Cinder service is not
 * reachable at http://127.0.0.1:18765 — the project lock needs it, so the
 * panel searches across all projects", and a label that cannot wrap either
 * clips that sentence or claims the whole width of the panel and pushes the
 * dismiss button off the edge.
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

/**
 * The connection pill: a coloured dot, the state, and a click that reconnects.
 *
 * It replaces the old "start the service" button, which only appeared after
 * a search had already failed. The pill is always visible, so the reader
 * always knows whether the channel is there — and clicking it is the way to
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
        addMouseListener(object : java.awt.event.MouseAdapter() {
            override fun mouseClicked(e: java.awt.event.MouseEvent) {
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
                "The Cinder MCP channel is ready — asking, searching, reading and writing work now. " +
                    "The local service is only needed for deep links and moving a note between projects. " +
                    "Click to re-check.",
            )
            LinkState.OFFLINE -> Triple(
                JBColor(Color(0xC0, 0x3A, 0x2B), Color(0xE8, 0x6C, 0x5A)),
                "offline",
                "The Cinder MCP channel is not available — repotory-mcp was not found. " +
                    "Click to locate it.",
            )
            LinkState.STARTING -> Triple(
                JBColor(Color(0xB4, 0x8A, 0x2F), Color(0xE2, 0xC0, 0x6A)),
                "starting…",
                "The local Cinder service is starting. Click to re-check.",
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

/** "Open in Cinder" as a toolbar action, so the tool window has a title bar button. */
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
