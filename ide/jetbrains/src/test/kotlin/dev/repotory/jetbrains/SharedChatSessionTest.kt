package dev.repotory.jetbrains

import com.google.gson.GsonBuilder
import java.util.UUID
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class SharedChatSessionTest {
    private class Store {
        val threads = linkedMapOf<String, SharedThread>()
        val messages = mutableMapOf<String, LinkedHashMap<String, SharedMessage>>()
        private val gson = GsonBuilder().serializeNulls().create()
        private var clock = 0

        @Synchronized fun call(args: Map<String, Any?>): String {
            fun str(key: String) = args[key] as String
            fun now() = (++clock).toString().padStart(5, '0')
            val payload: Any = when (str("operation")) {
                "list" -> mapOf("threads" to threads.values.toList())
                "create" -> {
                    val id = str("id")
                    val thread = threads.getOrPut(id) { SharedThread(id, str("title"), str("path"), now(), now()) }
                    mapOf("thread" to thread)
                }
                "read" -> mapOf("thread" to threads.getValue(str("thread_id")),
                    "messages" to messages[str("thread_id")]?.values.orEmpty().toList())
                "rename" -> {
                    val id = str("thread_id")
                    val updated = threads.getValue(id).copy(title = str("title"), updatedAt = now())
                    threads[id] = updated
                    mapOf("thread" to updated)
                }
                "append" -> {
                    val id = str("thread_id")
                    val thread = threads.getValue(id)
                    val rows = messages.getOrPut(id) { linkedMapOf() }
                    val incoming = SharedMessage(str("id"), str("role"), str("content"), str("source"),
                        args["reply_to"] as? String, now())
                    val old = rows[incoming.id]
                    check(old == null || old.copy(createdAt = incoming.createdAt) == incoming) { "immutable conflict" }
                    rows.putIfAbsent(incoming.id, incoming)
                    threads[id] = thread.copy(updatedAt = now())
                    mapOf("message" to rows.getValue(incoming.id))
                }
                else -> error("unknown operation")
            }
            return gson.toJson(payload)
        }
    }

    private class Client(private val store: Store, private val path: String = "/workspace/a") : ChatTransport {
        val calls = CopyOnWriteArrayList<Pair<String, Map<String, Any?>>>()
        var before: (String, Map<String, Any?>) -> Unit = { _, _ -> }
        var after: (String, Map<String, Any?>) -> Unit = { _, _ -> }
        var answer = "Knowledge answer.\n\nSources:\n[P1] page \"Evidence\" (id 2)\n"
        var rawRead: String? = null
        override fun callTool(name: String, args: Map<String, Any?>): String {
            calls.add(name to args)
            before(name, args)
            val result = when (name) {
                "repotory_ask" -> answer
                "repotory_chat" -> if (args["operation"] == "read" && rawRead != null) rawRead!! else store.call(args)
                "repotory_notes_search", "repotory_wiki_search" -> "[]"
                else -> error("unknown tool: $name")
            }
            after(name, args)
            return result
        }
        override fun rpc(method: String, args: List<Any?>): Map<String, Any?>? = null
        override fun workspacePath() = path
        override fun serverUrl() = "unused"
    }

    private fun session(client: Client) = SharedChatSession(client) {}.apply { setVisible(true) }
    private fun SharedChatSession.newThread(title: String = "Topic"): String {
        create(title)
        return assertNotNull(snapshot().selectedId)
    }
    private fun SharedChatSession.ask(question: String = "Why?"): SharedPending {
        val job = assertNotNull(prepareSend(question))
        send(job)
        return job
    }

    @Test fun `two clients share list create rename and history across workspace paths`() {
        val store = Store()
        val ideClient = Client(store)
        val wardenClient = Client(store, "/workspace/b")
        val ide = session(ideClient)
        val warden = session(wardenClient)
        ide.refresh()
        assertTrue(ide.snapshot().threads.isEmpty())
        assertNull(ide.prepareSend("Do not implicitly create a thread"))
        assertFalse(ideClient.calls.any { it.second["operation"] == "create" })
        val id = ide.newThread()
        UUID.fromString(id)
        warden.refresh()
        assertEquals(listOf(id), warden.snapshot().threads.map { it.id })
        assertNull(warden.snapshot().selectedId)
        warden.select(id)
        val question = ide.ask()
        UUID.fromString(question.userId)
        warden.refresh()
        assertEquals(ide.snapshot().messages, warden.snapshot().messages)
        assertEquals(listOf("user", "assistant"), warden.snapshot().messages.map { it.role })
        assertEquals(question.userId + "-reply", warden.snapshot().messages.last().id)
        assertEquals(question.userId, warden.snapshot().messages.last().replyTo)
        assertEquals(ideClient.answer, warden.snapshot().messages.last().content)
        assertTrue(warden.snapshot().messages.all { it.source == "jetbrains" })
        assertTrue(wardenClient.calls.filter { it.second["operation"] == "list" }.all { "path" !in it.second })
        warden.rename(id, "<html>Shared title</html>")
        ide.refresh()
        assertEquals("<html>Shared title</html>", ide.snapshot().selected?.title)
        val remote = UUID.randomUUID().toString()
        wardenClient.callTool("repotory_chat", mapOf("operation" to "append", "thread_id" to id,
            "id" to remote, "role" to "user", "content" to "From Warden", "source" to "warden"))
        ide.refresh()
        assertTrue(ide.snapshot().messages.any { it.id == remote && it.source == "warden" })
        val sendCalls = ideClient.calls.filter { it.first == "repotory_ask" || it.second["operation"] == "append" }
        assertEquals(listOf("user", null, "assistant"), sendCalls.map { it.second["role"] })
        assertEquals("/workspace/a", sendCalls[1].second["path"])
    }

    @Test fun `late answer stays in its captured thread even after switching`() {
        val client = Client(Store())
        val snapshots = CopyOnWriteArrayList<SharedChatState>()
        lateinit var chat: SharedChatSession
        chat = SharedChatSession(client) { snapshots.add(chat.snapshot()) }
        chat.setVisible(true)
        val first = chat.newThread("First")
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        client.before = { name, _ -> if (name == "repotory_ask") { entered.countDown(); check(release.await(5, TimeUnit.SECONDS)) } }
        val pool = Executors.newSingleThreadExecutor()
        try {
            val job = assertNotNull(chat.prepareSend("First question"))
            val future = pool.submit { chat.send(job) }
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            val second = chat.newThread("Second")
            release.countDown()
            future.get(5, TimeUnit.SECONDS)
            assertEquals(second, chat.snapshot().selectedId)
            assertTrue(chat.snapshot().messages.isEmpty())
            assertTrue(snapshots.filter { it.selectedId == second }.all { it.messages.isEmpty() && it.pending.isEmpty() })
            chat.select(first)
            chat.refresh()
            assertEquals(2, chat.snapshot().messages.size)
            assertEquals("First question", chat.snapshot().messages.first().content)
        } finally { release.countDown(); pool.shutdownNow() }
    }

    @Test fun `late read cannot paint the previous thread or overlap another refresh`() {
        val client = Client(Store())
        val chat = session(client)
        val first = chat.newThread("First")
        chat.ask()
        val second = chat.newThread("Second")
        chat.select(first)
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        client.before = { _, args ->
            if (args["operation"] == "read" && args["thread_id"] == first) {
                entered.countDown(); check(release.await(5, TimeUnit.SECONDS))
            }
        }
        val pool = Executors.newSingleThreadExecutor()
        try {
            val future = pool.submit { chat.refresh() }
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            val count = client.calls.size
            chat.select(second)
            repeat(5) { chat.refresh() }
            assertEquals(count, client.calls.size)
            release.countDown()
            future.get(5, TimeUnit.SECONDS)
            assertEquals(second, chat.snapshot().selectedId)
            assertTrue(chat.snapshot().messages.isEmpty())
            assertEquals(second, client.calls.last().second["thread_id"])
        } finally { release.countDown(); pool.shutdownNow() }
    }

    @Test fun `replyTo pairing tolerates interleaving reversed order and orphan replies`() {
        fun msg(id: String, role: String, reply: String?, time: String) = SharedMessage(id, role, id, "warden", reply, time)
        val messages = listOf(msg("a2", "assistant", "u2", "0"), msg("u1", "user", null, "1"),
            msg("u2", "user", null, "2"), msg("a1", "assistant", "u1", "3"),
            msg("orphan", "assistant", "missing", "4"), msg("no-parent", "assistant", null, "5"))
        assertEquals(listOf(listOf("u1", "a1"), listOf("u2", "a2"), listOf("orphan"), listOf("no-parent")),
            sharedMessageGroups(messages).map { group -> group.map { it.id } })
    }

    @Test fun `ask failure is retained locally and never appended as an assistant`() {
        val client = Client(Store())
        val chat = session(client)
        chat.newThread()
        client.answer = "  Error: provider rejected request"
        val job = chat.ask()
        assertEquals(listOf("user"), chat.snapshot().messages.map { it.role })
        assertTrue(chat.snapshot().pending.single().error.contains("provider rejected"))
        chat.refresh()
        assertTrue(chat.snapshot().pending.single().error.contains("provider rejected"))
        client.answer = "Recovered answer"
        chat.send(assertNotNull(chat.prepareRetry(job.userId)))
        assertEquals(2, chat.snapshot().messages.size)
        assertEquals(job.userId + "-reply", chat.snapshot().messages.last().id)
        assertTrue(chat.snapshot().pending.isEmpty())
    }

    @Test fun `failed user append keeps draft and does not call ask`() {
        val client = Client(Store())
        val chat = session(client)
        chat.newThread()
        client.before = { _, args -> if (args["operation"] == "append") error("write refused") }
        val job = chat.ask("Keep this question")
        assertTrue(chat.snapshot().messages.isEmpty())
        assertEquals("Keep this question", chat.snapshot().pending.single().question)
        assertFalse(client.calls.any { it.first == "repotory_ask" })
        client.before = { _, _ -> }
        chat.send(assertNotNull(chat.prepareRetry(job.userId)))
        assertEquals(listOf(job.userId, job.userId + "-reply"), chat.snapshot().messages.map { it.id })
    }

    @Test fun `lost assistant acknowledgement retries immutable append without asking again`() {
        val store = Store()
        val client = Client(store)
        val chat = session(client)
        val id = chat.newThread()
        client.after = { _, args -> if (args["role"] == "assistant") error("lost acknowledgement") }
        val job = chat.ask()
        val kept = chat.snapshot().pending.single()
        assertEquals(client.answer, kept.answer)
        assertTrue(kept.error.contains("lost acknowledgement"))
        assertEquals(2, store.messages.getValue(id).size)
        chat.refresh()
        assertEquals(2, chat.snapshot().messages.size)
        assertTrue(chat.snapshot().pending.isNotEmpty())
        client.after = { _, _ -> }
        chat.send(assertNotNull(chat.prepareRetry(job.userId)))
        assertEquals(1, client.calls.count { it.first == "repotory_ask" })
        assertEquals(2, store.messages.getValue(id).size)
        assertTrue(chat.snapshot().pending.isEmpty())
    }

    @Test fun `missing shared tool keeps history and does not break local Notes Pages or Ask`() {
        val client = Client(Store())
        val chat = session(client)
        chat.newThread()
        chat.ask()
        val before = chat.snapshot()
        client.before = { name, _ -> if (name == "repotory_chat") error("unknown tool repotory_chat") }
        chat.refresh()
        assertEquals(before.messages, chat.snapshot().messages)
        assertEquals(before.threads, chat.snapshot().threads)
        assertTrue(chat.snapshot().syncError.contains("upgrade"))
        assertFalse(chat.snapshot().synced)
        val local = ChatSession(client) {}
        for (mode in ChatMode.entries) local.run("still works", mode)
        assertTrue(local.turns().all { it.state == TurnState.DONE })
        client.before = { _, _ -> }
        chat.refresh()
        assertEquals("", chat.snapshot().syncError)
        assertTrue(chat.snapshot().synced)
    }

    @Test fun `malformed history cannot silently erase cached messages`() {
        val client = Client(Store())
        val chat = session(client)
        chat.newThread()
        chat.ask()
        val old = chat.snapshot().messages
        client.rawRead = "{\"thread\": {}, \"messages\": []}"
        chat.refresh()
        assertEquals(old, chat.snapshot().messages)
        assertTrue(chat.snapshot().syncError.isNotBlank())
    }

    @Test fun `close during an ask prevents later writes and notifications`() {
        val client = Client(Store())
        var events = 0
        val chat = SharedChatSession(client) { events++ }
        chat.setVisible(true)
        chat.newThread()
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        client.before = { name, _ -> if (name == "repotory_ask") { entered.countDown(); check(release.await(5, TimeUnit.SECONDS)) } }
        val job = assertNotNull(chat.prepareSend("Close while waiting"))
        val pool = Executors.newSingleThreadExecutor()
        try {
            val future = pool.submit { chat.send(job) }
            assertTrue(entered.await(5, TimeUnit.SECONDS))
            chat.close()
            val count = events
            release.countDown()
            future.get(5, TimeUnit.SECONDS)
            chat.refresh()
            assertNull(chat.prepareRetry(job.userId))
            assertEquals(count, events)
            assertFalse(client.calls.any { it.second["role"] == "assistant" })
        } finally { release.countDown(); pool.shutdownNow() }
    }

    @Test fun `visibility polling uses three seconds cancels hidden tasks resumes and cleans up`() {
        val client = Client(Store())
        val chat = SharedChatSession(client) {}
        val workers = mutableListOf<() -> Unit>()
        data class Scheduled(val delay: Int, val work: () -> Unit, var cancelled: Boolean = false)
        val timers = mutableListOf<Scheduled>()
        val poll = SharedChatPolling(chat, { workers.add(it) }, { it() }) { delay, work ->
            val task = Scheduled(delay, work)
            timers.add(task)
            val cancel: () -> Unit = { task.cancelled = true }
            cancel
        }
        poll.setVisible(false)
        assertTrue(workers.isEmpty())
        poll.setVisible(true)
        assertEquals(1, workers.size)
        workers.removeAt(0)()
        assertEquals(3000, timers.single().delay)
        assertEquals(1, client.calls.size)
        poll.setVisible(false)
        assertTrue(timers.single().cancelled)
        timers.single().work() // A stale host callback must also be harmless.
        assertTrue(workers.isEmpty())
        poll.setVisible(true)
        assertEquals(1, workers.size)
        poll.refreshNow() // Do not overlap a queued/in-flight read.
        assertEquals(1, workers.size)
        workers.removeAt(0)()
        assertEquals(1, workers.size)
        poll.close()
        val calls = client.calls.size
        workers.removeAt(0)()
        poll.setVisible(true)
        poll.refreshNow()
        assertEquals(calls, client.calls.size)
        assertTrue(workers.isEmpty())
        assertEquals(1, timers.size)
    }

    @Test fun `hiding during list skips read and reopening fetches the latest history`() {
        val client = Client(Store())
        val chat = session(client)
        chat.newThread()
        chat.ask()
        val count = client.calls.size
        client.after = { _, args -> if (args["operation"] == "list") chat.setVisible(false) }
        chat.refresh()
        assertEquals(listOf("list"), client.calls.drop(count).map { it.second["operation"] })
        chat.refresh()
        assertEquals(count + 1, client.calls.size)
        client.after = { _, _ -> }
        chat.setVisible(true)
        chat.refresh()
        assertEquals("read", client.calls.last().second["operation"])
        assertEquals(2, chat.snapshot().messages.size)
    }

    @Test fun `stale read cannot undo a rename and rename failure stays with its thread`() {
        val client = Client(Store())
        val chat = session(client)
        val first = chat.newThread("Original")
        val second = chat.newThread("Other")
        chat.select(first)
        var renameDuringRead = true
        client.after = { _, args ->
            if (args["operation"] == "read" && renameDuringRead) {
                renameDuringRead = false
                chat.rename(first, "Renamed")
            }
        }
        chat.refresh()
        assertEquals("Renamed", chat.snapshot().selected?.title)
        client.before = { _, args ->
            if (args["operation"] == "rename") {
                chat.select(second)
                error("rename refused")
            }
        }
        chat.rename(first, "Will fail")
        assertEquals(second, chat.snapshot().selectedId)
        assertEquals("", chat.snapshot().operationError)
        chat.select(first)
        assertTrue(chat.snapshot().operationError.contains("rename refused"))
        assertEquals("Renamed", chat.snapshot().selected?.title)
    }

    @Test fun `validation and empty ask responses are failures not shared answers`() {
        for (response in listOf("query too long: 90000 bytes", "query is required", "")) {
            val client = Client(Store())
            val chat = session(client)
            chat.newThread()
            client.answer = response
            chat.ask()
            assertEquals(listOf("user"), chat.snapshot().messages.map { it.role })
            assertTrue(chat.snapshot().pending.single().error.isNotBlank())
            assertNull(chat.snapshot().pending.single().answer)
        }
    }

    @Test fun `shared HTML escapes titles messages errors and sources`() {
        val injection = "<html><img src=\"file:///secret\"><script>alert(1)</script></html>"
        val state = SharedChatState(threads = listOf(SharedThread("t", injection, injection, "", "")),
            selectedId = "t", messages = listOf(SharedMessage("m", "user", injection, injection, null, "")),
            syncError = injection, operationError = injection)
        val html = ChatConsoleHtml.renderShared(state)
        assertFalse(html.contains("<script>"))
        assertFalse(html.contains("<img"))
        assertTrue(html.contains("&lt;script&gt;"))
        assertTrue(html.contains("&quot;file:///secret&quot;"))
        assertTrue(html.contains("not Warden's local provider"))
    }

    @Test fun `a thread longer than the read window ages out the same turns the other clients age out`() {
        val gson = GsonBuilder().serializeNulls().create()
        val client = Client(Store())
        val panel = session(client)
        panel.newThread()
        val messages = (1..600).map { i -> mapOf(
            "id" to "m%03d".format(i),
            "role" to "user",
            "content" to "q$i",
            "source" to "warden",
            "replyTo" to null,
            "createdAt" to "%05d".format(i),
        ) }
        client.rawRead = gson.toJson(mapOf("thread" to assertNotNull(panel.snapshot().selected), "messages" to messages))
        panel.refresh()
        // 500 is the server's read window (service.chatMessageLimit). The literal is
        // on purpose: Warden and VS Code replace their list with the latest 500, so a
        // panel that accumulated every message it had ever seen would show turns the
        // store aged out — three clients, three transcripts for one thread.
        assertEquals(500, panel.snapshot().messages.size)
        assertEquals("m101", panel.snapshot().messages.first().id)
    }
}
