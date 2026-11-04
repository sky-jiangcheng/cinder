// Repotory for JetBrains — the chat console's data contract (ADR-0036).
//
// The panel is a CONVERSATION, not a result list: a turn is one question and
// its answer, the turns stay in the feed, the old ones fold away, and the
// detail of an item opens IN PLACE inside the turn that produced it — there
// is no second pane.
//
// This file is the whole shape, so a renamed field cannot drift into a
// silently ignored value: the session fills these, the Swing renderer reads
// them, and both sides import the same types.
//
// It is deliberately free of IntelliJ APIs. The conversation model has to be
// unit-testable without an IDE — the same reason the MCP protocol client and
// the payload parsers are (ADR-0035): a rule that only a running IDE can
// check is a rule nobody checks.
//
// The DATA lives in the session, the FOLD STATE lives in the UI: a collapsed
// turn must stay collapsed while other turns arrive, which is exactly the
// Cline panel's known pain point (its collapse switches reset every time the
// panel is reopened).
package dev.repotory.jetbrains

/**
 * Which knowledge face a turn talks to. Ask is the default (ADR-0036 decision 4).
 *
 * The tool and the placeholder travel with the mode: one place decides which
 * surface a question goes to, so the panel cannot show one mode's wording
 * over another mode's answer.
 */
enum class ChatMode(val label: String, val placeholder: String, val tool: String) {
    ASK("Ask", "Ask about this project…", "repotory_ask"),
    NOTES("Notes", "Search notes & todos…", "repotory_notes_search"),
    PAGES("Pages", "Search approved knowledge pages…", "repotory_wiki_search"),
}

/** How a turn ended. A failed turn is a state, never an empty answer. */
enum class TurnState { PENDING, DONE, ERROR }

/** The severity of an announcement (the bar at the top of the panel). */
enum class ChatLevel { INFO, WARN, ERROR }

/**
 * The panel's scope: which project it is locked to.
 *
 * `locked` is a DEFAULT, not a restriction — the chip opens and the panel goes
 * back to cross-project. [projectId] is 0 when the workspace resolves to no
 * project, and then there is nothing to lock to.
 */
data class ScopeState(
    /** The project the workspace resolves to; 0 when it resolves to none. */
    val projectId: Long = 0,
    /** The project's display name ("" when there is none). */
    val projectName: String = "",
    /** The repository folder inside the project, when the workspace is in one. */
    val repository: String = "",
    /** True when the panel filters to that project. */
    val locked: Boolean = false,
)

/**
 * One cited source of an `repotory_ask` answer.
 *
 * The MCP tool answers in TEXT (`[P1] page "Title" (id 42, project Foo)`), so
 * this is a parsed view of a text line, not a service type. It exists so the
 * reader can see what P1 is — a ref that means nothing is worse than no answer.
 */
data class EvidenceSource(
    /** The bracketed ref the answer cites: "P1" (a page) or "N3" (a note). */
    val ref: String,
    /** The id space the source lives in: "page" or "note". */
    val type: String,
    /** The source's title. */
    val title: String,
    /** The stable id within its own type. */
    val id: Long,
    /** The page slug (pages only). */
    val slug: String? = null,
    /** The owning project's name. */
    val project: String? = null,
)

/**
 * The parsed answer of `repotory_ask`.
 *
 * The tool answers in TEXT, so the source list has to be read back out of the
 * reply. `parsed` is the honest flag: when the source lines do not match the
 * contract, the whole text is shown VERBATIM instead of a half-list — a
 * swallowed source is worse than an unparsed one (ADR-0036).
 */
data class AskAnswer(
    /** The reply without the source list; empty when the parse failed. */
    val answer: String,
    /** The tool's raw text, always. */
    val raw: String,
    /** The parsed sources; empty when the parse failed. */
    val sources: List<EvidenceSource>,
    /** False when the source list could not be parsed — render `raw` verbatim. */
    val parsed: Boolean,
)

/**
 * The body of a card once it has been opened in place.
 *
 * Three shapes, because three kinds of thing can be behind a card: a note
 * has a row to read, an approved page has a body plus a graph, and a todo has
 * nothing but the matched excerpt.
 */
sealed interface CardBody {
    /** The whole note (repotory_notes_read). */
    data class NoteBody(val note: NoteDetail) : CardBody

    /** The whole approved page (repotory_wiki_read). */
    data class PageBody(val page: WikiPageDetail) : CardBody

