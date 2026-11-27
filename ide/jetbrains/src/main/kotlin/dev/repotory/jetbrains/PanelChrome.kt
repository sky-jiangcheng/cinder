// The panel's own chrome — the small painted widgets the tool window is made of.
//
// Why this file exists: the header used to be two platform `ActionToolbar`s
// whose actions carry no icon, so Swing drew them as TEXT buttons
// ("Conversations", "New", "AI provider", "Desktop app") — six labels and a
// bordered "online" pill spent the top of a 300px tool window on chrome. The
// VS Code panel draws the same actions as icon buttons with a hover fill, and
// the two surfaces are supposed to be the same product (ADR-0036 decision 8),
// so the JetBrains side now paints its own.
//
// Two rules this file keeps:
//   · every colour comes from the IDE theme (JBColor pairs for light/dark),
//     never a fixed value — a panel that ignores the theme looks foreign in
//     half the IDE's schemes;
//   · the icons are line glyphs on the same 16-unit grid as the VS Code
//     panel's inline SVGs, so the two headers read the same at a glance.
package dev.repotory.jetbrains

import com.intellij.ui.JBColor
import com.intellij.util.ui.JBUI
import java.awt.BasicStroke
import java.awt.Color
import java.awt.Component
import java.awt.Cursor
import java.awt.Dimension
import java.awt.Font
import java.awt.FontMetrics
import java.awt.Graphics
import java.awt.Graphics2D
import java.awt.Insets
import java.awt.RenderingHints
import java.awt.geom.Path2D
import javax.swing.BorderFactory
import javax.swing.BoxLayout
import javax.swing.Icon
import javax.swing.JButton
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel

/**
 * What the dot knows: whether the thing the panel talks to is ON DISK.
 *
 * Not "connected": the probe resolves the cinder-mcp binary (cheap, spawns
 * nothing), so a green dot is a statement about a file, not about a channel
 * that answered. Nothing ever set STARTING — the dot does not watch a service
 * start — so that state is gone rather than left in the tooltip as a promise
 * about a flow that no longer exists.
 */
internal enum class LinkState {
    ONLINE,
    OFFLINE,
    CHECKING,
}

/** The panel's colours, pulled from the IDE theme. */
internal object PanelPalette {
    /** The fill of a selected mode pill and the send key. */
    val accent: Color get() = JBColor.namedColor("Button.default.startBackground", JBColor(Color(0x35, 0x74, 0xF0), Color(0x4A, 0x88, 0xC7)))

    /** Text/icons drawn ON the accent fill. */
    val onAccent: Color get() = JBColor.namedColor("Button.default.foreground", JBColor(Color.WHITE, Color.WHITE))

    /** Secondary text: metadata, hints, unselected pills. */
    val muted: Color get() = JBColor.namedColor("Component.infoForeground", JBColor(Color(0x6C, 0x70, 0x76), Color(0x9F, 0xA5, 0xAB)))

    /** The hover wash of an icon button — translucent, so the row stays flat. */
    val hover: Color get() = JBColor(Color(0x00, 0x00, 0x00, 0x22), Color(0xFF, 0xFF, 0xFF, 0x26))

    /** The pressed wash: the same hue, a step darker. */
    val pressed: Color get() = JBColor(Color(0x00, 0x00, 0x00, 0x38), Color(0xFF, 0xFF, 0xFF, 0x3C))

    /** Hairline borders (chips, the composer field). */
    val line: Color get() = JBColor.border()

    /**
     * The wash behind a segmented control.
     *
     * Mixed from the theme's own two colours, not from a fixed pair: the
     * track has to sit 6% of the way from the background toward the
     * foreground in EVERY scheme, including the high-contrast ones where a
     * hard-coded light grey turns into a foreign block.
     */
    val track: Color
        get() {
            val bg = JBColor.background()
            val fg = JBColor.foreground()
            fun mix(base: Int, toward: Int): Int = (base + (toward - base) * 0.06).toInt()
            return Color(mix(bg.red, fg.red), mix(bg.green, fg.green), mix(bg.blue, fg.blue))
        }
}

