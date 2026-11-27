// AI Provider settings page — the model endpoint that answers.
//
// It used to be a `DialogWrapper` opened from the panel header (ADR-0044
// decision 5). It is a settings page since 2026-11-26: the endpoint, the key
// and the model are configuration, and configuration has ONE home — the same
// settings tree that carries the MCP binary and the server address. The panel
// header's gear opens that tree, so the route did not get longer for the
// reader; it stopped being a second, parallel place to change a setting.
//
// What did NOT change (ADR-0044 decision 5's substance): the page reads and
// writes the SAME `ai_chat_*` rows the desktop app uses, through the same
// MCP config tools. The plugin keeps no second copy, so both clients always
// agree on which endpoint answers, and there is no AI key in the IDE's own
// settings.
//
// The stored API key is never echoed to a client (GetConfig masks it), so a
// blank key field means "keep the stored key", and "Test" uses the
// server-side `TestAIChatConfigured` in that case.
package dev.repotory.jetbrains

import com.google.gson.JsonObject
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.options.Configurable
import com.intellij.openapi.progress.ProgressManager
import com.intellij.openapi.ui.Messages
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import com.intellij.util.ui.JBUI
import java.awt.BorderLayout
import java.awt.FlowLayout
import javax.swing.JButton
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.JPasswordField

/**
 * The settings-tree node: `Settings ▸ Tools ▸ Cinder ▸ AI Provider`.
 *
 * It is a thin `Configurable` over [ProviderPage] — the page owns the form
 * and the network calls; this class only answers the three questions the
 * platform asks (is it modified, apply, reset).
 */
class RepotoryProviderConfigurable : Configurable {

    private var page: ProviderPage? = null

    override fun getDisplayName(): String = "AI Provider"

    override fun createComponent(): JComponent {
        val created = ProviderPage()
        page = created
        return created
    }

    override fun isModified(): Boolean = page?.isModified() ?: false

    override fun apply() {
        page?.apply()
    }

    override fun reset() {
        page?.reload()
    }

    override fun disposeUIResources() {
        page = null
    }
}

/**
 * The provider form.
 *
 * Three fields, two probes and one status line — the same set the VS Code
 * panel's provider sheet draws, so the two IDEs configure the same endpoint
 * with the same words (ADR-0044, ADR-0036 decision 8).
 */
internal class ProviderPage : JPanel(BorderLayout()) {

    private val baseField = JBTextField(34)
    private val modelField = JBTextField(24)
    private val keyField = JPasswordField(24)
    private val savedLabel = JBLabel(" ")
    private val status = JBLabel(" ")

    /** True when a key is already stored server-side (the value never arrives). */
    private var hasKey = false

    /** What the page last read or wrote; `isModified` is a diff against it. */
    private var loaded: SavedProvider? = null

    init {
        isOpaque = false
        border = JBUI.Borders.empty(0, 2, 8, 2)

        val fetch = JButton("Fetch models").apply { addActionListener { fetchModels() } }
        val test = JButton("Test").apply { addActionListener { testProvider() } }
        val buttons = JPanel(FlowLayout(FlowLayout.LEFT, 6, 0))
        buttons.isOpaque = false
        buttons.add(fetch)
        buttons.add(test)

        val modelRow = JPanel(BorderLayout(6, 0))
        modelRow.add(modelField, BorderLayout.CENTER)

        add(
            FormBuilder.createFormBuilder()
                .addComponent(
                    JBLabel(
                        "<html><body style='width:520px'>Any OpenAI-compatible endpoint: OpenRouter, OpenAI, " +
                            "DeepSeek, vLLM, LM Studio, Ollama. Answers are written by this endpoint; the key " +
                            "stays on this machine. Fetching models and testing typed values connect directly — " +
                            "saving, and testing the saved key, need the local Cinder service.</body></html>",
                    ).apply { foreground = PanelPalette.muted },
                )
                .addVerticalGap(JBUI.scale(10))
                .addLabeledComponent(JBLabel("Base URL:"), baseField, 1, false)
                .addLabeledComponent(JBLabel("Model:"), modelRow, 1, false)
                .addLabeledComponent(JBLabel("API key:"), keyField, 1, false)
                .addComponent(savedLabel)
                .addVerticalGap(JBUI.scale(8))
                .addComponent(buttons)
                .addVerticalGap(JBUI.scale(6))
                .addComponent(status)
                .addComponentFillVertically(JPanel(), 0)
                .panel,
            BorderLayout.CENTER,
        )
        reload()
    }

    // -------------------------------------------------------------- modified

