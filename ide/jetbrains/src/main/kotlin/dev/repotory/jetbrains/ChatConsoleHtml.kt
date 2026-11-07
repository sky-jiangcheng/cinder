// Cinder for JetBrains — the chat console's HTML.
//
// The Swing half of the conversation feed (ADR-0036 decision 8: the two IDE
// surfaces must LOOK the same; only "how Swing draws HTML" may differ).
//
// Swing's JEditorPane speaks a small dialect of HTML 3.2 with partial CSS:
// no flexbox, no CSS variables, no external stylesheets. The rules below stay
// inside it — a <style> block on the kit plus inline styles, colours pulled
// from the IDE's own UIManager keys so the feed follows the theme.
//
// Every interactive thing is an <a> with a "repotory:" href. HTMLEditorKit's
// LinkController fires a HyperlinkEvent for a click on a link in a
// read-only pane, and the event carries the raw href as its description —
// which is how the feed dispatches a click on a fold row, a card or an
// action without any Swing geometry maths.
package dev.repotory.jetbrains

import com.intellij.ui.JBColor
import java.awt.Color

/** The console's HTML — the feed, the cards, the source lists. */
internal object ChatConsoleHtml {

    /** How many recent turns stay expanded; the rest fold into one row. */
    const val VISIBLE_TURNS = 2

    /** The href scheme every action in the feed uses. */
    const val LINK_SCHEME = "repotory:"

    /** Colours pulled from the IDE theme, as #rrggbb for the HTML renderer. */
    private val meta: String get() = metaColorHex()
    private val border: String get() = colorHex(JBColor.border())

    /**
     * The whole feed: the fold row, then the visible turns.
     *
     * The old turns are folded into ONE row above the visible ones (ADR-0036
     * decision 3), and the fold state is passed in rather than derived: a
     * collapsed turn must stay collapsed while other turns arrive.
     */
    fun render(
        turns: List<ChatTurn>,
        showEarlier: Boolean,
        collapsedTurns: Set<String>,
        expandedSources: Set<String>,
        expandedCards: Set<String>,
    ): String {
        val sb = StringBuilder()
        sb.append("<html><body style=\"margin:0px; padding:8px 10px 14px 10px;\">")
        if (turns.isEmpty()) {
            sb.append(emptyStateHtml())
        } else {
            val hidden = maxOf(0, turns.size - VISIBLE_TURNS)
            if (hidden > 0) {
                sb.append(foldRowHtml(hidden, showEarlier))
            }
            val visible = if (showEarlier) turns else turns.takeLast(VISIBLE_TURNS)
            for (turn in visible) {
                sb.append(turnHtml(turn, collapsedTurns, expandedSources, expandedCards))
            }
        }
        sb.append("</body></html>")
        return sb.toString()
    }

    /** Shared text is escaped, never interpreted as HTML (including titles and source labels). */
    fun renderShared(state: SharedChatState): String = buildString {
        append("<html><body style=\"margin:8px;\">")
        append("<h3>Warden shared conversations</h3>")
        append("<p>Knowledge-base Q&amp;A through the shared memory layer — not Warden's local provider. " +
            "History is shared; local Ask/Notes/Pages history stays in this panel.</p>")
        if (state.syncError.isNotBlank()) append(sharedText("Not synced", state.syncError))
        if (state.operationError.isNotBlank()) append(sharedText("Operation failed", state.operationError))
        val thread = state.selected
        if (thread == null) {
            append("<p>Select an existing conversation, or explicitly choose New conversation.</p>")
        } else {
            append(sharedText(thread.title, thread.path.ifBlank { "All projects" }))
            if (state.messages.isEmpty() && state.pending.isEmpty()) {
                append("<p>No messages loaded yet. Send a knowledge-base question when ready.</p>")
            }
            val pending = state.pending.associateBy { it.userId }
            val ids = state.messages.map { it.id }.toSet()
            for (group in sharedMessageGroups(state.messages)) {
                for (message in group) {
                    val origin = if (message.role == "assistant" && message.source == "jetbrains") {
                        "jetbrains · knowledge-base Q&A"
                    } else message.source
                    append(sharedText("${message.role} · $origin · ${message.createdAt}", message.content))
                    if (message.role == "assistant" && group.first().role != "user") {
                        append(sharedText("Unpaired reply", message.replyTo ?: "No replyTo supplied"))
                    }
                }
                pending[group.first().id]?.let { append(sharedPendingHtml(it, ids)) }
                append("<hr>")
            }
            state.pending.filter { it.userId !in ids }.forEach {
                append(sharedText("user · jetbrains · not yet synced", it.question))
                append(sharedPendingHtml(it, ids))
            }
        }
        append("</body></html>")
    }