    /**
     * Plain text: the matched excerpt, or the failure that kept the body from
     * arriving. [isError] marks the failure — a card must not sit on
     * "Loading…" forever, and an unreadable item has to say WHY.
     */
    data class TextBody(val text: String, val isError: Boolean = false) : CardBody
}

/** One search hit as a card in the feed, with its body once it is opened. */
data class ChatCard(val hit: RepotoryHit, val body: CardBody? = null)

/** One question and everything it produced. */
data class ChatTurn(
    /** Session-assigned id; the UI uses it to address this turn. */
    val id: String,
    val mode: ChatMode,
    val query: String,
    val state: TurnState = TurnState.PENDING,
    /** The answer (ask mode). */
    val answer: String = "",
    /**
     * True when the answer must be shown VERBATIM: the source list could not
     * be parsed, and showing the raw text is the honest fallback (ADR-0036:
     * a broken parse must never swallow the sources).
     */
    val verbatim: Boolean = false,
    /** The parsed sources (ask mode). */
    val sources: List<EvidenceSource> = emptyList(),
    /** The hit cards (notes / pages mode). */
    val cards: List<ChatCard> = emptyList(),
    /**
     * One line about what the project lock filtered away — the honest cost of a
     * presentation-layer filter (ADR-0036 decision 2).
     */
    val scopeNote: String = "",
    /** The failure text, when the turn failed. */
    val error: String = "",
)

/**
 * The external AI provider as the panel's settings sheet shows it.
 *
 * The plugin does NOT own these values — it reads and writes the server's
 * `ai_chat_*` configuration (ADR-0044 decision 5). [hasKey] is the honest
 * shape of a secret the server masks: the sheet can say "a key is set"
 * without ever seeing it.
 */
data class AISettings(
    /** The OpenAI-compatible base URL. */
    val baseUrl: String,
    /** The model id to ask. */
    val model: String,
    /** True when a key is stored on the server (its value is masked there). */
    val hasKey: Boolean,
)

/**
 * The outcome of one provider operation (ADR-0044 decision 5).
 *
 * One type for all four calls — load, save, list models, test — because they
 * all answer the same way: a verdict line, and at most some data. The sheet
 * shows [message] where the user is looking, so the panel's announcement bar
 * stays about the CONVERSATION and does not repeat the same sentence twice.
 */
data class ProviderOutcome(
    /** True when the call did what it was asked. */
    val ok: Boolean,
    /** The one line the sheet shows. */
    val message: String,
    /** The settings the server holds (load only). */
    val settings: AISettings? = null,
    /** The models the endpoint advertises (list models only). */
    val models: List<String> = emptyList(),
)

/** What the session tells the UI. */
sealed interface ChatEvent {
    /** A turn was created or changed. */
    data class Turn(val turn: ChatTurn) : ChatEvent

    /** Something the reader should know about, shown in the announcement bar. */
    data class Announce(val level: ChatLevel, val message: String) : ChatEvent

    /** The scope changed (the chip shows it). */
    data class Scope(val scope: ScopeState) : ChatEvent

    /** The mode changed (the mode buttons show it). */
    data class Mode(val mode: ChatMode) : ChatEvent

    /**
     * The projects the workspace could belong to.
     *
     * Sent when the workspace resolves to no project: the chip used to be
     * disabled there, which left the user with nothing to do (ADR-0044
     * decision 6).
     */
    data class Candidates(val projects: List<ProjectOption>) : ChatEvent
}

// --------------------------------------------------------------- ask answers

/**
 * One source line of an `repotory_ask` answer.
 *
 * The tool writes it with Go's %q for the title, so the title may carry
 * backslash escapes ("a \"quoted\" title") and the id space is "page" or
 * "note" (service.EvidenceItem.Type). The shape is:
 *
 *     [P1] page "Some title" (id 42, slug some-slug, project Repotory)
 *     [N3] note "Another title" (id 7, project Repotory)
 *
 * The slug and the project name are optional; the id is not.
 */
private val ASK_SOURCE_LINE =
    Regex("""^\[([A-Za-z]?\d+)\]\s+(\S+)\s+"((?:[^"\\]|\\.)*)"\s*\((.*)\)\s*$""")

/** The parenthesised tail: `id 42[, slug s][, project P]`. */
private val ASK_SOURCE_META = Regex("""^id (\d+)(?:, slug ([^,]*))?(?:, project (.*))?$""")