    /**
     * True when the form differs from what is stored.
     *
     * A blank key is NOT a change: it means "keep the stored key", which is
     * exactly what Apply will do. Comparing it to the mask would mark the
     * page modified forever, and the IDE would refuse to close Settings
     * without a save nobody asked for.
     */
    fun isModified(): Boolean {
        val stored = loaded ?: return baseField.text.isNotBlank() || modelField.text.isNotBlank()
        val key = String(keyField.password).trim()
        return baseField.text.trim() != stored.baseUrl ||
            modelField.text.trim() != stored.model ||
            (key.isNotEmpty() && key != MASK)
    }

    // ----------------------------------------------------------------- load

    /** Read the stored provider again (called by Reset, and once on open). */
    fun reload() {
        status.text = "Reading the saved provider…"
        onPooled {
            val outcome = runCatching { readConfig() }
            onEdt {
                outcome.onSuccess { saved ->
                    baseField.text = saved.baseUrl
                    modelField.text = saved.model
                    hasKey = saved.hasKey
                    keyField.text = ""
                    loaded = saved
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

    private fun readConfig(): SavedProvider {
        // Over MCP since ADR-0063: the config pair is catalogue-covered, so
        // reading the provider settings needs no running local service.
        val config = McpPayloads.parseConfig(
            RepotoryMcp.TOOL_CONFIG_GET,
            repotoryMcp.callToolText(RepotoryMcp.TOOL_CONFIG_GET, emptyMap()),
        )
        fun str(key: String): String = config[key].orEmpty()
        return SavedProvider(
            baseUrl = str("ai_chat_base_url"),
            model = str("ai_chat_model"),
            hasKey = str("ai_chat_api_key") == MASK,
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
                val saved = if (key.isEmpty()) readConfig() else null
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
                        "$msg. This endpoint requires a key — type it above, or start the local service (Settings ▸ Tools ▸ Cinder ▸ Start local service) so the saved one can be read."
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

    /**
     * Apply: write the three values through the MCP config tool.
     *
     * The write is a process round trip, so it runs in a modal progress task
     * instead of blocking the EDT — the settings dialog stays usable and the
     * reader sees that the save is happening. A failure is a dialog, not a
     * silent no-op: `Apply` returning without doing anything is how a user
     * ends up believing a broken endpoint was saved.
     */
    fun apply() {
        if (!isModified()) return
        val base = baseField.text.trim()
        val model = modelField.text.trim()
        val key = String(keyField.password).trim()
        if (base.isEmpty() || model.isEmpty()) {
            Messages.showErrorDialog(
                "Base URL and model are both required.",
                "Cinder: AI provider not saved",
            )
            return
        }
        var failure: String? = null
        ProgressManager.getInstance().runProcessWithProgressSynchronously(
            {
                try {
                    writeConfig("ai_chat_base_url", base)
                    writeConfig("ai_chat_model", model)
                    // A blank key means "keep what is stored": writing the mask
                    // back would replace the real credential with asterisks.
                    if (key.isNotEmpty() && key != MASK) writeConfig("ai_chat_api_key", key)
                } catch (e: Exception) {
                    failure = serviceOfflineHint(e.message)
                }
            },
            "Saving the Cinder AI provider…",
            false,
            null,
        )
        val error = failure
        if (error != null) {
            status.text = "Save failed: $error"
            Messages.showErrorDialog("Save failed: $error", "Cinder: AI provider not saved")
            return
        }
        hasKey = hasKey || key.isNotEmpty()
        keyField.text = ""
        loaded = SavedProvider(base, model, hasKey)
        savedLabel.text = if (hasKey) {
            "A key is saved. Leave the field blank to keep it."
        } else {
            "No key saved (local endpoints often need none)."
        }
        status.text = "Saved. Answers now use this endpoint."
    }

    /** One config write over MCP; a rejected key surfaces as a failure. */
    private fun writeConfig(key: String, value: String) {
        val text = repotoryMcp.callToolText(
            RepotoryMcp.TOOL_CONFIG_UPDATE,
            mapOf("key" to key, "value" to value),
        )
        if (text.trimStart().startsWith("error: ")) {
            throw McpException(text.trimStart().removePrefix("error: ").trim())
        }
    }

    // ------------------------------------------------------------- plumbing

    private fun onPooled(work: () -> Unit) {
        ApplicationManager.getApplication().executeOnPooledThread { work() }
    }

    private fun onEdt(work: () -> Unit) {
        ApplicationManager.getApplication().invokeLater { if (isDisplayable) work() }
    }

    /** How long a saved-provider RPC probe may take: a model call, not a ping. */
    private companion object {
        const val RPC_TIMEOUT_MS = 120_000
        const val MASK = "********"
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
        "$text — the local Cinder service is not reachable. Start it in Settings ▸ Tools ▸ Cinder (Start local service), then retry: reading and saving the provider settings go through it. Fetching models and testing the typed values connect DIRECTLY and do not need it."
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
