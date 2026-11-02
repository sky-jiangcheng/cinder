// Repotory for JetBrains — the plugin's data channel, over MCP.
//
// ADR-0035 (Accepted 2026-10-30) decision 2: the plugin's EIGHT data
// capabilities go over MCP, and only two things stay on HTTP — MoveNote
// (re-filing knowledge is a human decision an agent must not make) and the
// web-UI probe (the deep link needs a server that serves the UI).
//
// Why this is a repair and not a rewrite: the HTTP bridge and the MCP tools
// are two doors into the SAME service layer, so the payload shapes are
// identical (domain.SearchHit, domain.Note, service.WikiPageSummary, …) and
// the plugin's own data classes do not move at all. What changes is where the
// bytes come from — and with them, the plugin stops requiring a running
// `repotory-server` to search, read, write or organize anything.
//
// The consequence users feel: install `repotory-mcp` and the panel works with
// no service running. Everything the server alone could still do (moving a
// note between projects, deep links into the web UI) says so plainly instead
// of failing vaguely.
package dev.repotory.jetbrains

import com.google.gson.JsonObject
import com.intellij.openapi.components.Service

/**
 * The MCP-backed half of the plugin.
 *
 * One long-lived client process, started on first use and reused afterwards:
 * the MCP handshake is cheap but not free, and a search that spawns a server
 * per keystroke would make the panel feel slow for no reason.
 */
@Service(Service.Level.APP)
class RepotoryMcp {
    companion object {
        const val TOOL_NOTES_SEARCH = "repotory_notes_search"
        const val TOOL_NOTES_READ = "repotory_notes_read"
        const val TOOL_NOTES_CREATE = "repotory_notes_create"
        const val TOOL_NOTES_UPDATE = "repotory_notes_update"
        const val TOOL_NOTES_ORGANIZE = "repotory_notes_organize"
        const val TOOL_WIKI_SEARCH = "repotory_wiki_search"
        const val TOOL_WIKI_READ = "repotory_wiki_read"
        const val TOOL_PROJECTS_LIST = "repotory_projects_list"
    }

    private var client: McpStdioClient? = null
    private var startProblem: String? = null

    /**
     * Why the MCP channel is unusable, when it is.
     *
     * The settings page shows this next to the binary field: "repotory-mcp
     * was not found" is only actionable if the user can see WHERE it looked.
     */
    val problem: String? get() = startProblem

    /**
     * Call one tool and return its RAW text result.
     *
     * The chat console (ADR-0036) needs the raw text, not a re-shaped
     * payload: the ask tool answers in prose with a source list appended
     * (ADR-0033), and the source list has to be read back out of that text.
     * A typed accessor would have to flatten the answer before the panel
     * could see which sources it cites.
     */
    @Throws(McpException::class)
    fun callToolText(tool: String, arguments: Map<String, Any?>): String {
        val args = JsonObject()
        for ((key, value) in arguments) {
            when (value) {
                null -> Unit
                is Number -> args.addProperty(key, value)
                is Boolean -> args.addProperty(key, value)
                is String -> args.addProperty(key, value)
                else -> args.addProperty(key, value.toString())
            }
        }
        return callTool(tool, args)
    }

    /**
     * Search notes and todos.
     *
     * The scope is deliberately unscoped: a project scope would FILTER the
     * notes down to that project, while the plugin's contract is "everything
     * the user knows", the same the desktop app's knowledge search returns.
     *
     * [path] is the workspace folder: the tool re-ranks the hits towards it
     * (the notes recorded in that repository come first) but filters nothing —
     * the notes tool has no project parameter, so the chat console applies
     * the project lock itself and says how many hits it dropped.
     */
    fun search(query: String, path: String? = null): SearchOutcome {
        if (query.isBlank()) return SearchOutcome.Hits(emptyList())
        return try {
            val args = JsonObject().apply {
                addProperty("query", query)
                if (!path.isNullOrBlank()) addProperty("path", path)
            }
            val text = callTool(TOOL_NOTES_SEARCH, args)
            SearchOutcome.Hits(McpPayloads.parseHits(TOOL_NOTES_SEARCH, text))
        } catch (e: McpException) {
            SearchOutcome.Failure(e.message ?: "Search failed")
        }
    }

    /**
     * Search the APPROVED knowledge pages (ADR-0032).
     *
     * [projectId] scopes the search SERVER-side: a project scope means "that
     * project's pages PLUS the global ones" (ADR-0031), which is what the
     * chat console's project lock does (ADR-0036 decision 2). Without it the
     * search spans every project, the same the desktop app's /wiki surface
     * does by default.
     */
    fun searchWiki(query: String, projectId: Long? = null): SearchOutcome {
        if (query.isBlank()) return SearchOutcome.Hits(emptyList())
        return try {
            val args = JsonObject().apply {
                addProperty("query", query)
                if (projectId != null && projectId > 0) addProperty("project_id", projectId)
            }
            val text = callTool(TOOL_WIKI_SEARCH, args)
            SearchOutcome.Hits(McpPayloads.parseWikiHits(TOOL_WIKI_SEARCH, text))
        } catch (e: McpException) {
            SearchOutcome.Failure(e.message ?: "Knowledge-page search failed")
        }
    }