/**
 * The line glyphs the panel draws.
 *
 * Each painter works in a 16×16 unit space and is scaled to the icon size, so
 * one definition serves the 16px header and any larger size later. The paths
 * are the VS Code panel's own (`ICON_HISTORY`, `ICON_PLUS`, `ICON_GEAR`,
 * `ICON_SEND`, `ICON_LOCK`), so the two headers are the same drawing at the
 * same weight rather than two interpretations of "a gear".
 */
internal object PanelIcons {

    fun history(size: Int = JBUI.scale(16)): Icon = lineIcon(size) { g ->
        g.draw(Path2D.Float().apply {
            moveTo(2.5f, 4f); lineTo(13.5f, 4f)
            moveTo(2.5f, 8f); lineTo(10.5f, 8f)
            moveTo(2.5f, 12f); lineTo(8f, 12f)
        })
    }

    fun plus(size: Int = JBUI.scale(16)): Icon = lineIcon(size) { g ->
        g.draw(Path2D.Float().apply {
            moveTo(8f, 2.8f); lineTo(8f, 13.2f)
            moveTo(2.8f, 8f); lineTo(13.2f, 8f)
        })
    }

    /**
     * A cog, not a sun: a hub plus eight radial spokes with no rim reads as a
     * sun at 16px. The rim (r 4.6) and the hole are what make it a gear; the
     * eight teeth only have to say "teeth", not be gear-accurate.
     */
    fun gear(size: Int = JBUI.scale(16)): Icon = lineIcon(size) { g ->
        g.draw(java.awt.geom.Ellipse2D.Float(6.1f, 6.1f, 3.8f, 3.8f))
        g.draw(java.awt.geom.Ellipse2D.Float(3.4f, 3.4f, 9.2f, 9.2f))
        g.draw(Path2D.Float().apply {
            moveTo(8f, 1f); lineTo(8f, 2.4f)
            moveTo(8f, 13.6f); lineTo(8f, 15f)
            moveTo(1f, 8f); lineTo(2.4f, 8f)
            moveTo(13.6f, 8f); lineTo(15f, 8f)
            moveTo(3.2f, 3.2f); lineTo(4.2f, 4.2f)
            moveTo(11.8f, 11.8f); lineTo(12.8f, 12.8f)
            moveTo(12.8f, 3.2f); lineTo(11.8f, 4.2f)
            moveTo(4.2f, 11.8f); lineTo(3.2f, 12.8f)
        })
    }

    /** The scope chip's lock — the project the panel is pinned to. */
    fun lock(size: Int = JBUI.scale(12)): Icon = lineIcon(size) { g ->
        g.draw(java.awt.geom.RoundRectangle2D.Float(3.6f, 7f, 8.8f, 6.4f, 2.4f, 2.4f))
        g.draw(Path2D.Float().apply {
            moveTo(5.8f, 7f); lineTo(5.8f, 5.4f)
            curveTo(5.8f, 4.0f, 10.2f, 4.0f, 10.2f, 5.4f)
            lineTo(10.2f, 7f)
        })
    }

    /** The banner's dismiss mark. */
    fun close(size: Int = JBUI.scale(16)): Icon = lineIcon(size) { g ->
        g.draw(Path2D.Float().apply {
            moveTo(4.6f, 4.6f); lineTo(11.4f, 11.4f)
            moveTo(11.4f, 4.6f); lineTo(4.6f, 11.4f)
        })
    }

    /** The send key: an arrow up, drawn in white on the accent fill. */
    fun send(size: Int = JBUI.scale(16)): Icon = lineIcon(size, PanelPalette.onAccent) { g ->
        g.draw(Path2D.Float().apply {
            moveTo(8f, 13.2f); lineTo(8f, 3.4f)
            moveTo(4f, 7.4f); lineTo(8f, 3.4f); lineTo(12f, 7.4f)
        })
    }

