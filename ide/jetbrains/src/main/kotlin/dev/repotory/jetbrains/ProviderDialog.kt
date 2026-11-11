// The AI provider dialog — the IDE's entry for the model endpoint.
//
// Why it exists: the panel answers through `repotory_ask`, which fails with
// "AI endpoint returned 404" when no endpoint is configured or when the
// configured Base URL is shaped like a full endpoint. Until this dialog the
// only way out of that error was the desktop app's settings page, so the error
// was a dead end in the IDE. The dialog reads and writes the SAME settings rows
// through the same /api/rpc bridge the desktop UI uses — the plugin keeps no
// second copy, so both clients always agree on which endpoint answers.
//
// The stored API key is never echoed to a client (GetConfig masks it), so
// "Test" uses the server-side `TestAIChatConfigured` when the key field is
// left blank and a key is already saved.
package dev.repotory.jetbrains

import com.google.gson.JsonObject
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.ui.DialogWrapper
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import javax.swing.JButton
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.JPasswordField

class ProviderDialog : DialogWrapper(true) {

    private val baseField = JBTextField(30)
    private val modelField = JBTextField(24)
    private val keyField = JPasswordField(24)
    private val savedLabel = JBLabel(" ")
    private val status = JBLabel(" ")

    /** True when a key is already stored server-side (the value never arrives). */
    private var hasKey = false

    init {
        title = "Cinder: AI provider"
        init()
        loadProvider()
    }

    override fun createCenterPanel(): JComponent {
        val fetch = JButton("Fetch models").apply { addActionListener { fetchModels() } }
        val test = JButton("Test").apply { addActionListener { testProvider() } }
        val buttons = JPanel(BorderLayout(JBUI.scale(6), 0))
        buttons.add(fetch, BorderLayout.WEST)
        buttons.add(test, BorderLayout.CENTER)

        return FormBuilder.createFormBuilder()
            .addComponent(
                JBLabel(
                    "<html>Any OpenAI-compatible endpoint: OpenRouter, OpenAI, DeepSeek, vLLM, " +
                        "LM Studio, Ollama. Answers are written by this endpoint; the key stays on this machine. " +
                        "Fetching models and testing the typed values connect directly; saving — and testing " +
                        "the saved key — need the local Cinder service (start the desktop app).</html>",
                ),
            )
            .addLabeledComponent(JBLabel("Base URL:"), baseField, 1, false)
            .addLabeledComponent(JBLabel("Model:"), modelField, 1, false)
            .addLabeledComponent(JBLabel("API key:"), keyField, 1, false)
            .addComponent(savedLabel)
            .addComponent(buttons)
            .addComponent(status)
            .panel
    }

    override fun getPreferredFocusedComponent(): JComponent = baseField

    // ------------------------------------------------------------------ load

    private fun loadProvider() {
        status.text = "Reading the saved provider…"
        val url = RepotorySettings.serverUrl()
        onPooled {
            val outcome = runCatching { readConfig(url) }
            onEdt {
                outcome.onSuccess { saved ->
                    baseField.text = saved.baseUrl
                    modelField.text = saved.model
                    hasKey = saved.hasKey
                    keyField.text = ""
                    savedLabel.text = if (saved.hasKey) {
                        "A key is saved. Leave the field blank to keep it."
                    } else {
                        "No key saved (local endpoints often need none)."
                    }
                    status.text = if (saved.baseUrl.isBlank() && saved.model.isBlank()) {
                        "No provider configured yet — answers fail until one is saved."
                    } else {
                        "Configured. Answers use this endpoint."
                    }
                }.onFailure {
                    status.text = "Could not read the provider: ${serviceOfflineHint(it.message)}"
                }
            }
        }
    }

    private data class SavedProvider(val baseUrl: String, val model: String, val hasKey: Boolean)

    private fun readConfig(url: String): SavedProvider {
        val result = repotoryClient.rpc(url, "GetConfig", emptyList()) as? JsonObject
        val config = result?.getAsJsonObject("config")
        fun str(key: String): String =
            config?.get(key)?.takeIf { it.isJsonPrimitive }?.asString.orEmpty()
        return SavedProvider(
            baseUrl = str("ai_chat_base_url"),
            model = str("ai_chat_model"),
            hasKey = str("ai_chat_api_key") == "********",
        )
    }

    // ------------------------------------------------------------------ test

    private fun testProvider() {
        val base = baseField.text.trim()
        val model = modelField.text.trim()
        val key = String(keyField.password).trim()
        if (base.isEmpty() || model.isEmpty()) {
            status.text = "Base URL and model are both required."
            return
        }
        status.text = "Testing…"
        val url = RepotorySettings.serverUrl()
        onPooled {
            val outcome = runCatching {
                // When the form matches the SAVED provider, "test" can also mean
                // "test what is stored" — but that goes through the local service,
                // because the stored key never leaves the host. Every other case
                // probes the endpoint DIRECTLY, so it works while the desktop
                // app — and with it the service — is not running.
                val saved = if (key.isEmpty()) readConfig(url) else null
                val testSaved = saved != null && saved.hasKey && saved.baseUrl == base && saved.model == model
                if (testSaved) {
                    val obj = repotoryClient.rpc(url, "TestAIChatConfigured", emptyList(), RPC_TIMEOUT_MS) as? JsonObject
                    obj?.get("resolved_base_url")?.takeIf { it.isJsonPrimitive }?.asString?.also { baseField.text = it }
                    obj?.get("reply")?.takeIf { it.isJsonPrimitive }?.asString.orEmpty().trim()
                } else {
                    val probe = probeChat(base, model, key)
                    if (probe.resolvedBaseUrl.isNotEmpty()) baseField.text = probe.resolvedBaseUrl
                    probe.reply
                }
            }
            onEdt {
                outcome.onSuccess { reply ->
                    status.text = if (reply.isBlank()) "Endpoint answered." else "Endpoint answered: ${reply.take(80)}"
                }.onFailure { err ->
                    val msg = err.message ?: err.javaClass.simpleName
                    val hint = if (key.isEmpty() && msg.contains("auth failed", ignoreCase = true)) {
                        "$msg. This endpoint requires a key — type it above, or start the Cinder desktop app to test with the saved one."
                    } else if (msg.contains("ECONNREFUSED", ignoreCase = true)) {
                        // The service is down: the RPC path (test the saved
                        // provider) cannot run, the direct one could not be taken.
                        serviceOfflineHint(msg)
                    } else aiFailureHint(msg)
                    status.text = "Test failed: $hint"
                }
            }
        }
    }

