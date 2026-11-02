// Repotory for JetBrains — the conversation model behind the chat console.
//
// ADR-0036: the panel is a CONVERSATION. A question goes in, a turn comes
// out, and the turns stay in the feed. This file owns that model — the data,
// the tool calls and the scope. The Swing side owns the FOLD STATE (which
// turns are collapsed), so a collapsed turn survives the next turn arriving.
//
// The thin-client discipline is unchanged and load-bearing here: the plugin
// decides WHICH tool to call and WHAT to show, never how to retrieve or rank.
// Every answer comes from the shared Go service layer:
//
//   ask   -> repotory_ask          (answer + sources, ADR-0033)
//   notes -> repotory_notes_search (hits; NO project filter — see below)
//   pages -> repotory_wiki_search  (approved pages, ADR-0032)
//
// The project lock (ADR-0036 decision 2) is a DEFAULT, not a restriction:
//
//   ask   -> the project id goes to the tool: the server really filters.
//   pages -> the project id goes to the tool: the server really filters.
//   notes -> the tool has NO project parameter, so the lock is a PRESENTATION
//           filter over the returned hits. The cost is real (a project can
//           come back with fewer than N hits) and the panel says so instead
//           of pretending the count is everything.
//
// The file is free of IntelliJ APIs on purpose: the transport is a port, so
// the whole model is testable without an IDE, a service or an MCP process.
package dev.repotory.jetbrains

/**
 * Everything the session needs from the outside world.
 *
 * A port, not a dependency on the IDE: the session is testable without an
 * editor, and the MCP/HTTP split stays in one place (the plugin side).
 */
interface ChatTransport {
    /** Call an MCP tool and return its text content. */
    fun callTool(name: String, args: Map<String, Any?>): String

    /** Call a headless-service RPC method (the bridge MCP does not carry). */
    fun rpc(method: String, args: List<Any?>): Map<String, Any?>?

    /** The headless service's base URL. */
    fun serverUrl(): String

    /** The folder the session is standing in, if any. */
    fun workspacePath(): String?
}

/**
 * The conversation model behind the IDE chat console.
 *
 * Every method that talks to the outside world BLOCKS, so the caller must run
 * it off the EDT. The [post] callback carries the outcome back to the UI.
 */