/**
 * The trailing note the tool appends when the context budget dropped
 * matches. It is an annotation, not a source — and it must not be mistaken
 * for a malformed one.
 */
private val ASK_OMITTED_LINE = Regex("""^\(\d+ further match\(es\) omitted[^)]*\)$""")

/** The marker the tool puts between the reply and the source list. */
private const val ASK_SOURCES_MARKER = "\n\nSources:\n"

/**
 * Parse the `repotory_ask` answer into the reply and its sources.
 *
 * The tool answers in TEXT (ADR-0033): a reply, then a source list. The
 * marker is searched from the END, because a reply may legitimately contain
 * the word "Sources:" — the real list is always the last one.
 *
 * A source line that does not match the contract means the contract changed
 * (or the model wrote into the list). The whole text is then returned
 * UNPARSED, so the caller shows it verbatim: half a source list is worse
 * than no parsing, because the reader cannot tell what was dropped.
 */
fun parseAskAnswer(text: String): AskAnswer {
    val raw = text ?: ""
    val at = raw.lastIndexOf(ASK_SOURCES_MARKER)
    if (at < 0) {
        // No source list at all: a keyword fallback, or a reply with no evidence.
        // That is a complete answer, not a parse failure.
        return AskAnswer(raw.trim(), raw, emptyList(), true)
    }
    val answer = raw.substring(0, at).trim()
    val sources = mutableListOf<EvidenceSource>()
    for (line in raw.substring(at + ASK_SOURCES_MARKER.length).split("\n")) {
        val trimmed = line.trim()
        if (trimmed.isEmpty()) continue
        if (ASK_OMITTED_LINE.matches(trimmed)) continue
        val match = ASK_SOURCE_LINE.matchEntire(trimmed)
        if (match == null) return AskAnswer("", raw, emptyList(), false)
        val meta = ASK_SOURCE_META.matchEntire(match.groupValues[4].trim())
        if (meta == null) return AskAnswer("", raw, emptyList(), false)
        sources.add(
            EvidenceSource(
                ref = match.groupValues[1],
                type = match.groupValues[2],
                title = unquoteGo(match.groupValues[3]),
                id = meta.groupValues[1].toLongOrNull() ?: 0L,
                slug = meta.groupValues[2].takeIf { it.isNotEmpty() },
                project = meta.groupValues[3].takeIf { it.isNotEmpty() },
            ),
        )
    }
    return AskAnswer(answer, raw, sources, true)
}

/**
 * Undo Go's %q quoting of a source title.
 *
 * The service writes titles with %q, so a title with a quote, a backslash or
 * a newline arrives escaped. Anything this does not recognise is kept
 * verbatim (backslash included) rather than dropped.
 */
fun unquoteGo(text: String): String {
    if (!text.contains("\\")) return text
    val out = StringBuilder()
    var i = 0
    while (i < text.length) {
        val ch = text[i]
        if (ch != '\\') {
            out.append(ch)
            i++
            continue
        }
        val next = if (i + 1 < text.length) text.substring(i + 1, i + 2) else null
        i++
        when (next) {
            "n" -> { out.append('\n'); i++ }
            "t" -> { out.append('\t'); i++ }
            "r" -> { out.append('\r'); i++ }
            "a" -> { out.append('\u0007'); i++ }
            "b" -> { out.append('\b'); i++ }
            "f" -> { out.append('\u000C'); i++ }
            "v" -> { out.append('\u000B'); i++ }
            "\\" -> { out.append('\\'); i++ }
            "\"" -> { out.append('"'); i++ }
            "x" -> {
                val hex = if (i + 3 <= text.length) text.substring(i + 1, i + 3) else ""
                if (hex.length == 2 && hex.all { it in "0123456789abcdefABCDEF" }) {
                    out.append(hex.toInt(16).toChar())
                    i += 3
                } else {
                    out.append("\\x")
                    i++
                }
            }
            "u" -> {
                val hex = if (i + 5 <= text.length) text.substring(i + 1, i + 5) else ""
                if (hex.length == 4 && hex.all { it in "0123456789abcdefABCDEF" }) {
                    out.append(hex.toInt(16).toChar())
                    i += 5
                } else {
                    out.append("\\u")
                    i++
                }
            }
            null -> out.append('\\')
            else -> { out.append('\\'); out.append(next); i++ }
        }
    }
    return out.toString()
}
