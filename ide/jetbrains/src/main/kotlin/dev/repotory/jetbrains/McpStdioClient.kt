// Cinder for JetBrains — minimal MCP stdio client.
//
// ADR-0035 (Accepted 2026-10-30) decision 2: the plugin's data calls go over
// MCP, with HTTP filling two deliberate gaps (MoveNote and the web-UI probe).
// This file is the Kotlin twin of ide/vscode/src/mcpClient.ts: the same wire
// contract, the same discipline (ADR-0009 thin client — it knows the MCP
// protocol and nothing about Cinder's internals).
//
// The transport is newline-delimited JSON-RPC 2.0 over the child's stdin and
// stdout, which is the MCP stdio transport. stdout carries protocol traffic
// only: anything the server prints for humans belongs on stderr, or it would
// be read as a protocol message.
//
// Deliberately free of IntelliJ APIs so it can be exercised by plain JVM
// tests — the IDE is not needed to prove that a JSON-RPC exchange works.
package dev.repotory.jetbrains

import com.google.gson.JsonElement
import com.google.gson.JsonObject
import com.google.gson.JsonParser
import java.io.BufferedReader
import java.io.BufferedWriter
import java.io.InputStreamReader
import java.io.OutputStreamWriter
import java.nio.charset.StandardCharsets
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ExecutionException
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

/** Raised when an MCP exchange fails: the server refused, died, or timed out. */
class McpException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * A thin MCP client over a child process's stdio.
 *
 * It does exactly three things: the `initialize` handshake, `tools/call`, and
 * reading the answers back. Everything Cinder-specific lives in the callers
 * — a file that grew a notion of notes would be a file that has to grow again
 * with every new MCP capability.
 */
