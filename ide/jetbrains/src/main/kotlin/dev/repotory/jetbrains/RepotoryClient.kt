// Repotory for JetBrains — the thin HTTP half of the plugin.
//
// ADR-0027 (Accepted 2026-10-22): the plugin is a THIN SHELL. Everything it
// shows comes from the shared service layer, and the plugin holds no retrieval
// logic of its own. That is the whole point of the thin-client discipline
// (ADR-0009, ADR-0017): the desktop app, the VS Code extension and this
// plugin must not drift apart, because none of them owns the search
// implementation.
//
// ADR-0035 (Accepted 2026-10-30) decision 2 moved the EIGHT data calls onto
// MCP (see RepotoryMcp.kt), which is the same switch the VS Code extension
// made long ago: "MCP primary, HTTP filling the gaps". Two things stay here
// on purpose:
//
//   ① MoveNote — re-filing knowledge between projects is a human decision an
//      agent must not make, so it never enters MCP.
//   ② the web-UI probe and the deep links — a deep link needs a server that
//      actually serves the UI, which is a property of the HTTP deployment.
//
// Everything else the plugin used to fetch over /api/rpc now goes through the
// MCP channel, so the panel works with no `repotory-server` running at all.
package dev.repotory.jetbrains

import com.google.gson.Gson
import com.google.gson.JsonArray
import com.google.gson.JsonObject
import com.google.gson.JsonParser
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.service
import com.intellij.openapi.diagnostic.Logger
import java.net.HttpURLConnection
import java.net.URI
import java.nio.charset.StandardCharsets

private const val LOG = "dev.repotory.jetbrains"

/** One search result, as the plugin needs it: what it is and where it lives. */
data class RepotoryHit(
    val type: String,
    val id: Long,
    val projectId: Long,
    val title: String,
    val snippet: String,
    /** The SPA route this hit opens, e.g. "/project/12?noteId=345". */
    val route: String,
    /**
     * The wiki page kind (concept | entity | synthesis | source | …) for a page
     * hit; empty for a note or a todo. The badge shows it, because "page" alone
     * tells the reader nothing about what kind of page it is.
     */
    val kind: String = "",
    /** The wiki page slug for a page hit; empty otherwise. */
    val slug: String = "",
)

/** One edge of the page graph: another page, and how it relates to this one. */
data class PageEdge(
    val pageId: Long,
    val slug: String,
    val title: String,
    val kind: String,
    val relation: String,
)

/**
 * One approved knowledge page in full (ADR-0032).
 *
 * The page body plus the graph around it: the pages it links to, the pages
 * that link to it, and the notes it was compiled from. Reading a page is
 * reading published knowledge — the service refuses anything that is not
 * approved, so the plugin never has to ask.
 */
data class WikiPageDetail(
    val id: Long,
    val slug: String,
    val title: String,
    val kind: String,
    val projectId: Long,
    val updatedAt: String,
    val content: String,
    val source: String,
    val outLinks: List<PageEdge>,
    val inLinks: List<PageEdge>,
    val sourceNoteIds: List<Long>,
)

/**
 * What a search produced.
 *
 * It is a sealed outcome rather than a plain list because the UI has to tell
 * "nothing matched" from "the search never ran": both used to arrive as an
 * empty list, so an unreachable service rendered exactly like a clean miss —
 * the failure that teaches users to distrust a working query.
 */
sealed interface SearchOutcome {
    /** Hits, possibly empty (an empty list is a real "nothing matched"). */
    data class Hits(val hits: List<RepotoryHit>) : SearchOutcome

    /**
     * The search could not be carried out; [message] says why, for the UI.
     * [offline] marks a transport failure (nothing answered at the address),
     * which is the one case where offering "start the local service" makes
     * sense; a 4xx/5xx from a live server is not something a restart fixes.
     */
    data class Failure(val message: String, val offline: Boolean = false) : SearchOutcome
}

/** Raised when an RPC call yields no payload; the message is user-presentable. */
class RepotoryRpcException(message: String) : Exception(message)

/**
 * One note's full content, as the reader pane needs it.
 *
 * A search hit carries only the matched excerpt (SearchHit.snippet); the
 * detail pane expands a hit into the note itself, which is what the
 * notes_read tool returns. `kind` and `pinned` ride along because the
 * editor round-trips them: a draft that lost the kind would silently
 * re-file a knowledge note as "other" on the next save, and one that lost
 * `pinned` would unpin it — data loss disguised as a metadata default.
 */
