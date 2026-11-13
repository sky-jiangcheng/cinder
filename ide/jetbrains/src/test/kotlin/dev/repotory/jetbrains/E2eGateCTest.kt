package dev.repotory.jetbrains

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Gate C (ADR-0059): the JetBrains panel works end to end against REAL binaries.
 *
 * Gate B pinned the behaviour rules with fakes on both sides. This is the other
 * half: the panel's own conversation model ([ChatSession]), driven through its
 * own [ChatTransport], reaching a real `cinder-mcp` process over stdio and a
 * real `cinder-server` over HTTP — completing the three journeys a user makes:
 *
 *   1. search — a query returns hits the panel can render;
 *   2. deep link — a hit produces the SPA route, and an API-only server is
 *      refused instead of opened as a dead 404 (ADR-0035 decision 6);
 *   3. ask — a question comes back as an answer with its source list.
 *
 * No IDE, no tool window, no display: the model is plain Kotlin behind a
 * transport port (ADR-0036), which is why this runs on a headless runner.
 *
 * The test is SKIPPED (not failed) when the binaries are absent, so a plain
 * `gradle test` on a machine without the Go toolchain stays green — the gate
 * itself is `scripts/e2e-gate-c.sh`, which builds them and fails loudly.
 *
 * Environment (exported by the script):
 *   CINDER_E2E_MCP     — the built cinder-mcp binary
 *   CINDER_E2E_SERVER  — the built cinder-server binary
 *   CINDER_E2E_HOME    — an ISOLATED HOME (never the user's real one)
 */
class E2eGateCTest {
    private val mcpBin = System.getenv("CINDER_E2E_MCP") ?: ""
    private val serverBin = System.getenv("CINDER_E2E_SERVER") ?: ""
    private val homeDir = System.getenv("CINDER_E2E_HOME") ?: ""
    private val webDist = System.getenv("CINDER_E2E_WEB_DIST") ?: ""

    private fun requireBinaries(): Boolean {
        if (mcpBin.isBlank() || serverBin.isBlank() || homeDir.isBlank()) {
            println("gate C: skipped — set CINDER_E2E_MCP / CINDER_E2E_SERVER / CINDER_E2E_HOME (scripts/e2e-gate-c.sh does it)")
            return false
        }
        return true
    }

    /** Start a server on [port] and wait for /health. */
    private fun startServer(port: Int, withWebDist: Boolean): Process {
        val args = mutableListOf(serverBin, "-port", port.toString())
        if (withWebDist && webDist.isNotBlank()) {
            args.add("--web-dist")
            args.add(webDist)
        }
        val builder = ProcessBuilder(args)
        builder.environment()["HOME"] = homeDir
        builder.redirectErrorStream(false)
        val proc = builder.start()
        // Drain stderr so a chatty server cannot fill the pipe and wedge.
        Thread { proc.errorStream?.readBytes() }.apply { isDaemon = true }.start()
        val deadline = System.currentTimeMillis() + 20_000
        while (System.currentTimeMillis() < deadline) {
            if (try {
                    RepotoryClient().ping("http://127.0.0.1:$port")
                } catch (e: Exception) {
                    false
                }
            ) {
                return proc
            }
            Thread.sleep(150)
        }
        proc.destroyForcibly()
        throw IllegalStateException("cinder-server never became healthy on port $port")
    }

    /** A fake OpenAI-compatible endpoint: enough for the ask tool loop. */
    private fun startFakeModel(port: Int): com.sun.net.httpserver.HttpServer {
        val server = com.sun.net.httpserver.HttpServer.create(java.net.InetSocketAddress("127.0.0.1", port), 0)
        server.createContext("/") { exchange ->
            exchange.requestBody.readBytes()
            val body = """
                {"id":"e2e","object":"chat.completion","choices":[{"index":0,
                "message":{"role":"assistant","content":"The gateway retries a 503 because the upstream pod may still be rolling out; the retry lives in pay-gateway, not in the client."},
                "finish_reason":"stop"}],"usage":{}}
            """.trimIndent().toByteArray()
            exchange.responseHeaders.add("Content-Type", "application/json")
            exchange.sendResponseHeaders(200, body.size.toLong())
            exchange.responseBody.use { it.write(body) }
        }
        server.start()
        return server
    }

    /** The transport the plugin would hand the session — real channels. */
    private fun realTransport(client: McpStdioClient, serverUrl: String): ChatTransport =
        object : ChatTransport {
            override fun callTool(name: String, args: Map<String, Any?>): String {
                val json = com.google.gson.JsonObject()
                for ((key, value) in args) {
                    when (value) {
                        null -> Unit
                        is Number -> json.addProperty(key, value)
                        is Boolean -> json.addProperty(key, value)
                        else -> json.addProperty(key, value.toString())
                    }
                }
                return client.callTool(name, json)
            }

            override fun rpc(method: String, args: List<Any?>): Map<String, Any?>? {
                val result = RepotoryClient().rpc(serverUrl, method, args) as? com.google.gson.JsonObject
                    ?: return null
                val out = mutableMapOf<String, Any?>()
                for ((key, value) in result.entrySet()) {
                    out[key] = when {
                        value.isJsonPrimitive -> value.asJsonPrimitive.let {
                            when {
                                it.isString -> it.asString
                                it.isBoolean -> it.asBoolean
                                else -> it.asNumber
                            }
                        }
                        else -> value.toString()
                    }
                }
                return out
            }

            override fun serverUrl(): String = serverUrl

            override fun workspacePath(): String? = null
        }

    private fun lastTurn(events: List<ChatEvent>): ChatTurn? =
        events.filterIsInstance<ChatEvent.Turn>().lastOrNull()?.turn

    @Test
    fun thePanelCompletesSearchDeepLinkAndAskAgainstRealBinaries() {
        if (!requireBinaries()) return

        val aiServer = startFakeModel(19899)
        val server = startServer(19872, withWebDist = true)
        val client = McpStdioClient(
            command = mcpBin,
            // Isolated HOME: the gate must never read or write the user's real
            // Cinder database. Without this the child inherits the test JVM's
            // environment and the assertions run against whatever the user has.
            env = mapOf("HOME" to homeDir),
        )
        try {
            client.start()
            val serverUrl = "http://127.0.0.1:19872"
            val transport = realTransport(client, serverUrl)

            // Seed through the product's own RPC bridge (the desktop's path).
            val seed = RepotoryClient()
            val created = seed.rpc(
                serverUrl, "CreateNoteWithMeta",
                listOf(
                    1, "Retry policy on 503",
                    "The gateway retries a 503 because the upstream pod may still be rolling out. See pay-gateway.",
                    "retry,gateway", "knowledge", "manual",
                ),
            ) as? com.google.gson.JsonObject ?: error("seed note was not created")
            // The id comes from the row that was actually written: hard-coding
            // it made the gate depend on a pristine database, so a re-run
            // against the same HOME failed for the wrong reason.
            val noteId = created.get("id").asLong
            seed.rpc(serverUrl, "UpdateConfig", listOf("ai_chat_base_url", "http://127.0.0.1:19899/v1"))
            seed.rpc(serverUrl, "UpdateConfig", listOf("ai_chat_model", "e2e-model"))

            val events = mutableListOf<ChatEvent>()
            val session = ChatSession(transport) { events.add(it) }

            // ---- journey 1: search ----------------------------------------
            session.run("retry 503", ChatMode.NOTES)
            val searchTurn = lastTurn(events)
            assertEquals(TurnState.DONE, searchTurn?.state, "search failed: ${searchTurn?.error}")
            val cards = searchTurn?.cards.orEmpty()
            assertTrue(cards.isNotEmpty(), "no hits came back from the real MCP server")
            assertTrue(
                cards.first().hit.title.contains("Retry policy", ignoreCase = true),
                "unexpected first hit: ${cards.first().hit.title}",
            )

            // ---- journey 2: deep link -------------------------------------
            val hit = cards.first().hit
            assertEquals(
                "$serverUrl/project/1?noteId=$noteId",
                RepotoryClient().deepLink(serverUrl, hit),
                "the deep link must point at the row the search actually returned",
            )
            // The accepting branch: the server serves the SPA. Only meaningful
            // when a built frontend was supplied — without one the server is
            // API-only by definition, which is the REFUSING branch's premise,
            // not a product defect.
            if (webDist.isNotBlank() && java.io.File(webDist, "index.html").exists()) {
                assertTrue(
                    RepotoryClient().servesWebUi(serverUrl),
                    "a server started with --web-dist must be recognised as serving the UI",
                )
            } else {
                println("gate C: no built web frontend (CINDER_E2E_WEB_DIST) — accepting deep-link branch skipped")
            }

            // The refusing branch: an API-only server must be recognised as
            // NOT serving the UI, so the panel can explain instead of opening
            // a dead link.
            val apiOnly = startServer(19873, withWebDist = false)
            try {
                assertTrue(
                    !RepotoryClient().servesWebUi("http://127.0.0.1:19873"),
                    "an API-only server must not be mistaken for a UI server",
                )
            } finally {
                apiOnly.destroyForcibly()
            }

            // ---- journey 3: ask -------------------------------------------
            session.run("why do we retry on 503?", ChatMode.ASK)
            val askTurn = lastTurn(events)
            assertEquals(TurnState.DONE, askTurn?.state, "ask failed: ${askTurn?.error}")
            assertTrue(
                askTurn?.answer.orEmpty().contains("pay-gateway") ||
                    askTurn?.answer.orEmpty().contains("rolling out"),
                "the answer did not come from the model stub: ${askTurn?.answer}",
            )
            assertTrue(
                askTurn?.sources.orEmpty().isNotEmpty(),
                "an evidence-backed answer must carry its sources",
            )
        } finally {
            client.close()
            server.destroyForcibly()
            aiServer.stop(0)
        }
    }
}
