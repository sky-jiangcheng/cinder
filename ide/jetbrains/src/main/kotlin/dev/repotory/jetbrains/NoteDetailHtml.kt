// HTML rendering for the knowledge reader's detail pane.
//
// JEditorPane speaks a small dialect of HTML 3.2 with partial CSS, so the
// builders here stay inside it: no flexbox, no CSS variables, no external
// stylesheets — a <style> block on the kit plus inline styles, and colours
// pulled from the IDE's own UIManager keys so the pane follows the theme.
//
// Note bodies are markdown. A wall of raw asterisks would undo the point
// of a detail pane, so MarkdownLite renders the common subset (fenced
// code, headings, bold, inline code, links, lists, paragraphs) and
// ESCAPES everything else: unknown syntax is shown verbatim, never
// dropped and never interpreted.
package dev.repotory.jetbrains

import com.intellij.ui.JBColor
import java.awt.Color
import javax.swing.UIManager
import javax.swing.text.html.HTMLEditorKit
import javax.swing.text.html.StyleSheet

/** The IDE's own secondary-text colour as #rrggbb, for HTML the theme cannot tint. */
internal fun metaColorHex(): String {
    val c: Color = UIManager.getColor("Label.infoForeground")
        ?: UIManager.getColor("Label.foreground")
        ?: JBColor(Color(0x6A, 0x6A, 0x6A), Color(0x94, 0x94, 0x94))
    return String.format("#%02x%02x%02x", c.red, c.green, c.blue)
}

/** An HTMLEditorKit carrying the pane's stylesheet; one per pane is enough. */
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

/** The idle pane before the first search — the product pitch, not an error. */
internal fun promoHtml(): String = """
    <html><body style="padding:6px">
    <h2>Knowledge, where you already are.</h2>
    <p>Cinder keeps what your coding agents learn — why a decision was
    made, what gotcha you hit, what to do next — in a local, searchable
    store.</p>
    <p>Type a query on the left. Results open in full, right here — the
    first one is selected for you. Double-click (or <b>Open in Cinder</b>)
    jumps to the item in the Cinder web app.</p>
    <p style="color:${metaColorHex()}">Everything stays on this machine: the
    plugin talks to the loopback-only Cinder service and never uploads
    anything.</p>
    </body></html>
""".trimIndent()

/** Shown the instant a hit is selected, before the full body arrives. */
internal fun loadingHtml(hit: RepotoryHit): String = """
    <html><body style="padding:6px">
    <h2>${esc(hit.title.ifBlank { "(untitled)" })}</h2>
    <p style="color:${metaColorHex()}">${esc(badgeLabel(hit))} · #${hit.id} · loading…</p>
    ${if (hit.snippet.isBlank()) "" else "<p>${snippetHtml(hit.snippet)}</p>"}
    </body></html>
""".trimIndent()

/** A note read in full: meta line, then the markdown body. */
internal fun noteHtml(detail: NoteDetail): String {
    val meta = buildString {
        append("note · #").append(detail.id)
        if (detail.updatedAt.isNotBlank()) append(" · updated ").append(detail.updatedAt.take(10))
        if (detail.tags.isNotBlank()) append(" · ").append(esc(detail.tags))
    }
    return """
        <html><body style="padding:6px">
        <h2>${esc(detail.title.ifBlank { "(untitled)" })}</h2>
        <p style="color:${metaColorHex()}">$meta</p>
        ${MarkdownLite.toHtml(detail.body)}
        </body></html>
    """.trimIndent()
}

/**
 * An approved knowledge page read in full (ADR-0032): meta line, the page
 * body, and the graph around it.
 *
 * The edges are plain text, not links: a JEditorPane renders an <a> as blue
 * underlined text but does not follow it, so a link here would look clickable
 * and do nothing. "Open in Cinder" is the way out of the pane.
 */
internal fun wikiPageHtml(detail: WikiPageDetail): String {
    val meta = buildString {
        append(esc(detail.kind.ifBlank { "page" }))
        append(" · #").append(detail.id)
        if (detail.slug.isNotBlank()) append(" · /").append(esc(detail.slug))
        append(" · ").append(if (detail.projectId > 0) "project ${detail.projectId}" else "global")
        if (detail.updatedAt.isNotBlank()) append(" · updated ").append(detail.updatedAt.take(10))
        if (detail.source.isNotBlank()) append(" · ").append(esc(detail.source))
    }
    return """
        <html><body style="padding:6px">
        <h2>${esc(detail.title.ifBlank { "(untitled)" })}</h2>
        <p style="color:${metaColorHex()}">$meta</p>
        ${MarkdownLite.toHtml(detail.content)}
        ${edgesHtml("Links", detail.outLinks)}
        ${edgesHtml("Backlinks", detail.inLinks)}
        ${notesHtml(detail.sourceNoteIds)}
        </body></html>
    """.trimIndent()
}

/** One graph edge list: "Links: 时态图 (relates-to) · 网关". */
private fun edgesHtml(label: String, edges: List<PageEdge>): String {
    if (edges.isEmpty()) return ""
    val items = edges.joinToString(" · ") { edge ->
        val name = edge.title.ifBlank { edge.slug.ifBlank { "#${edge.pageId}" } }
        if (edge.relation.isBlank()) esc(name) else esc(name) + " (" + esc(edge.relation) + ")"
    }
    return "<p style=\"color:${metaColorHex()}\">$label: $items</p>"
}

/** The notes a page was compiled from: "Compiled from notes: #3 · #7". */
private fun notesHtml(ids: List<Long>): String {
    if (ids.isEmpty()) return ""
    return "<p style=\"color:${metaColorHex()}\">Compiled from notes: " +
        ids.joinToString(" · ") { "#$it" } + "</p>"
}

/**
 * The label a hit carries in its badge: the note/todo type, or the page kind
 * for an approved page. "WIKI" alone says nothing about what the page IS.
 */
internal fun badgeLabel(hit: RepotoryHit): String = when (hit.type) {
    "wiki" -> hit.kind.ifBlank { "page" }
    else -> hit.type
}

/**
 * A todo, or a note whose full read failed: the matched excerpt is what
 * the search contract returned, and the pane says so instead of pretending
 * it is the whole item.
 */
internal fun excerptHtml(hit: RepotoryHit, full: Boolean = false): String {
    val label = if (full) "full body unavailable — matched excerpt" else "excerpt"
    return """
        <html><body style="padding:6px">
        <h2>${esc(hit.title.ifBlank { "(untitled)" })}</h2>
        <p style="color:${metaColorHex()}">${esc(badgeLabel(hit))} · #${hit.id} · $label</p>
        ${if (hit.snippet.isBlank()) "<p><i>No snippet returned.</i></p>" else snippetHtml(hit.snippet)}
        </body></html>
    """.trimIndent()
}

internal fun esc(text: String): String = text
    .replace("&", "&amp;")
    .replace("<", "&lt;")
    .replace(">", "&gt;")
    .replace("\"", "&quot;")

/** The markdown subset the note store actually uses, rendered to the pane's HTML. */
internal object MarkdownLite {

    fun toHtml(src: String): String {
        if (src.isBlank()) return "<p><i>Empty note.</i></p>"
        val blocks = mutableListOf<String>()
        // Fenced code first, lifted out as placeholders so no other rule
        // can touch their contents. A ```mermaid fence becomes a labelled
        // diagram-source block: Swing has no diagram engine, so the source
        // stays visible (and editable) instead of being silently swallowed
        // — the same contract as the VS Code webview's diagram card.
        var text = src.replace(Regex("```([^\\n]*)\\n([\\s\\S]*?)```")) { m ->
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