    /**
     * Read one note.
     *
     * Returns null when the note does not exist or the read failed: the
     * caller falls back to the matched excerpt, because a pane that shows
     * nothing is worse than one that shows the excerpt and says so.
     */
    fun note(id: Long): NoteDetail? {
        return try {
            val args = JsonObject().apply { addProperty("id", id) }
            val text = callTool(TOOL_NOTES_READ, args)
            McpPayloads.parseNote(TOOL_NOTES_READ, text)
        } catch (e: McpException) {
            null
        }
    }

    /** Read one approved page with the graph around it. */
    fun wikiPage(id: Long): WikiPageDetail? {
        return try {
            val args = JsonObject().apply { addProperty("id", id) }
            val text = callTool(TOOL_WIKI_READ, args)
            McpPayloads.parseWikiPage(TOOL_WIKI_READ, text)
        } catch (e: McpException) {
            null
        }
    }

    /** The project list for the editor's project dropdown. */
    fun projects(): List<ProjectOption> {
        return try {
            val text = callTool(TOOL_PROJECTS_LIST, JsonObject())
            McpPayloads.parseProjects(TOOL_PROJECTS_LIST, text)
        } catch (e: McpException) {
            emptyList()
        }
    }

    /**
     * Ask the configured AI to tidy one note, and return the PROPOSAL.
     *
     * The service writes nothing; the caller opens the editor with the result
     * and the user's save lands it.
     */
    @Throws(McpException::class)
    fun organizeNote(id: Long): NoteOrganizeResult {
        val args = JsonObject().apply { addProperty("id", id) }
        val text = callTool(TOOL_NOTES_ORGANIZE, args)
        return McpPayloads.parseOrganizeProposal(TOOL_NOTES_ORGANIZE, text)
    }

    /**
     * Save one existing note's fields, and return it as stored.
     *
     * `pinned` is sent explicitly (ADR-0035 decision 3): the MCP tool takes
     * it as an OPTIONAL boolean, and "omitted" means "keep the current
     * value" — so a draft that did not touch the pin state must say so
     * rather than let the field fall out of the request and leave the note
     * silently unpinned.
     */
    @Throws(McpException::class)
    fun updateNote(draft: NoteDraft): NoteDetail {
        val args = JsonObject().apply {
            addProperty("id", draft.id)
            addProperty("content", draft.content)
            addProperty("title", draft.title)
            addProperty("tags", draft.tags)
            addProperty("category", draft.kind)
            addProperty("pinned", draft.pinned)
        }
        val text = callTool(TOOL_NOTES_UPDATE, args)
        return McpPayloads.parseNote(TOOL_NOTES_UPDATE, text)
    }

    /** Create a note under a project and return it as stored. */
    @Throws(McpException::class)
    fun createNote(draft: NoteDraft): NoteDetail {
        val args = JsonObject().apply {
            addProperty("project_id", draft.projectId)
            addProperty("title", draft.title)
            addProperty("content", draft.content)
            addProperty("tags", draft.tags)
            addProperty("category", draft.kind)
        }
        val text = callTool(TOOL_NOTES_CREATE, args)
        return McpPayloads.parseNote(TOOL_NOTES_CREATE, text)
    }

    /** Stop the MCP server (used when the settings change or the plugin closes). */
    fun shutdown() {
        client?.close()
        client = null
    }

    /**
     * Call one tool, starting the server if it is not running yet.
     *
     * A server that died between two calls is restarted once: the child
     * process is long-lived, and a crash (a database lock, a killed
     * terminal) must not leave the panel dead until the IDE restarts.
     */
    @Throws(McpException::class)
    private fun callTool(tool: String, arguments: JsonObject): String {
        var lastError: McpException? = null
        for (attempt in 0 until 2) {
            val active = try {
                client()
            } catch (e: McpException) {
                lastError = e
                continue
            }
            try {
                return active.callTool(tool, arguments)
            } catch (e: McpException) {
                lastError = e
                // A dead child is worth one restart; anything else (a tool that
                // refused, a timeout) is the answer itself.
                if (!active.isRunning) {
                    shutdown()
                } else {
                    throw e
                }
            }
        }
        throw lastError ?: McpException("repotory-mcp call failed: $tool")
    }

    /** The running client, starting it when needed. */
    @Throws(McpException::class)
    private fun client(): McpStdioClient {
        client?.let { if (it.isRunning) return it; it.close() }
        val resolution = McpBinaryResolver.resolveHere(RepotorySettings.mcpBinaryPath())
        if (!resolution.found) {
            val message = resolution.problem ?: "repotory-mcp not found"
            startProblem = message
            throw McpException(message)
        }
        val started = McpStdioClient(
            command = resolution.path!!,
            clientVersion = pluginVersion(),
            onStderr = { line -> com.intellij.openapi.diagnostic.Logger.getInstance(MCP_LOG).debug("repotory-mcp: $line") },
        )
        try {
            started.start()
        } catch (e: McpException) {
            startProblem = e.message
            throw e
        }
        startProblem = null
        client = started
        return started
    }

    private fun pluginVersion(): String = try {
        com.intellij.ide.plugins.PluginManagerCore
            .getPlugin(com.intellij.openapi.extensions.PluginId.getId("dev.repotory.jetbrains"))
            ?.version
            ?: "0.0.0"
    } catch (_: Exception) {
        "0.0.0"
    }
}

private const val MCP_LOG = "dev.repotory.jetbrains"

/** Convenience accessor for the application-level MCP client. */
val repotoryMcp: RepotoryMcp
    get() = com.intellij.openapi.components.service()
