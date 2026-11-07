package dev.repotory.jetbrains

import kotlin.test.Test
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
            route = "/wiki/42",
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
            route = "/wiki/7",
            kind = "<i>kind</i>",
        )
        val html = renderCard(hit)

        assertFalse(html.contains("<b>title</b>"))
        assertFalse(html.contains("<script>"))
        assertFalse(html.contains("<I>KIND</I>"))
        assertTrue(html.contains("&lt;b&gt;title&lt;/b&gt;"))
        assertTrue(html.contains("&lt;I&gt;KIND&lt;/I&gt;"))
    }

    /** One card inside one turn — the smallest thing that renders a badge. */
    private fun renderCard(hit: RepotoryHit): String {
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
            expandedCards = emptySet(),
        )
    }
}