data class NoteDetail(
    val id: Long,
    val title: String,
    val body: String,
    val updatedAt: String,
    val tags: String,
    val kind: String = "",
    val projectId: Long = 0,
    val pinned: Boolean = false,
)

/** One project as the editor's project dropdown needs it. */
data class ProjectOption(val id: Long, val name: String)

/**
 * The AI tidying proposal for one note (internal/service/note_organize.go).
 * A PROPOSAL, never a write: the editor opens with it and the user's save
 * is what lands it. `warning` carries non-fatal caveats (a truncated reply,
 * a model that ignored the output protocol) shown next to the draft.
 */
data class NoteOrganizeResult(
    val noteId: Long,
    val title: String,
    val content: String,
    val tags: String,
    val warning: String = "",
)

/** The editable fields of a note draft, as the editor dialog holds them. */
data class NoteDraft(
    /** 0 = a new note under `projectId`. */
    val id: Long,
    val projectId: Long,
    val title: String,
    val tags: String,
    val kind: String,
    val content: String,
    val pinned: Boolean = false,
)

/**
 * Thin HTTP client for the Repotory headless service.
 *
 * Two endpoints only:
 *   GET  /health    — liveness (the settings page "test connection" button)
 *   POST /api/rpc   — the generic binding bridge, for MoveNote only
 *
 * The service is loopback-only by design, so the address is a loopback URL and
 * no authentication is involved. The timeouts are deliberately short: the
 * plugin must never hang the IDE's UI thread on a service that is not running.
 */
@Service(Service.Level.APP)
class RepotoryClient {
    companion object {
        const val DEFAULT_SERVER_URL = "http://127.0.0.1:18765"
        private const val CONNECT_TIMEOUT_MS = 1500
        private const val READ_TIMEOUT_MS = 4000
        private val GSON = Gson()
    }

    /** Health probe. True when the service answers /health with 200. */
    fun ping(serverUrl: String): Boolean = try {
        val conn = open(serverUrl, "/health", "GET")
        val code = try {
            conn.responseCode
        } finally {
            conn.disconnect()
        }
        code == 200
    } catch (e: Exception) {
        Logger.getInstance(LOG).debug("repotory ping failed: ${e.message}")
        false
    }

    /**
     * True when the running server also serves the Repotory web UI, i.e. it was
     * started with `--web-dist` (see cmd/server/main.go: without it the process
     * is API-only and every non-/api path 404s).
     *
     * Results deep-link into that UI, so a plain API-only server answers the API
     * but turns every "Open in Repotory" click into a 404. The plugin starts its
     * own local server with the sibling `web/` directory when the release bundle
     * ships one (see RepotoryServer.kt); for a server the user started, the
     * plugin asks this question and says the answer out loud.
     */
    fun servesWebUi(serverUrl: String): Boolean = try {
        val conn = open(serverUrl, "/", "GET")
        try {
            // The SPA root is index.html (serveIndex in cmd/server/main.go). A
            // 404 from the API mux, or any non-HTML answer, means no web UI here.
            conn.responseCode == 200 && (conn.contentType ?: "").contains("text/html", ignoreCase = true)
        } finally {
            conn.disconnect()
        }
    } catch (e: Exception) {
        Logger.getInstance(LOG).debug("repotory web-ui probe failed: ${e.message}")
        false
    }

    /**
     * Calls one binding over the generic JSON-RPC bridge.
     *
     * The bridge is the same one the desktop Wails app is bound to, so the
     * plugin can reach ANY binding without a hand-written REST route per
     * method — and without the plugin growing its own API surface.
     *
     * Throws [RepotoryRpcException] when the call produced no payload: a
     * non-200 status, an `error` envelope, or a body that is not an object.
     * Returning null instead would make every failure indistinguishable from a
     * successful call that legitimately carries no result — the caller could
     * not tell "the server said no" from "the server said nothing".
     */
    @Throws(RepotoryRpcException::class)
    fun rpc(serverUrl: String, method: String, args: List<Any?>): Any? =
        rpc(serverUrl, method, args, READ_TIMEOUT_MS)