    private fun sharedPendingHtml(job: SharedPending, storedIds: Set<String>): String = buildString {
        if (job.answer != null && job.userId + "-reply" !in storedIds) {
            append(sharedText("Knowledge-base answer · not yet synced", job.answer))
        }
        if (job.busy) append("<p><i>Saving / asking the knowledge base…</i></p>")
        if (job.error.isNotBlank()) {
            append(sharedText("Request failed · retained in this panel", job.error))
            // userId is generated locally, not a server-supplied href fragment.
            append("<p><a href=\"${LINK_SCHEME}//shared/retry/${esc(job.userId)}\">Retry failed request / sync</a></p>")
        }
    }

    private fun sharedText(label: String, text: String): String =
        "<p><b>${esc(label)}</b></p><div>${esc(text).replace("\n", "<br>")}</div>"

    // ------------------------------------------------------------- the pieces

    /**
     * The idle panel before the first question.
     *
     * Copy only talks about ACTIONS (ADR-0044 decision 4): one line of what
     * the panel does, then three tappable examples. The old four-paragraph
     * pitch described the product instead of telling the reader what to do.
     */
    private fun emptyStateHtml(): String = """
        <p style="font-size:12pt; font-weight:bold; margin-top:8px;">Ask what your project knows.</p>
        <p>Cinder answers from the notes your coding agents left behind, with the sources listed.</p>
        <table width="100%" cellpadding="0" cellspacing="0" style="margin-top:6px;">
        <tr><td style="padding:2px 0;"><a href="${LINK_SCHEME}//example/ask">Why was the retry budget raised?</a></td></tr>
        <tr><td style="padding:2px 0;"><a href="${LINK_SCHEME}//example/notes">Search the notes &amp; todos</a></td></tr>
        <tr><td style="padding:2px 0;"><a href="${LINK_SCHEME}//example/pages">Browse the approved knowledge pages</a></td></tr>
        </table>
    """.trimIndent()

    /** The folded history: one row for every turn older than the visible ones. */
    private fun foldRowHtml(hidden: Int, showEarlier: Boolean): String {
        val label = (if (showEarlier) "▾ " else "▸ ") + hidden +
            (if (hidden == 1) " earlier turn" else " earlier turns")
        val tip = if (showEarlier) "Collapse the earlier turns" else "Show the earlier turns"
        return """
            <table width="100%" cellpadding="0" cellspacing="0" style="margin-bottom:8px;">
            <tr><td><a href="${LINK_SCHEME}//earlier/toggle" title="$tip">$label</a></td></tr>
            </table>
        """.trimIndent()
    }

    /** One turn: the question, and everything it produced. */
    private fun turnHtml(
        turn: ChatTurn,
        collapsedTurns: Set<String>,
        expandedSources: Set<String>,
        expandedCards: Set<String>,
    ): String {
        val collapsed = turn.id in collapsedTurns
        val sb = StringBuilder()
        sb.append(
            """
            <table width="100%" cellpadding="0" cellspacing="0" style="margin-bottom:14px;">
            <tr>
              <td valign="top" style="padding-right:6px;"><span style="font-size:8pt; color:$meta;">${turn.mode.label.uppercase()}</span></td>
              <td valign="top" style="font-weight:bold;">${esc(turn.query)}</td>
              <td valign="top" align="right" style="width:16px;"><a href="${LINK_SCHEME}//turn/${turn.id}/toggle">${if (collapsed) "▸" else "▾"}</a></td>
            </tr>
            </table>
            """.trimIndent(),
        )
        if (!collapsed) {
            sb.append("<div style=\"margin-left:4px;\">")
            sb.append(turnBodyHtml(turn, expandedSources, expandedCards))
            sb.append("</div>")
        }
        return sb.toString()
    }