    private fun lineIcon(size: Int, color: Color? = null, paint: (Graphics2D) -> Unit): Icon =
        object : Icon {
            override fun getIconWidth(): Int = size

            override fun getIconHeight(): Int = size

            override fun paintIcon(c: Component?, g: Graphics, x: Int, y: Int) {
                val g2 = g.create() as Graphics2D
                try {
                    g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
                    g2.setRenderingHint(RenderingHints.KEY_STROKE_CONTROL, RenderingHints.VALUE_STROKE_PURE)
                    g2.color = color ?: JBColor.foreground()
                    g2.translate(x, y)
                    val scale = size / 16.0
                    g2.scale(scale, scale)
                    g2.stroke = BasicStroke(1.4f, BasicStroke.CAP_ROUND, BasicStroke.JOIN_ROUND)
                    paint(g2)
                } finally {
                    g2.dispose()
                }
            }
        }
}

/**
 * A 24px icon button with a hover wash and no platform frame.
 *
 * The platform's own toolbar draws text for an iconless action and a
 * bordered rectangle for everything else — the exact look the header was
 * criticized for. This paints the VS Code shape: a rounded square that only
 * shows anything on hover, focus or press.
 */
internal class IconButton(
    icon: Icon,
    tooltip: String,
    private val accent: Boolean = false,
    size: Int = 24,
    private val onClick: () -> Unit,
) : JButton(icon) {

    init {
        toolTipText = tooltip
        isContentAreaFilled = false
        isBorderPainted = false
        isFocusPainted = false
        isOpaque = false
        isRolloverEnabled = true
        margin = Insets(0, 0, 0, 0)
        border = JBUI.Borders.empty()
        cursor = Cursor.getPredefinedCursor(Cursor.HAND_CURSOR)
        preferredSize = Dimension(JBUI.scale(size), JBUI.scale(size))
        minimumSize = preferredSize
        maximumSize = preferredSize
        addActionListener { onClick() }
    }

    override fun paintComponent(g: Graphics) {
        val g2 = g.create() as Graphics2D
        try {
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            val arc = JBUI.scale(6)
            when {
                accent -> {
                    g2.color = PanelPalette.accent
                    g2.fillRoundRect(0, 0, width, height, arc, arc)
                }
                model.isPressed -> {
                    g2.color = PanelPalette.pressed
                    g2.fillRoundRect(0, 0, width, height, arc, arc)
                }
                model.isRollover || hasFocus() -> {
                    g2.color = PanelPalette.hover
                    g2.fillRoundRect(0, 0, width, height, arc, arc)
                }
            }
            if (hasFocus() && !accent) {
                g2.color = PanelPalette.accent
                g2.drawRoundRect(0, 0, width - 1, height - 1, arc, arc)
            }
        } finally {
            g2.dispose()
        }
        super.paintComponent(g)
    }
}

/**
 * The track the three mode pills sit in — one choice, one control.
 *
 * Three bordered pills read as three unrelated switches, and on a 300px panel
 * they took every pixel of the row. The track says what they are (a
 * one-of-three choice) before the reader has to work it out, and the chosen
 * segment is the only filled thing on it.
 */
internal class SegmentTrack : JPanel(java.awt.FlowLayout(java.awt.FlowLayout.LEFT, 2, 0)) {

    init {
        isOpaque = false
        border = JBUI.Borders.empty(2)
    }

    override fun paintComponent(g: Graphics) {
        val g2 = g.create() as Graphics2D
        try {
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            val arc = JBUI.scale(9)
            g2.color = PanelPalette.track
            g2.fillRoundRect(0, 0, width - 1, height - 1, arc, arc)
            g2.color = PanelPalette.line
            g2.drawRoundRect(0, 0, width - 1, height - 1, arc, arc)
        } finally {
            g2.dispose()
        }
    }
}

