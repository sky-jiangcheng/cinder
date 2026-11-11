package dev.repotory.jetbrains

import com.google.gson.JsonElement
import com.google.gson.JsonObject
import java.util.UUID
import java.util.concurrent.CancellationException

// The server's read window (`service.chatMessageLimit`). The three clients must
// show the same transcript for one thread: Warden and VS Code replace their list
// with the latest N, so accumulating every message ever seen here would both
// grow without bound and display turns the store deliberately aged out.
private const val HISTORY_WINDOW = 500

/** Shared storage over the existing MCP transport; no Warden provider or local database. */
internal class SharedChatSession(
    private val transport: ChatTransport,
    private val changed: () -> Unit,
) : AutoCloseable {
    private val lock = Any()
    private var disposed = false
    private var visible = false
    private var refreshing = false
    private var refreshAgain = false
    private var generation = 0L
    private var revision = 0L
    private var threads = emptyList<SharedThread>()
    private var selectedId: String? = null
    private val histories = mutableMapOf<String, List<SharedMessage>>()
    private val pending = linkedMapOf<String, SharedPending>()
    private val errors = mutableMapOf<String?, String>()
    private var syncError = ""
    private var synced = false

    fun snapshot(): SharedChatState = synchronized(lock) {
        SharedChatState(
            threads = threads.toList(),
            selectedId = selectedId,
            messages = histories[selectedId].orEmpty().toList(),
            pending = pending.values.filter { it.threadId == selectedId },
            syncError = syncError,
            operationError = listOfNotNull(errors[null], selectedId?.let { errors[it] }).joinToString("\n"),
            synced = synced,
        )
    }

    fun setVisible(value: Boolean) = synchronized(lock) {
        if (!disposed && visible != value) {
            visible = value
            generation++
            if (value) refreshAgain = true
        }
    }

    fun select(id: String?) = synchronized(lock) {
        if (disposed || id == selectedId || (id != null && threads.none { it.id == id })) return
        selectedId = id
        generation++
        synced = false
        syncError = ""
        refreshAgain = true
        changed()
    }

    /** Called off the EDT. Concurrent ticks collapse into one follow-up read. */
    fun refresh() {
        synchronized(lock) {
            if (disposed || !visible) return
            if (refreshing) {
                refreshAgain = true
                return
            }
            refreshing = true
        }
        try {
            do {
                val request = synchronized(lock) {
                    if (disposed || !visible) return
                    refreshAgain = false
                    Triple(generation, selectedId, revision)
                }
                try {
                    val listed = chat("list").array("threads").map { it.thread() }
                    if (!current(request.first)) continue
                    val read = request.second?.let { id ->
                        val result = chat("read", mapOf("thread_id" to id))
                        val thread = result.get("thread").thread()
                        check(thread.id == id) { "The service returned a different conversation." }
                        thread to result.array("messages").map { it.message() }
                    }
                    synchronized(lock) {
                        if (!current(request.first)) return@synchronized
                        // A read started before a rename/create must not undo that write.
                        if (revision == request.third) {
                            threads = listed
                            read?.first?.let { remember(it) }
                        }
                        read?.let { merge(it.first.id, it.second) }
                        syncError = ""
                        synced = true
                        changed()
                    }
                } catch (e: Exception) {
                    synchronized(lock) {
                        if (current(request.first)) {
                            syncError = "Not synced — ${reason(e)}. Check or upgrade the Cinder service; " +
                                "the shared-conversation tool is required. Cached history is kept."
                            synced = false
                            changed()
                        }
                    }
                }
            } while (synchronized(lock) { refreshAgain && visible && !disposed })
        } finally {
            synchronized(lock) { refreshing = false }
        }
    }

    /** The only way to create an empty thread: an explicit New conversation action. */
    fun create(title: String) {
        if (title.isBlank()) return
        val before = synchronized(lock) { if (disposed) return; generation }
        try {
            val id = UUID.randomUUID().toString()
            val thread = chat("create", mapOf("id" to id, "title" to title.trim(),
                "path" to transport.workspacePath().orEmpty())).get("thread").thread()
            check(thread.id == id) { "The service returned a different conversation." }
            synchronized(lock) {
                if (disposed) return
                remember(thread)
                revision++
                if (generation == before) {
                    selectedId = id
                    generation++
                }
                errors.remove(null)
                synced = false
                changed()
            }
            refresh()
        } catch (e: Exception) {
            operationFailed(null, e)
        }
    }

    /**
     * Rename a shared thread. ADR-0051 removed the Rename BUTTON from the
     * tool window, not the session capability: `repotory_chat` still serves
     * the `rename` operation, and the concurrency regressions exercise stale
     * reads and per-thread error attribution through this entry point.
     */
    fun rename(id: String, title: String) {
        if (title.isBlank()) return
        try {
            val thread = chat("rename", mapOf("thread_id" to id, "title" to title.trim())).get("thread").thread()
            check(thread.id == id) { "The service returned a different conversation." }
            synchronized(lock) {
                if (disposed) return
                remember(thread)
                revision++
                errors.remove(id)
                changed()
            }
        } catch (e: Exception) {
            operationFailed(id, e)
        }
    }

    /** Capture the destination on the UI thread, BEFORE queuing network work. */
    fun prepareSend(question: String): SharedPending? = synchronized(lock) {
        if (disposed || question.isBlank()) return null
        val thread = threads.firstOrNull { it.id == selectedId } ?: return null
        if (pending.values.any { it.threadId == thread.id && it.busy }) return null
        val job = SharedPending(thread.id, UUID.randomUUID().toString(), question.trim(), thread.path)
        pending[job.userId] = job
        changed()
        job
    }

    fun prepareRetry(userId: String): SharedPending? = synchronized(lock) {
        if (disposed) return null
        val old = pending[userId] ?: return null
        if (old.threadId != selectedId || pending.values.any { it.threadId == old.threadId && it.busy }) return null
        old.copy(error = "", busy = true).also { pending[userId] = it; changed() }
    }

    /** Append user -> ask knowledge base -> append reply. All writes use captured immutable ids. */
    fun send(job: SharedPending) {
        try {
            append(job, job.userId, "user", job.question, null)
            val answer = job.answer ?: transportAsk(job)
            synchronized(lock) {
                if (disposed) return
                pending[job.userId] = job.copy(answer = answer)
                changed()
            }
            append(job, job.userId + "-reply", "assistant", answer, job.userId)
            synchronized(lock) {
                if (disposed) return
                pending.remove(job.userId)
                changed()
            }
        } catch (e: Exception) {
            synchronized(lock) {
                if (disposed) return
                val retained = pending[job.userId] ?: job
                pending[job.userId] = retained.copy(busy = false, error =
                    "Not synced / request failed: ${aiFailureHint(reason(e))}. Retry keeps the same message ids; " +
                        "upgrade Cinder if the shared-conversation tool is unavailable.")
                changed()
            }
        }
    }

    private fun transportAsk(job: SharedPending): String {
        checkAlive()
        val args = mutableMapOf<String, Any?>("query" to job.question)
        // Ask resolves the selected conversation's path, not whichever IDE project is now open.
        if (job.path.isNotBlank()) args["path"] = job.path
        val answer = transport.callTool(ChatMode.ASK.tool, args)
        checkAskResponse(answer)
        check(answer.isNotBlank()) { EMPTY_ANSWER }
        return answer // Preserve sources and keyword-fallback wording across clients.
    }

    private fun append(job: SharedPending, id: String, role: String, content: String, replyTo: String?) {
        val args = mutableMapOf<String, Any?>("thread_id" to job.threadId, "id" to id,
            "role" to role, "content" to content, "source" to "jetbrains")
        if (replyTo != null) args["reply_to"] = replyTo
        val message = chat("append", args).get("message").message()
        check(message.id == id && message.role == role && message.content == content &&
            message.replyTo == replyTo && message.source == "jetbrains") { "Immutable message acknowledgement differs." }
        synchronized(lock) {
            if (!disposed) {
                merge(job.threadId, listOf(message))
                changed()
            }
        }
    }

    private fun chat(operation: String, args: Map<String, Any?> = emptyMap()): JsonObject {
        checkAlive()
        return McpPayloads.parseObject("repotory_chat", transport.callTool("repotory_chat", args + ("operation" to operation)))
    }

    private fun checkAlive() = synchronized(lock) { if (disposed) throw CancellationException("Panel closed") }
    private fun current(token: Long): Boolean = synchronized(lock) { !disposed && visible && generation == token }
    private fun remember(thread: SharedThread) { threads = threads.filterNot { it.id == thread.id } + thread }
    private fun merge(id: String, messages: List<SharedMessage>) {
        // Messages are immutable; merging also prevents a stale read from losing a concurrent append.
        histories[id] = (histories[id].orEmpty() + messages).distinctBy { it.id }
            .takeLast(HISTORY_WINDOW)
    }
    private fun operationFailed(id: String?, e: Exception) = synchronized(lock) {
        if (!disposed) {
            val operation = if (id == null) "Create conversation" else "Rename conversation"
            errors[id] = "$operation not synced — ${reason(e)}. Check or upgrade the Cinder service."
            changed()
        }
    }
    override fun close(): Unit = synchronized(lock) {
        disposed = true
        visible = false
        refreshAgain = false
        generation++
    }
}