    /** The body of one turn: the answer, or the cards. */
    private fun turnBodyHtml(
        turn: ChatTurn,
        expandedSources: Set<String>,
        expandedCards: Set<String>,
    ): String {
        if (turn.state == TurnState.PENDING) {
            return "<p><i>Working…</i></p>"
        }
        if (turn.state == TurnState.ERROR) {
            return errorBoxHtml(turn.error.ifBlank { "The request failed." })
        }
        if (turn.mode == ChatMode.ASK) {
            val answer = if (turn.verbatim) {
                // The source list could not be parsed: show the tool's text as
                // it came, so nothing is swallowed.
                "<pre>" + esc(turn.answer) + "</pre>"
            } else {
                MarkdownLite.toHtml(turn.answer)
            }
            return answer + sourcesHtml(turn, expandedSources)
        }
        val sb = StringBuilder()
        for (card in turn.cards) {
            sb.append(cardHtml(turn, card, expandedCards))
        }
        if (turn.scopeNote.isNotBlank()) {
            sb.append("<p style=\"font-size:8pt; color:$meta;\">${esc(turn.scopeNote)}</p>")
        }
        return sb.toString()
    }

    /**
     * The source list of an answer, folded by default.
     *
     * The answer is the body; the evidence is its footnote (ADR-0036
     * decision 3), so the list stays closed until the reader asks for it.
     */
    private fun sourcesHtml(turn: ChatTurn, expandedSources: Set<String>): String {
        if (turn.sources.isEmpty()) return ""
        val open = turn.id in expandedSources
        val sb = StringBuilder()
        sb.append(
            "<p style=\"margin-top:6px; font-size:9pt;\">" +
                "<a href=\"${LINK_SCHEME}//turn/${turn.id}/sources\">Sources (${turn.sources.size}) ${if (open) "▾" else "▸"}</a>" +
                "</p>",
        )
        if (!open) return sb.toString()
        sb.append("<table width=\"100%\" cellpadding=\"0\" cellspacing=\"0\" style=\"margin-left:6px;\">")
        for (source in turn.sources) {
            val metaParts = mutableListOf("id ${source.id}")
            source.slug?.let { metaParts.add("slug $it") }
            source.project?.let { metaParts.add("project $it") }
            sb.append(
                "<tr><td style=\"font-size:9pt; padding-bottom:3px;\">" +
                    "<b>[${esc(source.ref)}]</b> <b>${esc(source.type)}</b> ${esc(source.title)} " +
                    "<span style=\"color:$meta;\">(${esc(metaParts.joinToString(", "))})</span>" +
                    "</td></tr>",
            )
        }
        sb.append("</table>")
        return sb.toString()
    }

    /**
     * One card: the hit, its body once it is open, and its actions.
     *
     * EVERY value that came from the server goes through [esc] before it
     * reaches the HTML. The badge is the one that was easy to forget: for a
     * page hit it is the page's `kind` (concept | entity | synthesis | …),
     * and `kind` is a field a user fills in when the page is written — so a
     * badge is as author-controlled as a title, and it must be escaped the
     * same way.
     */
    private fun cardHtml(turn: ChatTurn, card: ChatCard, expandedCards: Set<String>): String {
        val hit = card.hit
        val key = "${turn.id}/${hit.type}/${hit.id}"
        val open = key in expandedCards
        val badge = badgeLabel(hit).uppercase()
        val badgeColor = badgeColor(hit)
        val sb = StringBuilder()
        sb.append(
            """
            <table width="100%" cellpadding="4" cellspacing="0" style="border:1px solid $border; margin-bottom:6px;">
            <tr><td>
              <table width="100%" cellpadding="0" cellspacing="0"><tr>
                <td valign="top" style="padding-right:6px;"><span style="font-size:8pt; color:$badgeColor;">${esc(badge)}</span></td>
                <td valign="top" style="font-weight:bold;"><a href="${LINK_SCHEME}//card/${turn.id}/${hit.type}/${hit.id}/toggle">${esc(hit.title.ifBlank { "(untitled)" })}</a></td>
                <td valign="top" align="right" style="font-size:8pt; color:$meta;">#${hit.id}</td>
              </tr></table>
            """.trimIndent(),
        )
        if (hit.snippet.isNotBlank()) {
            sb.append("<div style=\"font-size:9pt; color:$meta;\">${esc(snippetPlain(hit.snippet))}</div>")
        }
        if (open) {
            sb.append("<div style=\"margin-top:5px;\">")
            sb.append(cardBodyHtml(card))
            sb.append("</div>")
            sb.append(cardActionsHtml(turn, hit))
        }
        sb.append("</td></tr></table>")
        return sb.toString()
    }