/**
 * A pill: the mode group's members and the project chip are the same shape.
 *
 * Selected = filled with the accent (the VS Code `.mode-btn.active`), not
 * selected = a hairline border over the panel. Text stays 11px: the pills sit
 * under the composer, where 13px would compete with the input itself.
 */
internal open class PillBase(
    text: String,
    tooltip: String,
    private val tracked: Boolean = false,
) : JButton(text) {

    init {
        toolTipText = tooltip
        isContentAreaFilled = false
        isBorderPainted = false
        isFocusPainted = false
        isOpaque = false
        isRolloverEnabled = true
        font = font.deriveFont(Font.PLAIN, font.size2D - 1.5f)
        border = if (tracked) JBUI.Borders.empty(2, 8) else JBUI.Borders.empty(3, 8)
        cursor = Cursor.getPredefinedCursor(Cursor.HAND_CURSOR)
    }

    /** True when the pill draws as chosen (filled with the accent). */
    protected open fun active(): Boolean = false

    /** The hairline of an unselected pill — none inside a track: the track draws it. */
    protected open fun lineColor(): Color = if (tracked) Color(0, 0, 0, 0) else PanelPalette.line

    /** The label colour: muted until the pointer is on it, unless chosen. */
    protected open fun textColor(chosen: Boolean, hover: Boolean): Color = when {
        chosen -> PanelPalette.onAccent
        hover -> JBColor.foreground()
        else -> PanelPalette.muted
    }

    override fun getPreferredSize(): Dimension {
        val base = super.getPreferredSize()
        return Dimension(base.width, maxOf(base.height, JBUI.scale(if (tracked) 20 else 24)))
    }

    override fun paintComponent(g: Graphics) {
        val g2 = g.create() as Graphics2D
        val active = active()
        try {
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            val arc = JBUI.scale(7)
            val on = if (model.isPressed) PanelPalette.pressed else PanelPalette.hover
            when {
                active -> {
                    g2.color = PanelPalette.accent
                    g2.fillRoundRect(0, 0, width, height, arc, arc)
                }
                model.isRollover || hasFocus() -> {
                    g2.color = on
                    g2.fillRoundRect(0, 0, width, height, arc, arc)
                }
                else -> {
                    g2.color = lineColor()
                    g2.drawRoundRect(0, 0, width - 1, height - 1, arc, arc)
                }
            }
            if (hasFocus()) {
                g2.color = PanelPalette.accent
                g2.drawRoundRect(0, 0, width - 1, height - 1, arc, arc)
            }
        } finally {
            g2.dispose()
        }
        foreground = textColor(active, model.isRollover || hasFocus())
        super.paintComponent(g)
    }
}

/** One member of the mode group (Ask / Notes / Pages). */
internal class ModePill(text: String, tooltip: String) : PillBase(text, tooltip, tracked = true) {

    /**
     * Chosen, set by the panel — not `isSelected`.
     *
     * The three modes are one-of-three, and the panel already knows which one
     * it is (it renders from `mode`); a ButtonGroup would add a second copy of
     * that state, and the copy drifts the moment a mode changes from code.
     */
    var chosen: Boolean = false
        set(value) {
            if (field == value) return
            field = value
            repaint()
        }

    override fun active(): Boolean = chosen
}

/**
 * The project chip — a pill that says the panel's scope in words.
 *
 * It used to be a `JButton` with the platform's default look, which read as a
 * form control next to the mode group. The lock is DRAWN now: the 🔒 emoji
 * came out as another vendor's coloured glyph on some platforms, which is the
 * one picture on the panel that does not belong to the theme.
 */
internal class ChipButton(text: String, tooltip: String) : PillBase(text, tooltip) {

    /** True when the panel is locked to a project: accent hairline + lock glyph. */
    var locked: Boolean = false
        set(value) {
            if (field == value) return
            field = value
            icon = if (value) PanelIcons.lock(JBUI.scale(12)) else null
            repaint()
        }