    /**
     * Calls one binding over the generic JSON-RPC bridge, with an explicit
     * read timeout for calls that legitimately take minutes (the AI tidy).
     *
     * Throws [RepotoryRpcException] when the call produced no payload: a
     * non-200 status, an `error` envelope, or a body that is not an object.
     * Returning null instead would make every failure indistinguishable from a
     * successful call that legitimately carries no result — the caller could
     * not tell "the server said no" from "the server said nothing".
     */
    @Throws(RepotoryRpcException::class)
    fun rpc(serverUrl: String, method: String, args: List<Any?>, readTimeoutMs: Int): Any? {
        val payload = JsonObject()
        payload.addProperty("method", method)
        val arr = JsonArray()
        args.forEach { arr.add(GSON.toJsonTree(it)) }
        payload.add("args", arr)

        val conn = open(serverUrl, "/api/rpc", "POST", readTimeoutMs)
        try {
            conn.setRequestProperty("Content-Type", "application/json")
            conn.doOutput = true
            conn.outputStream.use { it.write(payload.toString().toByteArray(StandardCharsets.UTF_8)) }
            val code = conn.responseCode
            if (code != 200) {
                // Failures come back as 4xx/5xx with an {"error":…} body
                // (internal/httpapi/rpc.go); read it so the UI can say what
                // went wrong instead of only that something did.
                val detail = conn.errorStream?.bufferedReader(StandardCharsets.UTF_8)?.use { it.readText() }.orEmpty()
                val reason = try {
                    JsonParser.parseString(detail).asJsonObject.get("error")?.takeIf { it.isJsonPrimitive }?.asString
                } catch (e: Exception) {
                    if (detail.isBlank()) null else detail.trim().take(200)
                }
                Logger.getInstance(LOG).warn("repotory rpc $method -> HTTP $code ${reason ?: ""}")
                throw RepotoryRpcException(
                    "Repotory answered HTTP $code for $method" + (if (reason.isNullOrBlank()) "" else ": $reason"),
                )
            }
            val body = conn.inputStream.bufferedReader(StandardCharsets.UTF_8).readText()
            val envelope = JsonParser.parseString(body)
            if (!envelope.isJsonObject) {
                throw RepotoryRpcException("Repotory returned a non-object response for $method")
            }
            val obj = envelope.asJsonObject
            if (obj.has("error") && !obj.get("error").isJsonNull) {
                val reason = obj.get("error").toString()
                Logger.getInstance(LOG).warn("repotory rpc $method -> $reason")
                throw RepotoryRpcException("Repotory refused $method: $reason")
            }
            return if (obj.has("result")) obj.get("result") else null
        } finally {
            conn.disconnect()
        }
    }

    /** The SPA URL a hit opens in the browser. */
    fun deepLink(serverUrl: String, hit: RepotoryHit): String {
        val base = serverUrl.trimEnd('/')
        return "$base${hit.route}"
    }

    /**
     * Re-files a note under a different project (the project change).
     *
     * The ONE write that deliberately stays on HTTP (ADR-0035 decision 2):
     * re-filing knowledge is a human decision an agent must not make, so it
     * never enters the MCP tool surface.
     */
    @Throws(RepotoryRpcException::class)
    fun moveNote(serverUrl: String, noteId: Long, projectId: Long) {
        rpc(serverUrl, "MoveNote", listOf(noteId, projectId))
    }

    private fun open(serverUrl: String, path: String, method: String, readTimeoutMs: Int = READ_TIMEOUT_MS): HttpURLConnection {
        // URI.create, not URL(String): the latter is deprecated in Java 20+,
        // and URI is what URL.toURI() would hand back anyway.
        val url = URI.create(serverUrl.trimEnd('/') + path).toURL()
        val conn = url.openConnection() as HttpURLConnection
        conn.requestMethod = method
        conn.connectTimeout = CONNECT_TIMEOUT_MS
        conn.readTimeout = readTimeoutMs
        // The service is loopback-only; redirects elsewhere would be a bug, not
        // a feature. Following them blindly would let a hostile local process
        // bounce the plugin's request elsewhere.
        conn.instanceFollowRedirects = false
        return conn
    }
}

/** Convenience accessor for the application-level client. */
val repotoryClient: RepotoryClient
    get() = service()
