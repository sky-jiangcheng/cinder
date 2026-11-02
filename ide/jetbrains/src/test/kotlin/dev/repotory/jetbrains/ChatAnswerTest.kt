package dev.repotory.jetbrains

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * The ask-answer parser (ADR-0033 / ADR-0036).
 *
 * This is the risky one: it reads the SOURCE LIST back out of a TEXT answer,
 * and its failure mode must be "show the raw text", never "show half a source
 * list". The vectors are the ones the VS Code console is pinned to, so the two
 * IDE surfaces cannot drift apart on what a source line is.
 */
class ChatAnswerTest {

    @Test
    fun `an answer with sources splits into the reply and the source list`() {
        val text = listOf(
            "The 503 retry lives in the gateway, not in the client.",
            "",
            "Sources:",
            "[P1] page \"Retry policy\" (id 42, slug retry-policy, project Repotory)",
            "[N3] note \"503 notes\" (id 7, project Repotory)",
            "",
        ).joinToString("\n")
        val parsed = parseAskAnswer(text)
        assertTrue(parsed.parsed)
        assertEquals("The 503 retry lives in the gateway, not in the client.", parsed.answer)
        assertEquals(2, parsed.sources.size)
        assertEquals(
            EvidenceSource("P1", "page", "Retry policy", 42L, "retry-policy", "Repotory"),
            parsed.sources[0],
        )
        assertEquals(
            EvidenceSource("N3", "note", "503 notes", 7L, null, "Repotory"),
            parsed.sources[1],
        )
    }

    @Test
    fun `a source without a slug or a project still parses`() {
        val parsed = parseAskAnswer("Answer.\n\nSources:\n[P1] note \"Plain\" (id 7)\n")
        assertTrue(parsed.parsed)
        assertEquals(1, parsed.sources.size)
        assertNull(parsed.sources[0].slug)
        assertNull(parsed.sources[0].project)
        assertEquals(7L, parsed.sources[0].id)
    }

    @Test
    fun `the budget annotation is not a source and does not break the parse`() {
        val text = listOf(
            "Answer.",
            "",
            "Sources:",
            "[P1] note \"a\" (id 1)",
            "(3 further match(es) omitted by the context budget)",
            "",
        ).joinToString("\n")
        val parsed = parseAskAnswer(text)
        assertTrue(parsed.parsed)
        assertEquals(1, parsed.sources.size)
    }

    @Test
    fun `an answer without a source list is a complete answer, not a failure`() {
        val parsed = parseAskAnswer("No results found for: retry")
        assertTrue(parsed.parsed)
        assertEquals("No results found for: retry", parsed.answer)
        assertEquals(0, parsed.sources.size)
    }

    @Test
    fun `a malformed source line falls back to the raw text`() {
        // The contract changed (or the model wrote into the list): showing half
        // a source list would hide WHICH sources were dropped.
        val text = "Answer.\n\nSources:\n[P1] note \"unterminated (id 1)\n"
        val parsed = parseAskAnswer(text)
        assertFalse(parsed.parsed)
        assertEquals("", parsed.answer)
        assertEquals(0, parsed.sources.size)
        assertEquals(text, parsed.raw)
    }

    @Test
    fun `the LAST Sources marker wins when the reply mentions the word`() {
        val text = "See Sources:\n\nSources:\n[P1] note \"x\" (id 1)\n"
        val parsed = parseAskAnswer(text)
        assertTrue(parsed.parsed)
        assertEquals("See Sources:", parsed.answer)
        assertEquals(1, parsed.sources.size)
    }

    @Test
    fun `a quoted title is unquoted the way Go wrote it`() {
        val text = "Answer.\n\nSources:\n[P1] note \"say \\\"hi\\\"\" (id 5, project X)\n"
        val parsed = parseAskAnswer(text)
        assertTrue(parsed.parsed)
        assertEquals("say \"hi\"", parsed.sources[0].title)
    }

    @Test
    fun `an empty answer is not a crash`() {
        val parsed = parseAskAnswer("")
        assertTrue(parsed.parsed)
        assertEquals("", parsed.answer)
        assertEquals(0, parsed.sources.size)
    }

    @Test
    fun `unquoteGo keeps unknown escapes verbatim instead of dropping them`() {
        assertEquals("plain", unquoteGo("plain"))
        assertEquals("a\nb", unquoteGo("a\\nb"))
        assertEquals("a\tb", unquoteGo("a\\tb"))
        // 'say \"hi\"' is how Go writes a title that contains quotes.
        assertEquals("say \"hi\"", unquoteGo("say \\\"hi\\\""))
        assertEquals("back\\slash", unquoteGo("back\\\\slash"))
        assertEquals("weird \\q", unquoteGo("weird \\q"))
    }

    @Test
    fun `a hex escape is restored and a broken one is kept verbatim`() {
        assertEquals("A", unquoteGo("\\x41"))
        assertEquals("\\xzz", unquoteGo("\\xzz"))
        assertEquals("\\u00zz", unquoteGo("\\u00zz"))
    }
}