    init {
        iconTextGap = JBUI.scale(4)
    }

    override fun lineColor(): Color = if (locked) PanelPalette.accent else PanelPalette.line

    /** A locked chip is a statement of scope, not a hint: full contrast. */
    override fun textColor(chosen: Boolean, hover: Boolean): Color =
        if (locked) JBColor.foreground() else super.textColor(chosen, hover)
}

/**
 * The channel state as one dot, not a bordered pill with a word in it.
 *
 * The VS Code panel has no permanent status line either: a healthy channel
 * draws nothing worth reading, and a broken one is announced in the bar above
 * the feed. Clicking the dot re-checks the channel, which is the one thing
 * the old pill did besides take a row of width.
 */
internal class StatusDot : JComponent() {

    var onClick: (() -> Unit)? = null

    var state: LinkState = LinkState.CHECKING
        set(value) {
            field = value
            toolTipText = tip(value)
            repaint()
        }

    init {
        isOpaque = false
        cursor = Cursor.getPredefinedCursor(Cursor.HAND_CURSOR)
        toolTipText = tip(state)
        preferredSize = Dimension(JBUI.scale(22), JBUI.scale(22))
        minimumSize = preferredSize
        maximumSize = preferredSize
        addMouseListener(object : java.awt.event.MouseAdapter() {
            override fun mouseClicked(e: java.awt.event.MouseEvent) = onClick?.invoke() ?: Unit
        })
    }

    private fun tip(value: LinkState): String = when (value) {
        // Says what was measured. The probe resolves the binary; it never
        // calls a tool, so "asking and searching work now" would be a claim
        // the dot has no evidence for.
        LinkState.ONLINE ->
            "repotory-mcp was found (PATH, Homebrew/Scoop or ~/.local/bin) — every ask, search, " +
                "read and write goes through it. The local service is only needed for moving a " +
                "note between projects. Click to re-check."
        LinkState.OFFLINE ->
            "repotory-mcp was not found — the panel has nothing to talk to. Click to locate it."
        LinkState.CHECKING -> "Checking whether repotory-mcp is on this machine."
    }

    private fun dotColor(value: LinkState): Color = when (value) {
        LinkState.ONLINE -> JBColor(Color(0x3F, 0x8F, 0x4E), Color(0x73, 0xBD, 0x79))
        LinkState.OFFLINE -> JBColor(Color(0xC0, 0x3A, 0x2B), Color(0xE8, 0x6C, 0x5A))
        LinkState.CHECKING -> PanelPalette.muted
    }

    override fun paintComponent(g: Graphics) {
        val g2 = g.create() as Graphics2D
        try {
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            val dot = JBUI.scale(7)
            val x = (width - dot) / 2
            val y = (height - dot) / 2
            if (isRollover()) {
                g2.color = PanelPalette.hover
                g2.fillRoundRect(0, 0, width, height, JBUI.scale(6), JBUI.scale(6))
            }
            g2.color = dotColor(state)
            g2.fillOval(x, y, dot, dot)
        } finally {
            g2.dispose()
        }
    }

    /** Swing's rollover flag lives on the model; a JComponent has none. */
    private fun isRollover(): Boolean = mousePosition != null
}

/**
 * The composer's frame: the input and the send key in ONE rounded box.
 *
 * Windsurf's shape and the VS Code panel's (`.send` sits inside the
 * textarea): a separate Send button under the field made the composer look
 * like a form, and the button's own width came out of the question's.
 */
internal class ComposerField : JPanel(java.awt.BorderLayout(JBUI.scale(2), 0)) {

    /** True while the text field inside has the focus — the border turns accent. */
    var focused: Boolean = false
        set(value) {
            if (field == value) return
            field = value
            repaint()
        }

    init {
        isOpaque = false
        border = JBUI.Borders.empty(1)
    }

