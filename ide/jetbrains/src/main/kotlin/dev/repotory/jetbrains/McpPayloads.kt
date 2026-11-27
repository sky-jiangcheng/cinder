// Cinder for JetBrains — parsers for the MCP tool payloads.
//
// ADR-0035 decision 2 moves the plugin's eight data calls onto MCP. The
// payloads it has to read are the SAME structs the HTTP bridge used to
// marshal, so the plugin's own data classes (RepotoryHit, NoteDetail,
// WikiPageDetail, ProjectOption, NoteOrganizeResult) stay exactly as they
// were — only the transport changed. That is the thin-client discipline
// (ADR-0009) paying off: no UI code moves, no field is renamed, no second
// shape of "a note" comes into existence.
//
// THE ONE DANGEROUS DETAIL
//
// The MCP tools answer with JSON on the success path and with PLAIN TEXT on
// the error path (`makeTextResult` in cmd/mcp/results.go): "note not found",
// "error: …", "refusing to update note 7 …". A parser that treats unparseable
// text as "no rows" turns a failed call into an empty list — and an empty
// list is the one answer a user cannot tell apart from "nothing matched".
// Every parser here therefore FAILS LOUDLY on anything that is not the shape
// it expects, carrying the server's own words.
package dev.repotory.jetbrains

import com.google.gson.JsonArray
import com.google.gson.JsonElement
import com.google.gson.JsonObject
import com.google.gson.JsonParser

/** Parsers for the payloads the MCP tools return. */
object McpPayloads {
    /**
     * Parse a tool answer as JSON, or fail with the server's own message.
     *
     * The error path of every tool is a text result, so "this is not JSON"
     * is not a malformed payload — it is the tool saying no. Saying so out
     * loud is the whole point.
     */
    @Throws(McpException::class)
    fun parseJson(tool: String, text: String): JsonElement {
        val trimmed = text.trim()
        if (trimmed.isEmpty()) {
            throw McpException("Cinder tool $tool returned an empty answer")
        }
        return try {
            JsonParser.parseString(trimmed)
        } catch (e: Exception) {
            throw McpException("Cinder tool $tool said: $trimmed", e)
        }
    }

    /** The JSON array a list tool answers with. */
    @Throws(McpException::class)
    fun parseArray(tool: String, text: String): JsonArray {
        val element = parseJson(tool, text)
        if (!element.isJsonArray) {
            throw McpException("Cinder tool $tool returned ${element.shape()}, expected a list")
        }
        return element.asJsonArray
    }

    /** The JSON object a single-item tool answers with. */
    @Throws(McpException::class)
    fun parseObject(tool: String, text: String): JsonObject {
        val element = parseJson(tool, text)
        if (!element.isJsonObject) {
            throw McpException("Cinder tool $tool returned ${element.shape()}, expected an object")
        }
        return element.asJsonObject
    }

    /**
     * The hits of `repotory_notes_search` — notes and todos, ranked by the
     * service (domain.SearchHit over the wire: type/id/project_id/title/
     * snippet).
     */
    @Throws(McpException::class)
    fun parseHits(tool: String, text: String): List<RepotoryHit> {
        val array = parseArray(tool, text)
        return array.mapNotNull { element -> parseHit(element) }
    }

    /**
     * The page summaries of `repotory_wiki_search`
     * (service.WikiPageSummary: id/slug/title/kind/project_id/updated_at/
     * snippet).
     */
    @Throws(McpException::class)
    fun parseWikiHits(tool: String, text: String): List<RepotoryHit> {
        val array = parseArray(tool, text)
        return array.mapNotNull { element -> parseWikiHit(element) }
    }

    /** One note (domain.Note over the wire). */
    @Throws(McpException::class)
    fun parseNote(tool: String, text: String): NoteDetail {
        val obj = parseObject(tool, text)
        return NoteDetail(
            id = obj.long("id"),
            title = obj.str("title"),
            body = obj.str("content"),
            updatedAt = obj.str("updated_at"),
            tags = obj.str("tags"),
            kind = obj.str("kind"),
            projectId = obj.long("project_id"),
            pinned = obj.bool("pinned"),
        )
    }

    /** One approved page with the graph around it (service.WikiPageDetail). */
    @Throws(McpException::class)
    fun parseWikiPage(tool: String, text: String): WikiPageDetail {
        val obj = parseObject(tool, text)
        return WikiPageDetail(
            id = obj.long("id"),
            slug = obj.str("slug"),
            title = obj.str("title"),
            kind = obj.str("kind"),
            projectId = obj.long("project_id"),
            updatedAt = obj.str("updated_at"),
            content = obj.str("content"),
            source = obj.str("source"),
            outLinks = parseEdges(obj.get("out_links")),
            inLinks = parseEdges(obj.get("in_links")),
            sourceNoteIds = (obj.get("source_note_ids") as? JsonArray)
                ?.mapNotNull { it.takeIf { e -> e.isJsonPrimitive }?.asLong }
                ?: emptyList(),
        )
    }

