package dev.repotory.jetbrains

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The answer, not the model's notes — and the notes still readable.
 *
 * A gateway that does not separate reasoning_content leaves the reasoning
 * inline in content, wrapped in <think>…</think>. The pane folds it behind one
 * "Thinking" row (the shape CodeBuddy shows): the answer stays the answer, and
 * the notes are one click away instead of being deleted or laid out as prose.
 *
 * An unterminated block is the streaming case: the open tag arrives long
 * before the close, and the notes must not flash in as the reply.
 */
class MarkdownThinkingTest {

    private fun turn(id: String) = ChatTurn(
        id = id,
        mode = ChatMode.ASK,
        query = "why was the retry budget raised?",
        state = TurnState.DONE,
        answer = "<think>plan: read the note first</think>The retry budget was raised.",
    )

    @Test
    fun `the notes are split from the answer, not lost`() {
        val split = splitThinking("<think>plan: read the note first</think>The retry budget was raised.")
        assertEquals(listOf("plan: read the note first"), split.thoughts)
        assertEquals("The retry budget was raised.", split.body)
    }

    @Test
    fun `an unterminated block is the model still thinking`() {
        val split = splitThinking("Answer so far.\n<think>still thinking")
        assertEquals(listOf("still thinking"), split.thoughts)
        assertEquals("Answer so far.", split.body)
    }

    @Test
    fun `both spellings and any case are recognised`() {
        assertEquals(listOf("x"), splitThinking("<thinking>x</thinking>A.").thoughts)
        assertEquals(listOf("x"), splitThinking("<THINK>x</THINK>A.").thoughts)
        assertEquals(listOf("x"), splitThinking("<reasoning>x</reasoning>A.").thoughts)
    }

    @Test
    fun `the notes are folded behind one row`() {
        val html = ChatConsoleHtml.render(
            listOf(turn("t1")),
            showEarlier = false,
            collapsedTurns = emptySet(),
            expandedSources = emptySet(),
            expandedCards = emptySet(),
            expandedThoughts = emptySet(),
        )
        assertTrue(html.contains("Thinking ▸"))
        assertFalse(html.contains("plan: read the note first"))
        assertTrue(html.contains("The retry budget was raised."))
    }

    @Test
    fun `an open turn shows the notes above the answer`() {
        val html = ChatConsoleHtml.render(
            listOf(turn("t1")),
            showEarlier = false,
            collapsedTurns = emptySet(),
            expandedSources = emptySet(),
            expandedCards = emptySet(),
            expandedThoughts = setOf("t1"),
        )
        assertTrue(html.contains("Thinking ▾"))
        assertTrue(html.contains("plan: read the note first"))
        assertTrue(
            html.indexOf("plan: read the note first") < html.indexOf("The retry budget was raised."),
            "the notes belong above the answer they explain",
        )
    }

    @Test
    fun `the answer never carries the notes when rendered as markdown`() {
        val html = MarkdownLite.toHtml("<think>plan: read the note first</think>The retry budget was raised.")
        assertFalse(html.contains("plan: read the note first"))
        assertTrue(html.contains("The retry budget was raised."))
    }

    @Test
    fun `a stray tag is removed, the text around it is kept`() {
        val html = MarkdownLite.toHtml("Answer.</think>")
        assertTrue(html.contains("Answer."))
        assertFalse(html.contains("think"))
    }
}