class ChatSession(
    private val transport: ChatTransport,
    private val post: (ChatEvent) -> Unit,
) {
    private val turns = mutableListOf<ChatTurn>()
    private var counter = 0
    private var mode: ChatMode = ChatMode.ASK
    private var scope: ScopeState = ScopeState()

    // ------------------------------------------------------------- the state

    /** Every turn so far, oldest first. The feed renders the tail of it. */
    fun turns(): List<ChatTurn> = turns.toList()

    /** The current scope, for the chip and the status line. */
    fun currentScope(): ScopeState = scope

    /** The current mode, for the mode buttons and the input placeholder. */
    fun currentMode(): ChatMode = mode

    /** Switch the mode the composer sends to. */
    fun setMode(next: ChatMode) {
        mode = next
        post(ChatEvent.Mode(next))
    }

    /** The panel came on screen: hand it the state it needs to render. */
    fun init() {
        post(ChatEvent.Mode(mode))
        post(ChatEvent.Scope(scope))
        turns.forEach { post(ChatEvent.Turn(it)) }
        refreshScope()
    }

    // ------------------------------------------------------------- the scope

    /**
     * Resolve the workspace into a project and lock the panel to it.
     *
     * The matching rules live server-side on purpose: the MCP tools resolve
     * the same path, and a second implementation would let the panel and an
     * agent's repotory_context disagree about which knowledge a session gets.
     *
     * Without the headless service there is no project id to lock to — the
     * panel then says so and searches across projects, rather than pretending
     * it is scoped.
     */
    fun refreshScope() {
        val dir = transport.workspacePath()
        if (dir.isNullOrBlank()) {
            setScope(ScopeState())
            return
        }
        try {
            val res = transport.rpc("ResolveWorkspacePath", listOf(dir))
            val project = res?.get("Project") as? Map<*, *>
            val id = (project?.get("id") as? Number)?.toLong() ?: 0L
            if (project != null && id > 0) {
                val name = project["name"] as? String ?: ""
                val repository = ((res["Repository"] as? Map<*, *>)?.get("path") as? String)
                    ?.let { path -> path.split('/', '\\').lastOrNull() ?: "" }
                    ?: ""
                setScope(ScopeState(id, name, repository, locked = true))
                return
            }
            setScope(ScopeState())
            announce(
                ChatLevel.WARN,
                "This folder is not in the knowledge base yet — the panel searches across all projects.",
            )
        } catch (e: Exception) {
            setScope(ScopeState())
            announce(
                ChatLevel.WARN,
                "The local Repotory service is not reachable at ${transport.serverUrl()} — " +
                    "the project lock needs it, so the panel searches across all projects.",
            )
        }
    }

    /**
     * Unlock (or re-lock) the project.
     *
     * The lock is a default, not a restriction: opening the chip puts the panel
     * back to cross-project, and the chip says which of the two it is.
     */
    fun toggleScope() {
        if (scope.projectId <= 0) {
            announce(
                ChatLevel.WARN,
                "This workspace does not resolve to a Repotory project yet, so there is nothing to lock to.",
            )
            return
        }
        val locked = !scope.locked
        setScope(scope.copy(locked = locked))
        announce(
            ChatLevel.INFO,
            if (locked) {
                "Locked to ${scope.projectName}."
            } else {
                "Searching across all projects — ${scope.projectName} is not filtered in."
            },
        )
    }

    // -------------------------------------------------------------- the turns

    /**
     * Ask one question and append the turn to the feed.
     *
     * The turn is appended, never replaces: the panel is a history, not a
     * result slot (ADR-0036 decision 1).
     */
    fun run(query: String, mode: ChatMode = this.mode) {
        val text = query.trim()
        if (text.isEmpty()) return
        val turn = ChatTurn(id = "turn-${++counter}", mode = mode, query = text, state = TurnState.PENDING)
        turns.add(turn)
        post(ChatEvent.Turn(turn))
        try {
            when (mode) {
                ChatMode.ASK -> runAsk(turn)
                ChatMode.NOTES -> runNotes(turn)
                ChatMode.PAGES -> runPages(turn)
            }
        } catch (e: Exception) {
            val message = e.message?.takeIf { it.isNotBlank() } ?: e.javaClass.simpleName
            update(turn.id) { it.copy(state = TurnState.ERROR, error = message) }
            announce(ChatLevel.ERROR, message)
        }
    }

    /**
     * Ask mode: `repotory_ask` answers with a reply and a source list (ADR-0033).
     *
     * Two failure shapes have to be told apart. The tool reports a configured
     * endpoint that failed as a TEXT error, not as a protocol error — it comes
     * back as a normal result whose body starts with "error: ". Treating that as
     * an answer would put a failure in the feed in the shape of knowledge.
     */
    private fun runAsk(turn: ChatTurn) {
        val args = mutableMapOf<String, Any?>("query" to turn.query)
        if (scope.locked && scope.projectId > 0) args["project_id"] = scope.projectId
        val text = transport.callTool(ChatMode.ASK.tool, args)

        if (text.startsWith(ASK_ERROR_PREFIX)) {
            val detail = text.removePrefix(ASK_ERROR_PREFIX).trim()
            throw IllegalStateException(detail.ifEmpty { "The AI endpoint failed." })
        }
        if (text.startsWith(ASK_NO_ENDPOINT_PREFIX)) {
            announce(
                ChatLevel.WARN,
                "No AI endpoint is configured — Repotory answered with keyword matches, not an answer.",
            )
        }

        val parsed = parseAskAnswer(text)
        // A source list that does not parse is shown VERBATIM: the raw text still
        // carries the sources, and a half-parsed list would hide which ones.
        val answer = when {
            !parsed.parsed -> parsed.raw
            // An empty answer is a fact, not a blank panel: say so rather than
            // showing nothing under the question.
            parsed.answer.isBlank() -> EMPTY_ANSWER
            else -> parsed.answer
        }
        update(turn.id) {
            it.copy(
                state = TurnState.DONE,
                answer = answer,
                verbatim = !parsed.parsed,
                sources = if (parsed.parsed) parsed.sources else emptyList(),
            )
        }
    }

    /**
     * Notes mode: `repotory_notes_search`.
     *
     * The tool has NO project parameter — `path` only re-ranks. So the project
     * lock is applied HERE, over the returned hits. The panel says how many of
     * them belong to the locked project, because "3 hits" would otherwise read
     * as "3 hits in this project" and be wrong.
     */
    private fun runNotes(turn: ChatTurn) {
        val args = mutableMapOf<String, Any?>("query" to turn.query)
        transport.workspacePath()?.takeIf { it.isNotBlank() }?.let { args["path"] = it }
        val text = transport.callTool(ChatMode.NOTES.tool, args)
        val cards = McpPayloads.parseHits(ChatMode.NOTES.tool, text).map { ChatCard(it) }
        var scopeNote = ""
        val kept = if (scope.locked && scope.projectId > 0) {
            val filtered = cards.filter { it.hit.projectId == scope.projectId }
            if (filtered.size != cards.size) {
                scopeNote = "Locked to ${scope.projectName}: ${filtered.size} of ${cards.size} matches shown. " +
                    "The notes tool has no project filter, so the panel filtered the list."
            }
            filtered
        } else {
            cards
        }
        update(turn.id) { it.copy(state = TurnState.DONE, cards = kept, scopeNote = scopeNote) }
    }

    /**
     * Pages mode: `repotory_wiki_search`.
     *
     * Here the project scope IS a server-side filter: a project scope means
     * "that project's pages PLUS the global ones" (ADR-0031), which is why the
     * scope note mentions the global pages.
     */
    private fun runPages(turn: ChatTurn) {
        val args = mutableMapOf<String, Any?>("query" to turn.query)
        val locked = scope.locked && scope.projectId > 0
        if (locked) args["project_id"] = scope.projectId
        val text = transport.callTool(ChatMode.PAGES.tool, args)
        val cards = McpPayloads.parseWikiHits(ChatMode.PAGES.tool, text).map { ChatCard(it) }
        val scopeNote = if (locked) {
            "Scoped to ${scope.projectName} — that project's pages and the global ones."
        } else {
            ""
        }
        update(turn.id) { it.copy(state = TurnState.DONE, cards = cards, scopeNote = scopeNote) }
    }

    // -------------------------------------------------------------- the cards

    /**
     * Add one card to a turn.
     *
     * A note created from a card joins the SAME turn as a card of its own, so
     * the reader sees what was just written next to what it was written from.
     */
    fun addCard(turnId: String, hit: RepotoryHit) {
        update(turnId) { it.copy(cards = it.cards + ChatCard(hit)) }
    }

    /**
     * Read the full body of a card, in place, inside its turn.
     *
     * [force] re-reads a body that is already there — the editor needs it
     * after a save, so the card shows what was actually written.
     *
     * The card is addressed by its id AND its type: notes and todos have
     * INDEPENDENT id sequences, so the same number can name two different
     * cards in one turn, and "the card with id 42" is not a card.
     */
    fun expandCard(turnId: String, cardType: String, cardId: Long, force: Boolean = false) {
        val turn = turns.firstOrNull { it.id == turnId } ?: return
        val index = turn.cards.indexOfFirst { it.hit.type == cardType && it.hit.id == cardId }
        if (index < 0) return
        val card = turn.cards[index]
        if (card.body != null && !force) return
        val body = try {
            readCardBody(card.hit)
        } catch (e: Exception) {
            // The card must not sit on "Loading…" forever: it carries the failure
            // instead, so the reader sees WHY the body is missing.
            val message = e.message?.takeIf { it.isNotBlank() } ?: "This item could not be read."
            announce(ChatLevel.ERROR, message)
            CardBody.TextBody(message, isError = true)
        }
        update(turnId) { t ->
            val cards = t.cards.toMutableList()
            cards[index] = card.copy(body = body)
            t.copy(cards = cards)
        }
    }

    /**
     * The full body of one hit.
     *
     * Notes have a row to read (repotory_notes_read → the whole note); todos
     * do not — the matched excerpt is the most the search contract returns, so
     * the card says "excerpt" rather than pretending it is the full item. An
     * approved knowledge page is read with the page tool (ADR-0032): the two id
     * spaces are separate, so the wrong tool would answer about the wrong thing.
     */
    private fun readCardBody(hit: RepotoryHit): CardBody = when (hit.type) {
        "wiki" -> try {
            val text = transport.callTool(TOOL_WIKI_READ, mapOf("id" to hit.id))
            CardBody.PageBody(McpPayloads.parseWikiPage(TOOL_WIKI_READ, text))
        } catch (e: Exception) {
            CardBody.TextBody(hit.snippet)
        }
        "note" -> try {
            val text = transport.callTool(TOOL_NOTES_READ, mapOf("id" to hit.id))
            CardBody.NoteBody(McpPayloads.parseNote(TOOL_NOTES_READ, text))
        } catch (e: Exception) {
            CardBody.TextBody(hit.snippet)
        }
        else -> CardBody.TextBody(hit.snippet)
    }

    // ------------------------------------------------------------- the plumbing

    private fun setScope(next: ScopeState) {
        scope = next
        post(ChatEvent.Scope(next))
    }

    private fun update(turnId: String, transform: (ChatTurn) -> ChatTurn) {
        val index = turns.indexOfFirst { it.id == turnId }
        if (index < 0) return
        val updated = transform(turns[index])
        turns[index] = updated
        post(ChatEvent.Turn(updated))
    }

    private fun announce(level: ChatLevel, message: String) {
        post(ChatEvent.Announce(level, message))
    }
}

/** The prefix the ask tool uses when the configured AI endpoint failed. */
private const val ASK_ERROR_PREFIX = "error: "

/** The prefix the ask tool uses when no AI endpoint is configured at all. */
private const val ASK_NO_ENDPOINT_PREFIX = "No AI endpoint is configured"

/** What the panel says when the service answered with nothing at all. */
internal const val EMPTY_ANSWER = "The service returned an empty answer."

private const val TOOL_NOTES_READ = "repotory_notes_read"
private const val TOOL_WIKI_READ = "repotory_wiki_read"