private fun reason(e: Exception): String = e.message?.takeIf { it.isNotBlank() } ?: e.javaClass.simpleName
private fun JsonObject.array(name: String): List<JsonElement> {
    val value = get(name)
    check(value != null && value.isJsonArray) { "Invalid shared conversation field: $name" }
    return value.asJsonArray.toList()
}
private fun JsonObject.string(name: String): String {
    val value = get(name)
    check(value != null && value.isJsonPrimitive && value.asJsonPrimitive.isString) {
        "Invalid shared conversation field: $name"
    }
    return value.asString
}
private fun JsonElement?.thread(): SharedThread {
    check(this != null && isJsonObject) { "Invalid shared conversation" }
    val obj = asJsonObject
    return SharedThread(obj.string("id").also { check(it.isNotBlank()) }, obj.string("title"),
        obj.string("path"), obj.string("createdAt"), obj.string("updatedAt"))
}
private fun JsonElement?.message(): SharedMessage {
    check(this != null && isJsonObject) { "Invalid shared message" }
    val obj = asJsonObject
    val reply = obj.get("replyTo") ?: error("Missing replyTo")
    return SharedMessage(obj.string("id").also { check(it.isNotBlank()) },
        obj.string("role").also { check(it == "user" || it == "assistant") }, obj.string("content"),
        obj.string("source"), if (reply.isJsonNull) null else obj.string("replyTo"), obj.string("createdAt"))
}

