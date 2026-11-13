package dev.repotory.jetbrains

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue
import kotlin.test.fail

/**
 * Gate B runner for the JetBrains panel (ADR-0059, TODO 13.2).
 *
 * The fixture (scripts/shared-chat-conformance.fixture.json) is the TARGET
 * contract: rules marked settled are asserted against the panel's REAL code
 * paths — [sharedMessageGroups]' pairing and ordering, and
 * [SharedChatSession]'s refusal to store the keyword fallback as an answer.
 * Rules marked skipped name the product decision they wait on (TODO 11.1-11.3)
 * and are guarded by assert_forbidden: a runner that asserts them would pin
 * today's divergence as the standard.
 *
 * The VS Code panel has its own runner (ide/vscode/src/__tests__/
 * conformance.test.ts) over the same file; the two fail independently and each
 * names its surface — that is gate B's point.
 */
class SharedChatConformanceTest {

    private fun fixturePath(): String {
        var dir = workingDir()
        repeat(8) {
            val candidate = java.nio.file.Path.of(dir, "scripts", "shared-chat-conformance.fixture.json")
            if (candidate.toFile().exists()) return candidate.toString()
            dir = java.nio.file.Path.of(dir).parent?.toString() ?: return@repeat
        }
        error("shared-chat-conformance.fixture.json not found above ${workingDir()}")
    }

    private fun workingDir(): String = System.getProperty("user.dir")

    private class Rule(private val raw: Map<String, Any?>) {
        val id: String get() = raw["id"] as String
        val status: String get() = raw["status"] as String
        val statement: String get() = raw["statement"] as String
        val expect: Map<String, Any?>? get() = @Suppress("UNCHECKED_CAST") (raw["expect"] as? Map<String, Any?>)
        val skipReason: String get() = raw["skip_reason"] as? String ?: ""
        val assertForbidden: Boolean get() = raw["assert_forbidden"] == true
        @Suppress("UNCHECKED_CAST")
        val input: Map<String, Any?> get() = raw["input"] as? Map<String, Any?> ?: emptyMap()
    }

    private class Fixture(private val raw: Map<String, Any?>) {
        @Suppress("UNCHECKED_CAST")
        private val rules = (raw["rules"] as List<Map<String, Any?>>).map { Rule(it) }
        val coreVersion: String
            get() = @Suppress("UNCHECKED_CAST") ((raw["contract_version"] as Map<String, Any?>)["core"] as String)
        val ruleCount: Int
            get() = @Suppress("UNCHECKED_CAST") ((raw["contract_version"] as Map<String, Any?>)["rules"] as Number).toInt()
        val settled: List<Rule> get() = rules.filter { it.status == "settled" }
        val skipped: List<Rule> get() = rules.filter { it.status == "skipped" }
        fun byId(id: String): Rule = rules.firstOrNull { it.id == id } ?: error("rule $id disappeared from the fixture: the runner implements a contract that no longer exists")
    }

    private fun loadFixture(): Fixture {
        val json = com.google.gson.JsonParser.parseString(java.nio.file.Files.readString(java.nio.file.Path.of(fixturePath())))
        val map = com.google.gson.internal.LinkedTreeMap<String, Any>()
        @Suppress("UNCHECKED_CAST")
        val asMap = com.google.gson.GsonBuilder().create().fromJson(json, Map::class.java) as Map<String, Any?>
        return Fixture(asMap)
    }

    /** The fixture's message dicts, converted to the panel's SharedMessage. */
    @Suppress("UNCHECKED_CAST")
    private fun messagesOf(rule: Rule): List<SharedMessage> {
        val list = rule.input["messages"] as? List<Map<String, Any?>> ?: error("rule ${rule.id} has no messages input")
        return list.map { m ->
            SharedMessage(
                id = m["id"] as String,
                role = m["role"] as String,
                content = m["content"] as String,
                source = m["source"] as String,
                replyTo = m["reply_to"] as? String,
                createdAt = m["created_at"] as String,
            )
        }
    }

    private fun pairsOf(rule: Rule): List<Pair<String, String?>> {
        return sharedMessageGroups(messagesOf(rule)).map { group ->
            val user = group.first()
            val reply = group.firstOrNull { it.role == "assistant" }
            user.id to reply?.id
        }
    }

    @Suppress("UNCHECKED_CAST")
    private fun expectTurns(rule: Rule): List<Pair<String, String>> =
        (rule.expect?.get("turns") as? List<Map<String, Any?>>)?.map { t ->
            (t["user_id"] as String) to (t["reply_id"] as String)
        } ?: error("rule ${rule.id} carries no turns expectation")

