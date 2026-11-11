// The direct probes are pure JVM + JDK HttpClient, so they are tested against
// a REAL local stub: the interesting behaviour is which path gets tried next,
// and that is decided by the response the endpoint actually sends.
package dev.repotory.jetbrains

import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertContains

/** A throwaway endpoint recording the paths it served, closed in close(). */
private class StubServer {
    val paths = mutableListOf<String>()
    val base: String
    private val handler: MutableMap<String, Pair<Int, String>> = mutableMapOf()
    private val server: HttpServer

    init {
        server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/") { ex: HttpExchange ->
            paths.add(ex.requestURI.path)
            val (status, body) = handler[ex.requestURI.path] ?: 404 to """{"error":"not found"}"""
            ex.sendResponseHeaders(status, body.toByteArray().size.toLong())
            ex.responseBody.use { it.write(body.toByteArray()) }
            ex.close()
        }
        server.start()
        base = "http://127.0.0.1:${server.address.port}"
    }

    fun respond(path: String, status: Int, body: String): StubServer = apply {
        handler[path] = status to body
    }

    fun close() = server.stop(0)
}

class ProviderProbeTest {

    @Test
    fun `candidate URLs mirror the server shapes`() {
        assertEquals(
            listOf("https://api.deepseek.com/models", "https://api.deepseek.com/v1/models"),
            aiCandidateUrls("https://api.deepseek.com", "/models"),
        )
        assertEquals(
            listOf("http://localhost:8080/api/v1/models", "http://localhost:8080/v1/models"),
            aiCandidateUrls("http://localhost:8080/api/v1/", "/models"),
        )
        assertEquals(
            listOf("http://localhost:1234/v1/models"),
            aiCandidateUrls("http://localhost:1234/v1", "/models"),
        )
    }

    @Test
    fun `model parsing reads both shapes`() {
        assertEquals(listOf("a", "b"), parseProbeModels("""{"data":[{"id":"b"},{"id":"a"}]}"""))
        assertEquals(listOf("qwen"), parseProbeModels("""{"models":[{"name":"qwen"}]}"""))
        assertEquals(emptyList(), parseProbeModels("not json"))
    }

    @Test
    fun `probeModels walks candidates on 404 and adopts the answering base`() {
        val server = StubServer()
        try {
            server.respond("/api/v1/models", 404, """{"error":"not found"}""")
            server.respond("/v1/models", 200, """{"data":[{"id":"m2"},{"id":"m1"}]}""")
            val result = probeModels("${server.base}/api/v1", "key")
            assertEquals(listOf("/api/v1/models", "/v1/models"), server.paths)
            assertEquals(listOf("m1", "m2"), result.models)
            assertEquals("${server.base}/v1", result.resolvedBaseUrl)
        } finally {
            server.close()
        }
    }

    @Test
    fun `probeModels stops on auth failure without probing siblings`() {
        val server = StubServer()
        try {
            server.respond("/models", 401, """{"error":{"message":"bad key"}}""")
            server.respond("/v1/models", 200, """{"data":[]}""")
            val err = assertFailsWith<RuntimeException> { probeModels(server.base, "wrong") }
            assertContains(err.message ?: "", "auth failed (401)")
            assertEquals(listOf("/models"), server.paths)
        } finally {
            server.close()
        }
    }

    @Test
    fun `probeChat extracts the reply and the resolved base`() {
        val server = StubServer()
        try {
            server.respond("/chat/completions", 200, """{"choices":[{"message":{"content":" OK "}}]}""")
            val result = probeChat(server.base, "m", "key")
            assertEquals("OK", result.reply)
            assertEquals(server.base, result.resolvedBaseUrl)
        } finally {
            server.close()
        }
    }

    @Test
    fun `probeChat reports a 200 error envelope in the provider's words`() {
        val server = StubServer()
        try {
            server.respond("/chat/completions", 200, """{"error":{"message":"provider_unavailable — retry later"}}""")
            val err = assertFailsWith<RuntimeException> { probeChat(server.base, "m", "key") }
            assertContains(err.message ?: "", "provider_unavailable")
        } finally {
            server.close()
        }
    }
}