    /** The project list (domain.Project over the wire). */
    @Throws(McpException::class)
    fun parseProjects(tool: String, text: String): List<ProjectOption> {
        val array = parseArray(tool, text)
        return array.mapNotNull { element ->
            if (!element.isJsonObject) return@mapNotNull null
            val obj = element.asJsonObject
            val id = obj.long("id")
            if (id <= 0L) return@mapNotNull null
            ProjectOption(id, obj.str("name").ifEmpty { "project $id" })
        }
    }

    /**
     * The configuration map of `repotory_config_get` (ADR-0063).
     *
     * "No config" is not a legitimate answer here — the tool always returns
     * the full config object (secrets masked). Anything that is not that
     * shape is the tool refusing, and fails loudly instead of reading as an
     * empty configuration.
     */
    @Throws(McpException::class)
    fun parseConfig(tool: String, text: String): Map<String, String> {
        val obj = parseObject(tool, text)
        val config = obj.get("config") as? JsonObject
            ?: throw McpException("Cinder tool $tool returned no config object")
        return config.entrySet().associate { (k, v) ->
            k to v.takeIf { it.isJsonPrimitive }?.asString.orEmpty()
        }
    }

    /** The workspace resolution of `repotory_workspace_resolve` (ADR-0062):
     * Project / Repository / Candidates, field-for-field the
     * ResolveWorkspacePath binding's answer. "No project" is a legitimate
     * answer (the folder is not in the knowledge base yet), but "I could not
     * read the answer" must never be read as one — so anything that is not
     * a resolution object fails loudly, carrying the server's own words.
     */
    @Throws(McpException::class)
    fun parseWorkspaceResolution(tool: String, text: String): WorkspaceResolution {
        val obj = parseObject(tool, text)
        val project = obj.get("Project") as? JsonObject
        val id = project?.long("id") ?: 0L
        val name = project?.str("name").orEmpty()
        val repositoryPath = (obj.get("Repository") as? JsonObject)?.str("path").orEmpty()
        val candidates = (obj.get("Candidates") as? JsonArray)
            ?.mapNotNull { element ->
                if (!element.isJsonObject) return@mapNotNull null
                val candidate = element.asJsonObject
                val candidateId = candidate.long("id")
                if (candidateId <= 0L) return@mapNotNull null
                ProjectOption(candidateId, candidate.str("name").ifEmpty { "project $candidateId" })
            }
            ?: emptyList()
        return WorkspaceResolution(id, name, repositoryPath, candidates)
    }

    /** The AI tidying proposal (service.NoteOrganizeResult). */
    @Throws(McpException::class)
    fun parseOrganizeProposal(tool: String, text: String): NoteOrganizeResult {
        val obj = parseObject(tool, text)
        val content = obj.str("content")
        if (content.isBlank()) {
            throw McpException("Cinder tool $tool returned an empty proposal")
        }
        return NoteOrganizeResult(
            noteId = obj.long("note_id"),
            title = obj.str("title"),
            content = content,
            tags = obj.str("tags"),
            warning = obj.str("warning"),
        )
    }

    /** One note or todo hit. */
    private fun parseHit(raw: JsonElement): RepotoryHit? {
        if (!raw.isJsonObject) return null
        val obj = raw.asJsonObject
        val type = obj.str("type")
        if (type.isEmpty()) return null
        val id = obj.long("id")
        if (id <= 0L) return null
        val projectId = obj.long("project_id")
        return RepotoryHit(
            type = type,
            id = id,
            projectId = projectId,
            title = obj.str("title"),
            snippet = obj.str("snippet"),
        )
    }

    /** One approved page as a list row. */
    private fun parseWikiHit(raw: JsonElement): RepotoryHit? {
        if (!raw.isJsonObject) return null
        val obj = raw.asJsonObject
        val id = obj.long("id")
        if (id <= 0L) return null
        val projectId = obj.long("project_id")
        return RepotoryHit(
            type = "wiki",
            id = id,
            projectId = projectId,
            title = obj.str("title"),
            snippet = obj.str("snippet"),
            kind = obj.str("kind"),
            slug = obj.str("slug"),
        )
    }

    /** The page graph edges, skipping anything that is not an edge object. */
    private fun parseEdges(raw: JsonElement?): List<PageEdge> {
        val array = raw as? JsonArray ?: return emptyList()
        return array.mapNotNull { element ->
            if (!element.isJsonObject) return@mapNotNull null
            val obj = element.asJsonObject
            val pageId = obj.long("page_id")
            if (pageId <= 0L) return@mapNotNull null
            PageEdge(
                pageId = pageId,
                slug = obj.str("slug"),
                title = obj.str("title"),
                kind = obj.str("kind"),
                relation = obj.str("relation"),
            )
        }
    }
}

private fun JsonObject.str(name: String): String =
    get(name)?.takeIf { it.isJsonPrimitive }?.asString.orEmpty()

private fun JsonObject.long(name: String): Long =
    get(name)?.takeIf { it.isJsonPrimitive }?.asLong ?: 0L

private fun JsonObject.bool(name: String): Boolean =
    get(name)?.takeIf { it.isJsonPrimitive }?.asBoolean ?: false

private fun JsonElement.shape(): String = when {
    isJsonArray -> "a list"
    isJsonObject -> "an object"
    isJsonNull -> "null"
    else -> "a ${asJsonPrimitive.takeIf { it.isString }?.let { "string" } ?: "scalar"}"
}