    @Test
    fun `fixture contract - the runner reads the one file and its counts hold`() {
        val fixture = loadFixture()
        assertEquals("2.0.0", fixture.coreVersion, "the fixture pins the tool-face contract; a change there goes through the CoreVersion gates first")
        assertEquals(fixture.ruleCount, fixture.settled.size + fixture.skipped.size, "contract_version.rules must equal the file's rule count")
        assertTrue(fixture.settled.isNotEmpty(), "a fixture with no settled rules gates nothing")
        // Skips may legitimately reach zero: a decided open question is promoted
        // to settled (11.1-11.3 were closed on 2026-11-13). What must never
        // happen is a rule DISAPPEARING — the rule count above and byId()'s
        // error are what make a deletion visible.
        (fixture.settled + fixture.skipped).forEach { rule ->
            assertTrue(rule.status == "settled" || rule.status == "skipped", "unknown status ${rule.status}")
        }
        // A skipped rule MUST carry the guard: asserting an open decision pins
        // today's divergence as the standard (ADR-0059).
        fixture.skipped.forEach { rule ->
            assertTrue(rule.assertForbidden, "skipped rule ${rule.id} must set assert_forbidden")
            assertTrue(rule.skipReason.contains("11."), "skipped rule ${rule.id} must name the TODO it waits on: ${rule.skipReason}")
        }
    }

    @Test
    fun `rule pairing attach - a turn pairs by reply_to, one authority`() {
        val fixture = loadFixture()
        val rule = fixture.byId("pairing.attach")
        assertEquals("settled", rule.status)
        val got = pairsOf(rule)
        assertEquals(expectTurns(rule), got, "the JetBrains panel's pairing diverged from the fixture — the fixture is the target, so the panel changes")
    }

    @Test
    fun `rule pairing order - turns render in the server's read order, never re-sorted`() {
        val fixture = loadFixture()
        val rule = fixture.byId("pairing.order")
        assertEquals("settled", rule.status)
        val got = pairsOf(rule)
        // This is the rule the current implementation FAILED: the group sort
        // orders by created_at (writer clock skew inverts real pairs). The
        // fixture is the target; if this assertion is red, sharedMessageGroups
        // changes, not the fixture.
        assertEquals(expectTurns(rule), got, "createdAt sorting across writer clocks inverts real pairs; the server's seq order is the only order two processes agree on")
    }

    @Test
    fun `rule fallback not persisted - the keyword fallback never lands as an answer`() {
        val fixture = loadFixture()
        val rule = fixture.byId("fallback.not_persisted")
        assertEquals("settled", rule.status)
        val store = Store()
        val client = Client(store)
        val session = SharedChatSession(client) {}
        session.setVisible(true)
        session.create("Fallback rule")
        val selected = assertNotNull(session.snapshot().selectedId)
        // askWithFallback drives prepareSend/send with the REAL transport ask:
        // the fake Client answers with the fixture's fallback text.
        val job = assertNotNull(session.prepareSend("Why this design?"))
        client.answer = rule.input["ask_tool_result"] as String
        session.send(job)
        val messages = store.messages.getValue(selected).values.toList()
        assertTrue(messages.any { it.role == "user" && it.content == "Why this design?" }, "the user's question is a real session event and stays")
        assertFalse(
            messages.any { it.role == "assistant" },
            "the fallback text must never be appended to the shared thread as an assistant message: " +
                messages.filter { it.role == "assistant" }.joinToString { it.content.take(80) },
        )
        session.close()
    }

    @Test
    fun `rule fallback not persisted zero hit - the zero-hit branch obeys the same rule`() {
        val fixture = loadFixture()
        val rule = fixture.byId("fallback.not_persisted.zero_hit")
        assertEquals("settled", rule.status)
        val store = Store()
        val client = Client(store)
        val session = SharedChatSession(client) {}
        session.setVisible(true)
        session.create("Zero hit")
        val selected = assertNotNull(session.snapshot().selectedId)
        val job = assertNotNull(session.prepareSend("nothing matches this"))
        // The zero-hit wording has NO "No AI endpoint" prefix — a prefix check
        // alone would store it (that is why the rule exists separately).
        client.answer = rule.input["ask_tool_result"] as String
        session.send(job)
        val messages = store.messages.getValue(selected).values.toList()
        assertFalse(messages.any { it.role == "assistant" }, "the zero-hit fallback must not land as an answer either")
        session.close()
    }

    @Test
    fun `formerly open rules are settled and carry their expectations`() {
        // These three were the open decisions (TODO 11.1 / 11.3). They were
        // promoted to settled on 2026-11-13 when the user decided: the thread
        // belongs to the person (ADR-0061), an orphan reply is shown, and the
        // last of several replies wins. The guards below are the inverse of the
        // ones that used to live here: a settled rule MUST carry an expectation,
        // because a settled rule without one gates nothing — and a "skip"
        // regression would silently stop asserting behaviour three panels ship.
        val fixture = loadFixture()
        for (id in listOf("scope.path", "pairing.orphan_reply", "pairing.multiple_replies")) {
            val rule = fixture.byId(id)
            assertEquals("settled", rule.status, "rule $id went back to skipped after the 2026-11-13 decision")
            assertFalse(rule.assertForbidden, "a settled rule must not keep assert_forbidden")
            assertNotNull(rule.expect, "settled rule $id carries no expectation: it would gate nothing")
        }
    }