    private fun fetchModels() {
        val base = baseField.text.trim()
        val key = String(keyField.password).trim()
        if (base.isEmpty()) {
            status.text = "A Base URL is required to fetch models."
            return
        }
        status.text = "Fetching models…"
        onPooled {
            val outcome = runCatching {
                // DIRECT probe: the model list is the endpoint's own, so the
                // IDE asks the endpoint — the local service does not need to
                // be running while the user is setting this up.
                val probe = probeModels(base, key)
                if (probe.resolvedBaseUrl.isNotEmpty()) baseField.text = probe.resolvedBaseUrl
                probe.models
            }
            onEdt {
                outcome.onSuccess { models ->
                    if (models.isEmpty()) {
                        status.text = "The endpoint reported no models."
                    } else {
                        if (modelField.text.isBlank()) modelField.text = models.first()
                        status.text = "${models.size} model(s): " + models.take(10).joinToString(", ") +
                            if (models.size > 10) "…" else ""
                    }
                }.onFailure { err -> status.text = "Fetch failed: ${aiFailureHint(err.message)}" }
            }
        }
    }

    // ------------------------------------------------------------------ save

    override fun doOKAction() {
        val base = baseField.text.trim()
        val model = modelField.text.trim()
        val key = String(keyField.password).trim()
        if (base.isEmpty() || model.isEmpty()) {
            status.text = "Base URL and model are both required."
            return
        }
        val url = RepotorySettings.serverUrl()
        onPooled {
            val outcome = runCatching {
                repotoryClient.rpc(url, "UpdateConfig", listOf("ai_chat_base_url", base))
                repotoryClient.rpc(url, "UpdateConfig", listOf("ai_chat_model", model))
                // A blank key means "keep what is stored": writing the mask back
                // would replace the real credential with asterisks.
                if (key.isNotEmpty() && key != "********") {
                    repotoryClient.rpc(url, "UpdateConfig", listOf("ai_chat_api_key", key))
                }
            }
            onEdt {
                outcome.onSuccess { close(OK_EXIT_CODE) }
                    .onFailure { status.text = "Save failed: ${serviceOfflineHint(it.message)}" }
            }
        }
    }

    // ------------------------------------------------------------- plumbing

    private fun onPooled(work: () -> Unit) {
        ApplicationManager.getApplication().executeOnPooledThread { work() }
    }

    private fun onEdt(work: () -> Unit) {
        ApplicationManager.getApplication().invokeLater { if (!isDisposed) work() }
    }

    /** How long a saved-provider RPC probe may take: a model call, not a ping. */
    private companion object {
        const val RPC_TIMEOUT_MS = 120_000
    }
}

/**
 * A failure of an RPC against the LOCAL service, told in terms of what to do.
 *
 * The settings and the stored key live in the service's database, so reading
 * and saving the provider go through it — and when it is down (the desktop
 * app is closed, which is exactly when people fill in forms) a raw
 * "connect ECONNREFUSED 127.0.0.1:18765" reads like the endpoint is broken.
 * The message names the real cause and what still works without it.
 */
internal fun serviceOfflineHint(message: String?): String {
    val text = message?.takeIf { it.isNotBlank() } ?: "unknown error"
    return if (Regex("ECONNREFUSED|timeout|invalid JSON|socket hang up|ECONNRESET", RegexOption.IGNORE_CASE)
        .containsMatchIn(text)
    ) {
        "$text — the local Cinder service is not reachable. Start the Cinder desktop app, then retry: reading and saving the provider settings go through it. Fetching models and testing the typed values connect DIRECTLY and do not need it."
    } else {
        aiFailureHint(text)
    }
}

/**
 * A model-endpoint failure, with the sentence that names the usual cause.
 *
 * A 404 from an OpenAI-compatible gateway is almost always a Base URL shaped
 * like a full endpoint ("…/v1/chat/completions") instead of the server root;
 * the raw "Not Found" says nothing about that. Same wording as the VS Code
 * panel's hint, so both IDEs diagnose the same failure identically.
 */
internal fun aiFailureHint(message: String?): String {
    val text = message?.takeIf { it.isNotBlank() } ?: "unknown error"
    return when {
        text.contains("404") ->
            "$text — a 404 usually means the Base URL points at a full endpoint. Use the server root, e.g. https://api.deepseek.com or http://localhost:1234/v1."
        text.contains("No AI endpoint", ignoreCase = true) || text.contains("not configured", ignoreCase = true) ->
            "$text — fill in Base URL and model, then Save."
        text.contains("auth failed", ignoreCase = true) || text.contains("401") || text.contains("403") ->
            "$text — check the API key for this endpoint."
        else -> text
    }
}
