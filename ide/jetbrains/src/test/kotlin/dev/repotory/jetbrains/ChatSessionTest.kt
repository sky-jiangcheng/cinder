package dev.repotory.jetbrains

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * The conversation model (ADR-0036).
 *
 * The model owns three decisions that the UI cannot make for itself:
 *   - which tool a mode talks to;
 *   - what the project lock does in that mode (a server-side filter, or a
 *     presentation filter the panel has to admit to);
 *   - what a failure looks like (a failed turn, not an empty answer).
 *
 * The transport is faked here: no MCP process, no service, no editor.
 */
class ChatSessionTest {

    /** A transport that answers with canned tool texts and records the calls. */
    private class FakeTransport(
        private val tools: Map<String, String>,
        private val project: Pair<Long, String>? = null,
        private val rpcFails: Boolean = false,
    ) : ChatTransport {
        val calls = mutableListOf<Pair<String, Map<String, Any?>>>()

        override fun callTool(name: String, args: Map<String, Any?>): String {
            calls.add(name to args)
            return tools[name] ?: throw IllegalStateException("no such tool: $name")
        }

        override fun rpc(method: String, args: List<Any?>): Map<String, Any?>? {
            if (rpcFails) throw IllegalStateException("connection refused")
            if (method == "ResolveWorkspacePath" && project != null) {
                return mapOf("Project" to mapOf("id" to project.first, "name" to project.second))
            }
            return null
        }

        override fun serverUrl(): String = "http://127.0.0.1:18765"

        override fun workspacePath(): String? = "/tmp/work"
    }

    private fun collector(): Pair<MutableList<ChatEvent>, (ChatEvent) -> Unit> {
        val events = mutableListOf<ChatEvent>()
        return events to { event -> events.add(event) }
    }

    private fun lastTurn(events: List<ChatEvent>): ChatTurn {
        val turns = events.filterIsInstance<ChatEvent.Turn>()
        return turns.last().turn
    }

    private fun announcements(events: List<ChatEvent>): List<ChatEvent.Announce> =
        events.filterIsInstance<ChatEvent.Announce>()

    // ------------------------------------------------------------------- ask

    @Test
    fun `ask mode calls repotory_ask and carries the parsed sources`() {
        val transport = FakeTransport(
            mapOf("repotory_ask" to "Because the gateway retries.\n\nSources:\n[P1] note \"retry\" (id 3, project Foo)\n"),
        )
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("why 503?", ChatMode.ASK)
        val turn = lastTurn(events)
        assertEquals(TurnState.DONE, turn.state)
        assertEquals("Because the gateway retries.", turn.answer)
        assertEquals(1, turn.sources.size)
        assertEquals("repotory_ask", transport.calls[0].first)
    }

