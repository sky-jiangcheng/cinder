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

    /** One note or todo hit, with the SPA route it opens. */
    private fun parseHit(raw: JsonElement): RepotoryHit? {
        if (!raw.isJsonObject) return null
        val obj = raw.asJsonObject
        val type = obj.str("type")
        if (type.isEmpty()) return null
        val id = obj.long("id")
        if (id <= 0L) return null
        val projectId = obj.long("project_id")
        // The SPA routes (web/src/App.tsx): a note opens the project page on
        // the notes tab with ?noteId= (ProjectDetail reads `noteId`, not
        // `note`); a todo opens the project's todo tab. The route vocabulary
        // belongs to the frontend, so it is read from the frontend's contract
        // rather than invented here.
        val route = when (type) {
            "note" -> "/project/$projectId?noteId=$id"
            "todo" -> "/project/$projectId?tab=todos"
            else -> "/project/$projectId"
        }
        return RepotoryHit(
            type = type,
            id = id,
            projectId = projectId,
            title = obj.str("title"),
            snippet = obj.str("snippet"),
            route = route,
        )
    }

    /** One approved page as a list row; its route is the SPA's /wiki/:id. */
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
            route = "/wiki/$id",
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