    /** The body of an opened card: the note, the page, the excerpt or the failure. */
    private fun cardBodyHtml(card: ChatCard): String = when (val body = card.body) {
        null -> "<p><i>Loading…</i></p>"
        is CardBody.NoteBody -> {
            val note = body.note
            val meta = buildString {
                append("note · #").append(note.id)
                if (note.updatedAt.isNotBlank()) append(" · updated ").append(note.updatedAt.take(10))
                if (note.tags.isNotBlank()) append(" · ").append(esc(note.tags))
            }
            "<p style=\"font-weight:bold;\">${esc(note.title.ifBlank { "(untitled)" })}</p>" +
                "<p style=\"font-size:8pt; color:$meta;\">$meta</p>" +
                MarkdownLite.toHtml(note.body)
        }
        is CardBody.PageBody -> {
            val page = body.page
            val meta = buildString {
                append(esc(page.kind.ifBlank { "page" }))
                append(" · #").append(page.id)
                if (page.slug.isNotBlank()) append(" · /").append(esc(page.slug))
                append(" · ").append(if (page.projectId > 0) "project ${page.projectId}" else "global")
                if (page.updatedAt.isNotBlank()) append(" · updated ").append(page.updatedAt.take(10))
                if (page.source.isNotBlank()) append(" · ").append(esc(page.source))
            }
            "<p style=\"font-weight:bold;\">${esc(page.title.ifBlank { "(untitled)" })}</p>" +
                "<p style=\"font-size:8pt; color:$meta;\">$meta</p>" +
                MarkdownLite.toHtml(page.content)
        }
        is CardBody.TextBody -> if (body.isError) {
            errorBoxHtml(body.text.ifBlank { "This item could not be read." })
        } else {
            "<p style=\"font-size:9pt; color:$meta;\">excerpt</p>" +
                "<p>${esc(body.text)}</p>"
        }
    }

    /** The actions of an opened card — the same set the VS Code console ships. */
    private fun cardActionsHtml(turn: ChatTurn, hit: RepotoryHit): String {
        val sb = StringBuilder()
        sb.append("<p style=\"font-size:9pt; margin-top:6px;\">")
        sb.append("<a href=\"${LINK_SCHEME}//card/${turn.id}/${hit.type}/${hit.id}/open\">Open in Cinder</a>")
        if (hit.type == "note") {
            sb.append(" · <a href=\"${LINK_SCHEME}//card/${turn.id}/${hit.type}/${hit.id}/edit\">Edit</a>")
            sb.append(" · <a href=\"${LINK_SCHEME}//card/${turn.id}/${hit.type}/${hit.id}/create\">New note here</a>")
            sb.append(" · <a href=\"${LINK_SCHEME}//card/${turn.id}/${hit.type}/${hit.id}/organize\">AI Tidy</a>")
        }
        sb.append("</p>")
        return sb.toString()
    }

    /** A failure, boxed so it cannot be mistaken for an answer. */
    private fun errorBoxHtml(message: String): String =
        "<table width=\"100%\" cellpadding=\"5\" cellspacing=\"0\" style=\"border:1px solid ${errorColor()};\">" +
            "<tr><td style=\"font-size:9pt;\">${esc(message)}</td></tr></table>"

    /** The colour of a kind badge, paired light/dark so it follows the theme. */
    private fun badgeColor(hit: RepotoryHit): String = when (hit.type) {
        "todo" -> colorHex(JBColor(Color(0x8B, 0x6F, 0x3A), Color(0xD9, 0xB7, 0x73)))
        "wiki" -> colorHex(JBColor(Color(0x6F, 0x42, 0xC1), Color(0xD2, 0xA8, 0xFF)))
        else -> colorHex(JBColor(Color(0x1D, 0x4E, 0xD8), Color(0x60, 0xA5, 0xFA)))
    }

    /** The failure colour, paired light/dark. */
    private fun errorColor(): String =
        colorHex(JBColor(Color(0xC0, 0x3A, 0x2B), Color(0xE8, 0x6C, 0x5A)))

    /** A colour as #rrggbb, for the HTML renderer. */
    private fun colorHex(color: Color): String =
        String.format("#%02x%02x%02x", color.red, color.green, color.blue)
}
