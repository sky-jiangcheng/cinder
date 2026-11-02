// Repotory for JetBrains — the chat console's transport.
//
// The port the conversation model talks to (ChatSession.kt). It is the ONE
// place where the two channels meet:
//
//   · MCP carries the data (ADR-0035 decision 2): ask, notes, pages, and the
//     reads behind an opened card.
//   · HTTP carries the ONE binding MCP does not have: ResolveWorkspacePath,
//     which answers "which project is this folder" with the server's own
//     matcher, so the panel and an agent can never disagree about it.
//
// The workspace path is the IDE project's own directory: the panel locks
// itself to the project the user is standing in (ADR-0036 decision 2).
package dev.repotory.jetbrains

import com.google.gson.JsonElement
import com.google.gson.JsonObject
import com.intellij.openapi.project.Project

/** The plugin's implementation of the chat console's transport port. */
internal class McpChatTransport(private val project: Project?) : ChatTransport {

    override fun callTool(name: String, args: Map<String, Any?>): String =
        repotoryMcp.callToolText(name, args)

    override fun rpc(method: String, args: List<Any?>): Map<String, Any?>? {
        val result = repotoryClient.rpc(RepotorySettings.serverUrl(), method, args) ?: return null
        return toMap(result) ?: return null
    }

    override fun serverUrl(): String = RepotorySettings.serverUrl()

    /**
     * The folder the panel stands in: the IDE project's own directory.
     *
     * Null when the project has no directory on disk (a scratch project, the
     * default project) — then there is nothing to resolve, and the panel says
     * so instead of pretending it is scoped.
     */
    override fun workspacePath(): String? = project?.basePath?.takeIf { it.isNotBlank() }
}

/** A JSON object as a plain map, so the session needs no JSON library. */
private fun toMap(element: Any?): Map<String, Any?>? {
    val obj = element as? JsonObject ?: return null
    val out = LinkedHashMap<String, Any?>()
    for (entry in obj.entrySet()) {
        out[entry.key] = toPlain(entry.value)
    }
    return out
}

/** A JSON value as a plain Kotlin value. */
private fun toPlain(element: JsonElement?): Any? = when {
    element == null || element.isJsonNull -> null
    element.isJsonPrimitive -> {
        val primitive = element.asJsonPrimitive
        when {
            primitive.isBoolean -> primitive.asBoolean
            primitive.isNumber -> {
                // Integral numbers stay Long: a project id must not lose its
                // last digits on the way through a Double.
                val value = primitive.asDouble
                if (!value.isInfinite() && value == Math.floor(value)) value.toLong() else value
            }
            else -> primitive.asString
        }
    }
    element.isJsonObject -> toMap(element)
    element.isJsonArray -> element.asJsonArray.map { toPlain(it) }
    else -> null
}