    /** Self-contained fake store: same protocol surface as the session talks to. */
    private class Store {
        val threads = linkedMapOf<String, SharedThread>()
        val messages = mutableMapOf<String, LinkedHashMap<String, SharedMessage>>()
        private val gson = com.google.gson.GsonBuilder().serializeNulls().create()
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

    /** Self-contained fake transport: ask answers with the fixture's text. */
    @Test
    fun `rule scope path - a shared ask carries no scope`() {
        val fixture = loadFixture()
        val rule = fixture.byId("scope.path")
        assertEquals("settled", rule.status)
        @Suppress("UNCHECKED_CAST")
        val forbidden = rule.expect?.get("ask_args_keys_exclude") as List<String>
        assertEquals(listOf("path", "project_id"), forbidden)

        val store = Store()
        val client = Client(store)
        val session = SharedChatSession(client) {}
        session.setVisible(true)
        session.create("Scoped?")
        val job = assertNotNull(session.prepareSend("why?"))
        session.send(job)

        // The ASK ARGUMENTS, recorded by the fake transport, are what this rule
        // is about. A thread created from a workspace used to send that path,
        // which made the service resolve it to a project and answer from there
        // only — while VS Code answered from the whole library. Same thread,
        // same question, two answers.
        val askCall = client.calls.lastOrNull { it.first == "repotory_ask" }
        assertNotNull(askCall, "the ask must have run")
        for (key in forbidden) {
            assertFalse(
                askCall.second.containsKey(key),
                "the ask sent \"$key\": a shared thread belongs to the person, so the workspace must not scope the answer (ADR-0061)",
            )
        }
        session.close()
    }

    @Test
    fun `rule orphan reply - a reply with no question is shown as its own turn`() {
        val fixture = loadFixture()
        val rule = fixture.byId("pairing.orphan_reply")
        assertEquals("settled", rule.status)
        assertEquals(true, rule.expect?.get("orphan_rendered"))
        assertEquals(true, rule.expect?.get("orphan_not_dropped"))

        val messages = listOf(
            SharedMessage(id = "u1", role = "user", content = "paired question", source = "jetbrains", replyTo = null, createdAt = "2026-11-13T00:00:01Z"),
            SharedMessage(id = "u1-reply", role = "assistant", content = "paired answer", source = "jetbrains", replyTo = "u1", createdAt = "2026-11-13T00:00:02Z"),
            // replyTo names a user message that is NOT in this history.
            SharedMessage(id = "ghost-reply", role = "assistant", content = "answer with no question", source = "warden", replyTo = "u-gone", createdAt = "2026-11-13T00:00:03Z"),
        )
        val groups = sharedMessageGroups(messages)
        // The orphan is its own group (it has no user to attach to), so it is
        // never dropped. Its body is rendered the same way a paired reply is.
        val orphanGroup = groups.firstOrNull { it.any { message -> message.id == "ghost-reply" } }
        assertNotNull(orphanGroup, "the orphan reply was dropped: VS Code keeps it, so the same history would differ")
        assertEquals("ghost-reply", orphanGroup.first().id, "the orphan group is the reply itself")
        assertFalse(
            orphanGroup.size > 1,
            "an orphan must not be folded into some other question's group",
        )
    }

    @Test
    fun `rule multiple replies - one question, one answer, the last reply wins`() {
        val fixture = loadFixture()
        val rule = fixture.byId("pairing.multiple_replies")
        assertEquals("settled", rule.status)
        assertEquals("last_in_read_order", rule.expect?.get("reply_winner"))
        assertEquals(false, rule.expect?.get("earlier_replies_rendered"))

        val messages = listOf(
            SharedMessage(id = "u1", role = "user", content = "q", source = "jetbrains", replyTo = null, createdAt = "2026-11-13T00:00:01Z"),
            SharedMessage(id = "u1-reply", role = "assistant", content = "stale answer", source = "jetbrains", replyTo = "u1", createdAt = "2026-11-13T00:00:02Z"),
            SharedMessage(id = "u1-reply-2", role = "assistant", content = "final answer", source = "warden", replyTo = "u1", createdAt = "2026-11-13T00:00:03Z"),
        )
        val groups = sharedMessageGroups(messages)
        assertEquals(1, groups.size, "one question must stay one group")
        val replies = groups.first().filter { it.role == "assistant" }
        assertEquals(1, replies.size, "the stale duplicate must not be rendered: ${replies.map { it.id }}")
        assertEquals("u1-reply-2", replies.first().id, "the LAST reply in read order must win")
    }

    private class Client(private val store: Store) : ChatTransport {
        var answer: String = "Knowledge answer."
        val calls = mutableListOf<Pair<String, Map<String, Any?>>>()
        override fun callTool(name: String, args: Map<String, Any?>): String = when (name) {
            "repotory_ask" -> answer
            "repotory_chat" -> store.call(args)
            else -> error("unknown tool: $name")
        }.also { calls.add(name to args) }
        override fun rpc(method: String, args: List<Any?>): Map<String, Any?>? = null
        override fun workspacePath() = "/workspace/conformance"
        override fun serverUrl() = "unused"
    }
}