class McpStdioClient(
    private val command: String,
    private val args: List<String> = emptyList(),
    private val clientVersion: String = "0.1.0",
    private val onStderr: (String) -> Unit = {},
) {
    companion object {
        // Tool calls are second-scale against the local service; 60s is
        // generous headroom while still bounding a hang. The VS Code client
        // uses the same ceiling, so the two IDEs behave alike on a wedged
        // server.
        private const val REQUEST_TIMEOUT_MS = 60_000L
        private const val PROTOCOL_VERSION = "2024-11-05"
        private const val CLIENT_NAME = "repotory-jetbrains"
    }

    private var process: Process? = null
    private var writer: BufferedWriter? = null
    private var nextId = 1L
    private val pending = ConcurrentHashMap<Long, CompletableFuture<JsonObject>>()

    /** True while the child process is alive and the handshake is done. */
    val isRunning: Boolean get() = process?.isAlive == true

    /**
     * Spawn the server and complete the MCP handshake.
     *
     * Throws [McpException] when the binary cannot be started or does not
     * answer the handshake — the caller turns that into the plugin's
     * "repotory-mcp is not installed" state rather than a dead panel.
     */
    @Throws(McpException::class)
    fun start() {
        if (isRunning) return
        val proc = try {
            ProcessBuilder(listOf(command) + args).start()
        } catch (e: Exception) {
            throw McpException("Cannot start $command: ${e.message ?: e.javaClass.simpleName}", e)
        }
        process = proc
        writer = BufferedWriter(OutputStreamWriter(proc.outputStream, StandardCharsets.UTF_8))

        // stderr is drained on its own thread. A server that writes more to
        // stderr than the pipe buffer holds would BLOCK on the write, and the
        // client would see that as a hang — the deadlock would be invisible.
        val errThread = Thread({ drainStderr(proc) }, "repotory-mcp-stderr")
        errThread.isDaemon = true
        errThread.start()

        val outThread = Thread({ pump(proc) }, "repotory-mcp-stdout")
        outThread.isDaemon = true
        outThread.start()

        try {
            val clientInfo = JsonObject().apply {
                addProperty("name", CLIENT_NAME)
                addProperty("version", clientVersion)
            }
            request(
                "initialize",
                JsonObject().apply {
                    addProperty("protocolVersion", PROTOCOL_VERSION)
                    add("capabilities", JsonObject())
                    add("clientInfo", clientInfo)
                },
            )
            notify("notifications/initialized", JsonObject())
        } catch (e: Exception) {
            close()
            throw e
        }
    }

    /**
     * Call one tool and return its text content.
     *
     * The MCP tools answer with a single text block; the client concatenates
     * the text parts, exactly as the VS Code client does. A call that came
     * back without any text is an error, not an empty answer — "the server
     * said nothing" and "the server said nothing useful" must not look the
     * same to the caller.
     */
    @Throws(McpException::class)
    fun callTool(name: String, arguments: JsonObject = JsonObject()): String {
        val params = JsonObject().apply {
            addProperty("name", name)
            add("arguments", arguments)
        }
        val result = request("tools/call", params)
        val content = result.get("content")
        if (content == null || !content.isJsonArray) {
            throw McpException("repotory-mcp returned no content for $name")
        }
        val text = content.asJsonArray.mapNotNull { part: JsonElement ->
            if (!part.isJsonObject) return@mapNotNull null
            val obj = part.asJsonObject
            if (obj.get("type")?.takeIf { it.isJsonPrimitive }?.asString != "text") {
                return@mapNotNull null
            }
            obj.get("text")?.takeIf { it.isJsonPrimitive }?.asString
        }
        if (text.isEmpty()) {
            throw McpException("repotory-mcp returned no text for $name")
        }
        return text.joinToString("\n")
    }

    /** Stop the server and fail every call still in flight. */
    fun close() {
        failAll(McpException("repotory-mcp client closed"))
        try {
            writer?.close()
        } catch (_: Exception) {
            // The child may already be gone; closing is best-effort.
        }
        writer = null
        process?.destroy()
        process = null
    }

    private fun request(method: String, params: JsonObject): JsonObject {
        val out = writer ?: throw McpException("repotory-mcp is not running")
        val id = nextId++
        val future = CompletableFuture<JsonObject>()
        pending[id] = future

        val message = JsonObject().apply {
            addProperty("jsonrpc", "2.0")
            addProperty("id", id)
            addProperty("method", method)
            add("params", params)
        }
        try {
            // One writer, one lock: two threads writing to the same pipe
            // would interleave halves of two JSON lines.
            synchronized(out) {
                out.write(message.toString())
                out.write("\n")
                out.flush()
            }
        } catch (e: Exception) {
            pending.remove(id)
            throw McpException("Cannot write to $command: ${e.message ?: e.javaClass.simpleName}", e)
        }

        return try {
            future.get(REQUEST_TIMEOUT_MS, TimeUnit.MILLISECONDS)
        } catch (e: TimeoutException) {
            pending.remove(id)
            throw McpException("repotory-mcp request timed out: $method", e)
        } catch (e: ExecutionException) {
            pending.remove(id)
            val cause = e.cause
            throw McpException(cause?.message ?: "repotory-mcp request failed: $method", cause)
        }
    }

    private fun notify(method: String, params: JsonObject) {
        val out = writer ?: return
        val message = JsonObject().apply {
            addProperty("jsonrpc", "2.0")
            addProperty("method", method)
            add("params", params)
        }
        try {
            synchronized(out) {
                out.write(message.toString())
                out.write("\n")
                out.flush()
            }
        } catch (e: Exception) {
            throw McpException("Cannot write to $command: ${e.message ?: e.javaClass.simpleName}", e)
        }
    }

    private fun pump(proc: Process) {
        try {
            BufferedReader(InputStreamReader(proc.inputStream, StandardCharsets.UTF_8)).use { reader ->
                while (true) {
                    val line = reader.readLine() ?: break
                    if (line.isBlank()) continue
                    dispatch(line)
                }
            }
        } catch (e: Exception) {
            failAll(McpException("repotory-mcp closed its output: ${e.message ?: e.javaClass.simpleName}", e))
            return
        }
        // EOF: the server exited on its own. Every pending call fails now
        // instead of waiting out its full timeout.
        val code = try {
            if (proc.isAlive) "?" else proc.exitValue().toString()
        } catch (_: Exception) {
            "?"
        }
        failAll(McpException("repotory-mcp exited (code $code)"))
    }

    private fun drainStderr(proc: Process) {
        try {
            BufferedReader(InputStreamReader(proc.errorStream, StandardCharsets.UTF_8)).use { reader ->
                while (true) {
                    val line = reader.readLine() ?: break
                    if (line.isNotBlank()) onStderr(line)
                }
            }
        } catch (_: Exception) {
            // The process died; nothing left to report.
        }
    }

    private fun dispatch(line: String) {
        val message = try {
            JsonParser.parseString(line)
        } catch (_: Exception) {
            // Not JSON: a server that writes prose to stdout. Ignore it
            // rather than treating it as a protocol violation.
            return
        }
        if (!message.isJsonObject) return
        val obj = message.asJsonObject
        val id = readId(obj.get("id")) ?: return
        val future = pending.remove(id) ?: return

        val error = obj.get("error")
        if (error != null && !error.isJsonNull) {
            val detail = if (error.isJsonObject) {
                error.asJsonObject.get("message")?.takeIf { it.isJsonPrimitive }?.asString
            } else {
                error.asString
            }
            future.completeExceptionally(McpException(detail ?: "repotory-mcp refused the request"))
            return
        }
        val result = obj.get("result")
        if (result == null || result.isJsonNull || !result.isJsonObject) {
            future.completeExceptionally(McpException("repotory-mcp returned no result"))
            return
        }
        future.complete(result.asJsonObject)
    }

    /** JSON-RPC ids are numbers here; a string id is tolerated, nothing else is. */
    private fun readId(element: JsonElement?): Long? {
        if (element == null || !element.isJsonPrimitive) return null
        val primitive = element.asJsonPrimitive
        return when {
            primitive.isNumber -> primitive.asLong
            primitive.isString -> primitive.asString.toLongOrNull()
            else -> null
        }
    }

    private fun failAll(error: McpException) {
        for (future in pending.values) future.completeExceptionally(error)
        pending.clear()
    }
}
