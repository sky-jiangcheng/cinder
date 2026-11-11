// Direct probes for the AI provider dialog, sent by the IDE straight at the
// configured endpoint.
//
// Same reason as the VS Code panel's aiProbe.ts: the dialog's other actions
// (read config, save, "test the saved provider") go through the local Cinder
// service's /api/rpc bridge, because the settings and the stored key live in
// the service's database. But "does this endpoint answer" is a property of the
// ENDPOINT, not of the service — and the service is often NOT running while
// the user is configuring the endpoint (it comes up with the desktop app).
//
// Same contract as the server's ListAIModels / TestAIChat (internal/service/
// ai.go) and the VS Code panel's aiProbe.ts, so every surface diagnoses an
// endpoint identically: same candidate URLs, same 404-means-try-next rule,
// same payload shapes, same hints.
package dev.repotory.jetbrains

import com.google.gson.JsonParser
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration

/** How long a probe may wait: a model list is fast, a completion is not. */
const val PROBE_TIMEOUT_MS = 30_000L

data class ProbeModelsResult(val models: List<String>, val resolvedBaseUrl: String)

data class ProbeChatResult(val reply: String, val resolvedBaseUrl: String)

/** The near-miss base shapes worth trying, mirroring aiCandidateURLs. */
fun aiCandidateUrls(rawBase: String, path: String): List<String> {
    val base = rawBase.trim().trimEnd('/')
    val urls = mutableListOf(base + path)
    if (!base.endsWith("/v1")) urls.add(base + "/v1" + path)
    if (base.endsWith("/api/v1")) urls.add(base.removeSuffix("/api/v1") + "/v1" + path)
    if (base.endsWith("/api")) urls.add(base.removeSuffix("/api") + path)
    return urls.distinct()
}

/**
 * Parse a /models body: the OpenAI shape ({"data":[{"id"}]}) first, then
 * Ollama's native ({"models":[{"name"}]}). Sorted like the server sorts them.
 */
fun parseProbeModels(raw: String): List<String> {
    try {
        val obj = JsonParser.parseString(raw).asJsonObject
        val ids = mutableListOf<String>()
        val data = obj.getAsJsonArray("data")
        if (data != null) {
            for (m in data) m.asJsonObject.get("id")?.takeIf { it.isJsonPrimitive }?.let { ids.add(it.asString) }
        }
        if (ids.isEmpty()) {
            val models = obj.getAsJsonArray("models")
            if (models != null) {
                for (m in models) m.asJsonObject.get("name")?.takeIf { it.isJsonPrimitive }?.let { ids.add(it.asString) }
            }
        }
        return ids.distinct().sorted()
    } catch (_: Exception) {
        return emptyList()
    }
}

private class ProviderProbeException(message: String) : RuntimeException(message)

private fun networkHint(error: Throwable): String {
    val msg = error.message ?: error.javaClass.simpleName
    return when {
        "ECONNREFUSED" in msg || "fetch failed" in msg || "NetworkError" in msg ->
            "nothing is listening on that host:port — is the server running, and is the port right? ($msg)"
        "abort" in msg || "timed out" in msg -> "connection timed out"
        "ENOTFOUND" in msg || "getaddrinfo" in msg -> "host not found — check the address"
        else -> "AI endpoint unreachable: $msg"
    }
}

private fun authHeaders(apiKey: String): Map<String, String> {
    val key = apiKey.trim()
    return if (key.isEmpty()) emptyMap() else mapOf("Authorization" to "Bearer $key")
}

private fun truncated(text: String, max: Int): String =
    if (text.length <= max) text else text.substring(0, max)

private fun HttpRequest.Builder.withAuth(headers: Map<String, String>): HttpRequest.Builder =
    headers.forEach { (name, value) -> header(name, value) }.let { this }

/**
 * Probe the endpoint's /models with the typed values — no service, no save.
 *
 * Only a 404 moves to the next candidate (a wrong path); an auth failure or
 * any other status is a real answer about the endpoint and must not be
 * retried against a made-up sibling URL — the same rule the server applies.
 */
