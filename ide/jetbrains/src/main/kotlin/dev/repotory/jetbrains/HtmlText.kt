// The HTML the panel's panes are built from: escaping, theme colour, and the
// markdown subset.
//
// JEditorPane speaks a small dialect of HTML 3.2 with partial CSS, so the
// builders here stay inside it: no flexbox, no CSS variables, no external
// stylesheets — a <style> block on the kit plus inline styles, and colours
// pulled from the IDE's own UIManager keys so the pane follows the theme.
//
// Note bodies are markdown. A wall of raw asterisks would undo the point of a
// reader, so MarkdownLite renders the common subset (fenced code, headings,
// bold, inline code, links, lists, paragraphs) and ESCAPES everything else:
// unknown syntax is shown verbatim, never dropped and never interpreted.
package dev.repotory.jetbrains

import com.intellij.ui.JBColor
import java.awt.Color
import javax.swing.UIManager
import javax.swing.text.html.HTMLEditorKit
import javax.swing.text.html.StyleSheet

/*
 * There used to be a second, static detail pane in this file — the pane a
 * result replaced instead of joining a conversation (promoHtml, noteHtml,
 * wikiPageHtml, and their "Open in Cinder" line). The feed replaced it
 * (ADR-0036 / ADR-0044), and once the panel stopped jumping out to the web UI
 * there was nothing left for that pane to say, so it is gone rather than kept
 * as a second way to render the same note.
 */

/** The IDE's own secondary-text colour as #rrggbb, for HTML the theme cannot tint. */
internal fun metaColorHex(): String {
    val c: Color = UIManager.getColor("Label.infoForeground")
        ?: UIManager.getColor("Label.foreground")
        ?: JBColor(Color(0x6A, 0x6A, 0x6A), Color(0x94, 0x94, 0x94))
    return String.format("#%02x%02x%02x", c.red, c.green, c.blue)
}

/** An HTMLEditorKit carrying the panes' stylesheet; one per pane is enough. */
internal fun detailHtmlKit(): HTMLEditorKit {
    val kit = HTMLEditorKit()
    val sheet: StyleSheet = kit.styleSheet
    sheet.addRule("body { font-family: sans-serif; font-size: 10pt; margin: 0px; }")
    sheet.addRule("h2 { font-size: 13pt; margin: 0px 0px 6px 0px; }")
    sheet.addRule("h3 { font-size: 11pt; margin: 10px 0px 4px 0px; }")
    sheet.addRule("h4 { font-size: 10pt; margin: 8px 0px 3px 0px; }")
    sheet.addRule("p { margin: 5px 0px; }")
    sheet.addRule("pre { font-family: monospace; font-size: 9pt; margin: 6px 0px; }")
    sheet.addRule("code { font-family: monospace; font-size: 9pt; }")
    sheet.addRule("ul, ol { margin: 5px 0px; }")
    sheet.addRule("a { }")
    return kit
}

/**
 * The label a hit carries in its badge: the note/todo type, or the page kind
 * for an approved page. "WIKI" alone says nothing about what the page IS.
 */
internal fun badgeLabel(hit: RepotoryHit): String = when (hit.type) {
    "wiki" -> hit.kind.ifBlank { "page" }
    else -> hit.type
}

internal fun esc(text: String): String = text
    .replace("&", "&amp;")
    .replace("<", "&lt;")
    .replace(">", "&gt;")
    .replace("\"", "&quot;")

/** One model reply, split into its inline reasoning and the answer. */
internal data class ThinkingSplit(val thoughts: List<String>, val body: String)

/**
 * A gateway that does not separate reasoning_content leaves the model's
 * reasoning inline in content, wrapped in `<think>…</think>`.
 *
 * It is the model's notes, not the answer, so the panes fold it behind one
 * row instead of laying it out as prose — the shape CodeBuddy and the other
 * IDE agents use. An unterminated block is folded too: streaming delivers the
 * open tag long before the close, and the notes must not flash in while the
 * model is still thinking.
 */