    override fun paintComponent(g: Graphics) {
        val g2 = g.create() as Graphics2D
        try {
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            val arc = JBUI.scale(8)
            g2.color = com.intellij.util.ui.UIUtil.getTextFieldBackground()
            g2.fillRoundRect(0, 0, width - 1, height - 1, arc, arc)
            g2.color = if (focused) PanelPalette.accent else PanelPalette.line
            g2.drawRoundRect(0, 0, width - 1, height - 1, arc, arc)
        } finally {
            g2.dispose()
        }
        super.paintComponent(g)
    }
}

/**
 * The announcement bar's level badge: a filled circle with a glyph.
 *
 * The bar used to be a tinted rectangle with a sentence in it and nothing to
 * separate "this failed" from "this is done" except the colour — which a
 * colour-blind reader cannot use.
 */
internal class LevelBadgeIcon(private val level: ChatLevel, private val size: Int = JBUI.scale(14)) : Icon {

    override fun getIconWidth(): Int = size

    override fun getIconHeight(): Int = size

    override fun paintIcon(c: Component?, g: Graphics, x: Int, y: Int) {
        val g2 = g.create() as Graphics2D
        try {
            g2.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
            g2.color = color()
            g2.fillOval(x, y, size, size)
            g2.color = JBColor(Color.WHITE, Color(0x1E, 0x1E, 0x1E))
            val glyph = when (level) {
                ChatLevel.ERROR -> "!"
                ChatLevel.WARN -> "!"
                ChatLevel.INFO -> "i"
            }
            g2.font = g2.font.deriveFont(Font.BOLD, size * 0.72f)
            val metrics: FontMetrics = g2.fontMetrics
            g2.drawString(
                glyph,
                x + (size - metrics.stringWidth(glyph)) / 2,
                y + (size + metrics.ascent - metrics.descent) / 2,
            )
        } finally {
            g2.dispose()
        }
    }

    private fun color(): Color = when (level) {
        ChatLevel.ERROR -> JBColor(Color(0xC0, 0x3A, 0x2B), Color(0xE8, 0x6C, 0x5A))
        ChatLevel.WARN -> JBColor(Color(0xB4, 0x8A, 0x2F), Color(0xE2, 0xC0, 0x6A))
        ChatLevel.INFO -> JBColor(Color(0x35, 0x74, 0xF0), Color(0x4A, 0x88, 0xC7))
    }
}

/**
 * A settings row: a title, an optional hint, and the control itself.
 *
 * The settings pages used to be a flat `FormBuilder` column where a field, a
 * button and a paragraph of status text sat at the same level — nothing said
 * which button belonged to which field. This groups them without adding a
 * new form abstraction: the repo only needed the spacing and the wording.
 */
internal class SettingsSection(
    title: String,
    hint: String? = null,
) : JPanel() {

    init {
        layout = BoxLayout(this, BoxLayout.Y_AXIS)
        isOpaque = false
        border = JBUI.Borders.empty(0, 0, JBUI.scale(14), 0)
        add(fieldLabel(title))
        hint?.let {
            add(JLabel("<html><body style='width:420px'>" + escape(it) + "</body></html>").apply {
                foreground = PanelPalette.muted
                border = JBUI.Borders.emptyTop(JBUI.scale(2))
                alignmentX = LEFT_ALIGNMENT
            })
        }
    }

    /** Add one control, full width, one step under the title. */
    fun row(component: JComponent): SettingsSection {
        component.alignmentX = LEFT_ALIGNMENT
        if (component is JLabel) component.foreground = PanelPalette.muted
        component.border = BorderFactory.createCompoundBorder(
            JBUI.Borders.emptyTop(JBUI.scale(5)),
            component.border ?: JBUI.Borders.empty(),
        )
        add(component)
        return this
    }

    private fun fieldLabel(text: String): JLabel = JLabel(text.uppercase()).apply {
        font = font.deriveFont(Font.BOLD, font.size2D - 1f)
        foreground = PanelPalette.muted
        alignmentX = LEFT_ALIGNMENT
    }

    private fun escape(text: String): String =
        text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}