    @Test
    fun `an unparseable source list shows the answer verbatim instead of losing it`() {
        val raw = "Answer.\n\nSources:\n[P1] note \"broken (id 3)\n"
        val transport = FakeTransport(mapOf("repotory_ask" to raw))
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.ASK)
        val turn = lastTurn(events)
        assertEquals(TurnState.DONE, turn.state)
        assertTrue(turn.verbatim)
        assertEquals(raw, turn.answer)
        assertEquals(0, turn.sources.size)
    }

    @Test
    fun `the tool's text error becomes a failed turn, not an answer`() {
        val transport = FakeTransport(mapOf("repotory_ask" to "error: the AI endpoint refused the request"))
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.ASK)
        val turn = lastTurn(events)
        assertEquals(TurnState.ERROR, turn.state)
        assertEquals("the AI endpoint refused the request", turn.error)
    }

    @Test
    fun `a thrown tool error becomes a failed turn`() {
        val transport = FakeTransport(emptyMap())
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.ASK)
        val turn = lastTurn(events)
        assertEquals(TurnState.ERROR, turn.state)
        assertNotNull(turn.error)
    }

    @Test
    fun `an empty answer says so instead of showing nothing under the question`() {
        val transport = FakeTransport(mapOf("repotory_ask" to ""))
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.ASK)
        val turn = lastTurn(events)
        assertEquals(TurnState.DONE, turn.state)
        assertEquals("The service returned an empty answer.", turn.answer)
    }

    @Test
    fun `the keyword fallback is announced as a warning, not read as an answer`() {
        val transport = FakeTransport(
            mapOf("repotory_ask" to "No AI endpoint is configured, so this is a keyword match list, NOT an answer:"),
        )
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.ASK)
        val warned = announcements(events).last()
        assertEquals(ChatLevel.WARN, warned.level)
        assertTrue(warned.message.contains("No AI endpoint is configured"))
    }

    // ----------------------------------------------------------------- notes

    @Test
    fun `notes mode filters by the locked project and says how many were dropped`() {
        val hits = """
            [{"type":"note","id":1,"project_id":7,"title":"mine","snippet":"s"},
             {"type":"note","id":2,"project_id":9,"title":"other","snippet":"s"},
             {"type":"note","id":3,"project_id":7,"title":"mine too","snippet":"s"}]
        """.trimIndent()
        val transport = FakeTransport(mapOf("repotory_notes_search" to hits), 7L to "Repotory")
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.refreshScope()
        session.run("q", ChatMode.NOTES)
        val turn = lastTurn(events)
        assertEquals(TurnState.DONE, turn.state)
        assertEquals(2, turn.cards.size)
        assertNotNull(turn.scopeNote)
        assertTrue(turn.scopeNote!!.contains("2 of 3"))
        assertTrue(turn.scopeNote!!.contains("Repotory"))
        assertTrue(
            turn.scopeNote!!.contains("The notes tool has no project filter, so the panel filtered the list."),
        )
    }

    @Test
    fun `notes mode without a lock keeps every hit and says nothing about scope`() {
        val hits = """[{"type":"note","id":1,"project_id":9,"title":"other","snippet":"s"}]"""
        val transport = FakeTransport(mapOf("repotory_notes_search" to hits))
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.NOTES)
        val turn = lastTurn(events)
        assertEquals(1, turn.cards.size)
        assertEquals("", turn.scopeNote)
    }

    @Test
    fun `notes mode sends the workspace path so the tool can re-rank`() {
        val transport = FakeTransport(mapOf("repotory_notes_search" to "[]"))
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.NOTES)
        val call = transport.calls.first { it.first == "repotory_notes_search" }
        assertEquals("/tmp/work", call.second["path"])
    }

    // ----------------------------------------------------------------- pages

    @Test
    fun `pages mode passes the project id to the tool - the filter is server-side`() {
        val transport = FakeTransport(
            mapOf("repotory_wiki_search" to """[{"id":1,"slug":"s","title":"P","kind":"concept","project_id":7}]"""),
            7L to "Repotory",
        )
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.refreshScope()
        session.run("q", ChatMode.PAGES)
        val call = transport.calls.first { it.first == "repotory_wiki_search" }
        assertEquals(7L, call.second["project_id"])
        val turn = lastTurn(events)
        assertEquals(1, turn.cards.size)
        assertTrue(turn.scopeNote.contains("Scoped to Repotory"))
        assertTrue(turn.scopeNote.contains("that project's pages and the global ones."))
    }

    // ------------------------------------------------------------- the scope

    @Test
    fun `ask mode passes the project id to the tool too`() {
        val transport = FakeTransport(mapOf("repotory_ask" to "no evidence"), 7L to "Repotory")
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.refreshScope()
        session.run("q", ChatMode.ASK)
        val call = transport.calls.first { it.first == "repotory_ask" }
        assertEquals(7L, call.second["project_id"])
    }

    @Test
    fun `unlocking drops the project id from the calls`() {
        val transport = FakeTransport(mapOf("repotory_ask" to "no evidence"), 7L to "Repotory")
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.refreshScope()
        session.toggleScope()
        assertFalse(session.currentScope().locked)
        session.run("q", ChatMode.ASK)
        val call = transport.calls.first { it.first == "repotory_ask" }
        assertNull(call.second["project_id"])
    }

    @Test
    fun `a workspace that resolves to no project leaves the panel unlocked`() {
        val transport = FakeTransport(mapOf("repotory_ask" to "no evidence"))
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.refreshScope()
        val scope = session.currentScope()
        assertEquals(0L, scope.projectId)
        assertFalse(scope.locked)
    }

    @Test
    fun `an unreachable service says so instead of pretending the panel is scoped`() {
        val transport = FakeTransport(mapOf("repotory_ask" to "no evidence"), rpcFails = true)
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.refreshScope()
        assertFalse(session.currentScope().locked)
        val warned = announcements(events).last()
        assertEquals(ChatLevel.WARN, warned.level)
        assertEquals(
            "The local Repotory service is not reachable at http://127.0.0.1:18765 — " +
                "the project lock needs it, so the panel searches across all projects.",
            warned.message,
        )
    }

    // ------------------------------------------------------------- the turns

    @Test
    fun `turns accumulate - the panel is a history, not a result slot`() {
        val transport = FakeTransport(mapOf("repotory_ask" to "one"))
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("first", ChatMode.ASK)
        session.run("second", ChatMode.ASK)
        val turns = events.filterIsInstance<ChatEvent.Turn>()
        assertTrue(turns.size >= 4)
        val ids = turns.map { it.turn.id }.toSet()
        assertEquals(2, ids.size)
        assertEquals(2, session.turns().size)
    }

    @Test
    fun `a card opens in place and carries the body it read`() {
        val hits = """[{"type":"note","id":42,"project_id":7,"title":"FTS5 quirk","snippet":"s"}]"""
        val transport = FakeTransport(
            mapOf(
                "repotory_notes_search" to hits,
                "repotory_notes_read" to """{"id":42,"project_id":7,"title":"FTS5 quirk","content":"the body"}""",
            ),
        )
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.NOTES)
        session.expandCard("turn-1", "note", 42L)
        val card = lastTurn(events).cards.first()
        val body = card.body
        assertNotNull(body)
        assertTrue(body is CardBody.NoteBody)
        assertEquals("the body", (body as CardBody.NoteBody).note.body)
    }

    @Test
    fun `a card is addressed by its type too - notes and todos share numbers`() {
        // Notes and todos have INDEPENDENT id sequences, so the same number can
        // name two different cards in one turn. Opening one must not open the
        // other.
        val hits = """
            [{"type":"note","id":42,"project_id":7,"title":"a note","snippet":"s"},
             {"type":"todo","id":42,"project_id":7,"title":"a todo","snippet":"t"}]
        """.trimIndent()
        val transport = FakeTransport(
            mapOf(
                "repotory_notes_search" to hits,
                "repotory_notes_read" to """{"id":42,"project_id":7,"title":"a note","content":"the body"}""",
            ),
        )
        val (events, post) = collector()
        val session = ChatSession(transport, post)
        session.run("q", ChatMode.NOTES)
        session.expandCard("turn-1", "todo", 42L)
        val cards = lastTurn(events).cards
        assertEquals(2, cards.size)
        // The TODO was opened — the note beside it, with the same number, was not.
        assertTrue(cards[1].body is CardBody.TextBody)
        assertNull(cards[0].body)
    }
}