/** Per-panel, visibility-bound polling. The host supplies an EDT one-shot scheduler. */
internal class SharedChatPolling(
    private val session: SharedChatSession,
    private val execute: (() -> Unit) -> Unit,
    private val onUi: (() -> Unit) -> Unit,
    private val schedule: (Int, () -> Unit) -> (() -> Unit),
) : AutoCloseable {
    private var active = false
    private var closed = false
    private var inFlight = false
    private var refreshRequested = false
    private var cancel: (() -> Unit)? = null

    fun setVisible(visible: Boolean) {
        if (closed || active == visible) return
        active = visible
        session.setVisible(visible)
        cancel?.invoke()
        cancel = null
        if (active) refreshNow()
    }

    fun refreshNow() {
        if (closed || !active) return
        cancel?.invoke()
        cancel = null
        if (inFlight) {
            refreshRequested = true
            return
        }
        inFlight = true
        refreshRequested = false
        execute {
            try { session.refresh() } finally {
                onUi {
                    inFlight = false
                    if (active && !closed) {
                        if (refreshRequested) refreshNow()
                        else cancel = schedule(3000) { cancel = null; refreshNow() }
                    }
                }
            }
        }
    }

    override fun close() {
        closed = true
        active = false
        cancel?.invoke()
        cancel = null
        session.close()
    }
}
