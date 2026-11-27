package dev.repotory.jetbrains

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The chat console's HTML: every value that came from the server is escaped.
 *
 * The badge was the one that was easy to forget. For a page hit the badge is
 * the page's `kind` (concept | entity | synthesis | …), and `kind` is a field
 * a person fills in when the page is written — so a badge is as
 * author-controlled as a title and has to be escaped the same way. A page
 * kind of `<img src=x onerror=…>` used to reach the pane as markup.
 *
 * These are the assertion the fix asked for: not "this one string is
 * escaped" but "no server-supplied string reaches the HTML as markup".
 */
class ChatConsoleHtmlTest {

    @Test
    fun `a page badge is escaped — a hostile kind never reaches the pane as markup`() {
        val hit = RepotoryHit(
            type = "wiki",
            id = 42L,
            projectId = 1L,
            title = "Retry policy",
            snippet = "",
            kind = "<img src=x onerror=alert(1)>",
        )
        val html = renderCard(hit)

        // The renderer upper-cases the badge, so unescaped markup would have
        // arrived upper-cased — that is the string that must NOT be there.
        assertFalse(html.contains("<IMG SRC=X ONERROR=ALERT(1)>"))
        assertTrue(html.contains("&lt;IMG SRC=X ONERROR=ALERT(1)&gt;"))
    }

    @Test
    fun `no server-supplied string in a card reaches the HTML as markup`() {
        val hit = RepotoryHit(
            type = "wiki",
            id = 7L,
            projectId = 1L,
            title = "<b>title</b>",
            snippet = "<script>alert(2)</script>",
            kind = "<i>kind</i>",
        )
        val html = renderCard(hit)

        assertFalse(html.contains("<b>title</b>"))
        assertFalse(html.contains("<script>"))
        assertFalse(html.contains("<I>KIND</I>"))
        assertTrue(html.contains("&lt;b&gt;title&lt;/b&gt;"))
        assertTrue(html.contains("&lt;I&gt;KIND&lt;/I&gt;"))
    }

    /**
     * A card's first action continues the conversation; it does not leave.
     *
     * The card used to carry "Open in Cinder", a link out of the panel for a
     * body that was already on screen. The only useful next move is a question
     * about what was just read, so that is the action — the link carries the
     * card's address, which askAboutCard() turns into a question and sends.
     */
    @Test
    fun `an opened card offers a follow-up instead of a jump out of the panel`() {
        val hit = RepotoryHit(
            type = "note",
            id = 42L,
            projectId = 1L,
            title = "FTS5 quirk",
            snippet = "content tables answer",
        )
        val html = renderCard(hit, expanded = true)

        assertTrue(html.contains("/card/t1/note/42/ask"), "the follow-up action is missing")
        assertTrue(html.contains("Ask about this"))
        assertFalse(html.contains("Open in Cinder"))
        assertFalse(html.contains("/open"), "nothing in a card may leave the panel")
    }

    @Test
    fun `the follow-up question names the item, and a quoted title cannot break the phrase`() {
        val hit = RepotoryHit(
            type = "note",
            id = 42L,
            projectId = 1L,
            title = "the \"retry\" quirk",
            snippet = "",
        )
        assertEquals(
            "Tell me about note #42 \"the 'retry' quirk\": what it says, and why it matters.",
            followUpQuestion(hit),
        )
        assertEquals(
            "Tell me about page #7 \"Time-state graph\": what it says, and why it matters.",
            followUpQuestion(
                RepotoryHit(type = "wiki", id = 7L, projectId = 1L, title = "Time-state graph", snippet = ""),
            ),
        )
    }

    /**
     * The idle panel's question example asks the sentence it shows.
     *
     * The row is a link; the dispatcher answers it by calling askNow with
     * ChatConsoleHtml.EXAMPLE_ASK. If the label and the constant drifted apart
     * the reader would click one question and send another, so they are pinned
     * to the same string.
     */
    @Test
    fun `the idle question example shows the question it asks`() {
        val html = ChatConsoleHtml.render(
            emptyList(),
            showEarlier = true,
            collapsedTurns = emptySet(),
            expandedSources = emptySet(),
            expandedCards = emptySet(),
            expandedThoughts = emptySet(),
        )

        assertTrue(html.contains("//example/ask"), "the example row is missing")
        assertTrue(
            html.contains(ChatConsoleHtml.EXAMPLE_ASK),
            "the label must be the question the click sends",
        )
    }

    /** One card inside one turn — the smallest thing that renders a badge. */
    private fun renderCard(hit: RepotoryHit, expanded: Boolean = false): String {
        val turn = ChatTurn(
            id = "t1",
            mode = ChatMode.PAGES,
            query = "q",
            state = TurnState.DONE,
            cards = listOf(ChatCard(hit)),
        )
        return ChatConsoleHtml.render(
            listOf(turn),
            showEarlier = true,
            collapsedTurns = emptySet(),
            expandedSources = emptySet(),
            expandedCards = if (expanded) setOf("t1/${hit.type}/${hit.id}") else emptySet(),
            expandedThoughts = emptySet(),
        )
    }
}