fun probeModels(rawBase: String, apiKey: String, client: HttpClient = HttpClient.newHttpClient()): ProbeModelsResult {
    val base = rawBase.trim()
    require(base.isNotEmpty()) { "base URL is required" }
    val candidates = aiCandidateUrls(base, "/models")
    val headers = authHeaders(apiKey)

    var lastNet: Throwable? = null
    var lastStatus = 0
    var lastBody = ""
    for (url in candidates) {
        val req = HttpRequest.newBuilder(URI.create(url))
            .timeout(Duration.ofMillis(PROBE_TIMEOUT_MS))
            .GET()
            .withAuth(headers)
            .build()
        try {
            val res = client.send(req, HttpResponse.BodyHandlers.ofString())
            when (res.statusCode()) {
                404 -> { lastStatus = 404; lastBody = res.body() }
                401, 403 -> throw ProviderProbeException("auth failed (${res.statusCode()}) — check the API key")
                200 -> {
                    val models = parseProbeModels(res.body())
                    if (models.isEmpty()) throw ProviderProbeException("endpoint returned no models")
                    return ProbeModelsResult(models, url.removeSuffix("/models"))
                }
                else -> throw ProviderProbeException("AI endpoint returned ${res.statusCode()}: ${truncated(res.body(), 200)}")
            }
        } catch (err: ProviderProbeException) {
            throw err
        } catch (err: Exception) {
            lastNet = err
        }
    }
    lastNet?.let { throw ProviderProbeException(networkHint(it)) }
    throw ProviderProbeException(
        "no /models path found — tried: ${candidates.joinToString(", ")} (last status $lastStatus: ${truncated(lastBody, 120)})",
    )
}

/**
 * Verify a specific model answers a minimal completion — the direct twin of
 * TestAIChat. A 200 body can still carry an error envelope instead of a
 * completion (OpenRouter does this when the upstream provider is saturated),
 * so the provider's own words are reported rather than a bare "no choices".
 */
fun probeChat(rawBase: String, model: String, apiKey: String, client: HttpClient = HttpClient.newHttpClient()): ProbeChatResult {
    val base = rawBase.trim()
    require(base.isNotEmpty()) { "base URL is required" }
    require(model.trim().isNotEmpty()) { "model is required" }
    val candidates = aiCandidateUrls(base, "/chat/completions")
    val body = """{"model":"${model.trim()}","messages":[{"role":"user","content":"Reply with exactly: OK"}]}"""

    var lastNet: Throwable? = null
    var lastStatus = 0
    var lastBody = ""
    for (url in candidates) {
        val req = HttpRequest.newBuilder(URI.create(url))
            .timeout(Duration.ofMillis(PROBE_TIMEOUT_MS))
            .header("Content-Type", "application/json")
            .withAuth(authHeaders(apiKey))
            .POST(HttpRequest.BodyPublishers.ofString(body))
            .build()
        try {
            val res = client.send(req, HttpResponse.BodyHandlers.ofString())
            when (res.statusCode()) {
                404 -> { lastStatus = 404; lastBody = res.body() }
                401, 403 -> throw ProviderProbeException("auth failed (${res.statusCode()}) — check the API key")
                200 -> {
                    val obj = JsonParser.parseString(res.body()).asJsonObject
                    val choices = obj.getAsJsonArray("choices")
                    if (choices == null || choices.isEmpty()) {
                        // The provider's own words beat a bare "no choices".
                        val envelope = obj.getAsJsonObject("error")
                        val msg = envelope?.get("message")?.takeIf { it.isJsonPrimitive }?.asString
                        throw ProviderProbeException(if (!msg.isNullOrBlank()) msg else "AI endpoint returned no choices")
                    }
                    val content = choices[0].asJsonObject.getAsJsonObject("message").get("content")
                    return ProbeChatResult(
                        reply = (content?.takeIf { it.isJsonPrimitive }?.asString ?: "").trim(),
                        resolvedBaseUrl = url.removeSuffix("/chat/completions"),
                    )
                }
                else -> throw ProviderProbeException("AI endpoint returned ${res.statusCode()}: ${truncated(res.body(), 300)}")
            }
        } catch (err: ProviderProbeException) {
            throw err
        } catch (err: Exception) {
            lastNet = err
        }
    }
    lastNet?.let { throw ProviderProbeException(networkHint(it)) }
    throw ProviderProbeException(
        "no working path found — tried: ${candidates.joinToString(", ")} (last status $lastStatus: ${truncated(lastBody, 120)})",
    )
}