internal fun splitThinking(src: String): ThinkingSplit {
    val thoughts = mutableListOf<String>()
    val body = src
        .replace(Regex("<(think|thinking|reason|reasoning)>([\\s\\S]*?)(?:</\\1>|$)", RegexOption.IGNORE_CASE)) { m ->
            val inner = m.groupValues[2].trim()
            if (inner.isNotEmpty()) thoughts.add(inner)
            ""
        }
        .replace(Regex("</?(?:think|thinking|reason|reasoning)>", RegexOption.IGNORE_CASE), "")
        .replace(Regex("^\\s*\\n+"), "")
        .trimEnd()
    return ThinkingSplit(thoughts, body)
}

/** The markdown subset the note store actually uses, rendered to the panes' HTML. */
internal object MarkdownLite {

    fun toHtml(src: String): String {
        if (src.isBlank()) return "<p><i>Empty note.</i></p>"
        val blocks = mutableListOf<String>()
        // Fenced code first, lifted out as placeholders so no other rule
        // can touch their contents. A ```mermaid fence becomes a labelled
        // diagram-source block: Swing has no diagram engine, so the source
        // stays visible (and editable) instead of being silently swallowed
        // — the same contract as the VS Code webview's diagram card.
        var text = splitThinking(src).body.replace(Regex("```([^\\n]*)\\n([\\s\\S]*?)```")) { m ->
            val lang = m.groupValues[1].trim().lowercase()
            val code = m.groupValues[2].trimStart('\n')
            if (lang == "mermaid") {
                blocks.add(
                    "<p style=\"color:${metaColorHex()};font-size:8pt;margin:6px 0 2px 0\">" +
                        "流程图 · mermaid 源码（在 Cinder 桌面应用中渲染为图形）</p>" +
                        "<pre>" + esc(code) + "</pre>",
                )
            } else {
                blocks.add("<pre>" + esc(if (lang.isNotEmpty()) lang + "\n" + code else code) + "</pre>")
            }
            "\u0000${blocks.size - 1}\u0000"
        }
        val inline: (String) -> String = { line ->
            esc(line)
                .replace(Regex("`([^`\\n]+)`"), "<code>$1</code>")
                .replace(Regex("\\*\\*([^*\\n]+)\\*\\*"), "<b>$1</b>")
                .replace(Regex("\\[([^\\]]+)\\]\\(([^)\\s]+)\\)"), "<a href=\"$2\">$1</a>")
        }
        val out = mutableListOf<String>()
        var listTag: String? = null
        fun closeList() {
            listTag?.let { out.add("</$it>") }
            listTag = null
        }
        for (para in text.split(Regex("\n{2,}"))) {
            val p = para.trim()
            if (p.isEmpty()) continue
            if (Regex("^\u0000\\d+\u0000$").matches(p)) {
                closeList()
                out.add(blocks[p.trim('\u0000').toInt()])
                continue
            }
            val buffer = mutableListOf<String>()
            fun flushParagraph() {
                if (buffer.isNotEmpty()) {
                    out.add("<p>" + buffer.map(inline).joinToString("<br>") + "</p>")
                    buffer.clear()
                }
            }
            for (line in p.split("\n")) {
                val heading = Regex("^(#{1,4})\\s+(.*)$").find(line)
                val bullet = Regex("^[-*]\\s+(.*)$").find(line)
                val numbered = Regex("^\\d+[.)]\\s+(.*)$").find(line)
                when {
                    heading != null -> {
                        flushParagraph(); closeList()
                        val level = if (heading.groupValues[1].length <= 1) "h3" else "h4"
                        out.add("<$level>" + inline(heading.groupValues[2]) + "</$level>")
                    }
                    bullet != null -> {
                        flushParagraph()
                        if (listTag != "ul") { closeList(); out.add("<ul>"); listTag = "ul" }
                        out.add("<li>" + inline(bullet.groupValues[1]) + "</li>")
                    }
                    numbered != null -> {
                        flushParagraph()
                        if (listTag != "ol") { closeList(); out.add("<ol>"); listTag = "ol" }
                        out.add("<li>" + inline(numbered.groupValues[1]) + "</li>")
                    }
                    else -> buffer.add(line)
                }
            }
            flushParagraph()
            closeList()
        }
        return out.joinToString("\n")
    }
}
