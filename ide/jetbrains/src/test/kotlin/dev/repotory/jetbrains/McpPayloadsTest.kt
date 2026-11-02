package dev.repotory.jetbrains

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/**
 * The regression suite for the MCP payload parsers (ADR-0035 decision 2).
 *
 * The risk these tests exist for: a parser that quietly accepts the wrong
 * shape turns a failed call into an EMPTY result, and an empty result is the
 * one answer a user cannot tell apart from "nothing matched". So every tool
 * gets two cases — the payload it really returns, and the text it returns
 * when it refuses.
 */
class McpPayloadsTest {
    // ---------------------------------------------------------------- search

    @Test
    fun `notes search parses the hit list`() {
        val payload = """
            [
              {"type":"note","id":42,"project_id":7,"title":"FTS5 quirk","snippet":"content tables answer","rank":-1.2},
              {"type":"todo","id":9,"project_id":7,"title":"Ship v2","snippet":"release checklist"}
            ]
        """.trimIndent()
        val hits = McpPayloads.parseHits("repotory_notes_search", payload)
        assertEquals(2, hits.size)
        assertEquals("note", hits[0].type)
        assertEquals(42L, hits[0].id)
        assertEquals(7L, hits[0].projectId)
        assertEquals("FTS5 quirk", hits[0].title)
        assertEquals("content tables answer", hits[0].snippet)
        // The route vocabulary belongs to the frontend (web/src/App.tsx): a
        // note opens ?noteId=, a todo opens the todo tab.
        assertEquals("/project/7?noteId=42", hits[0].route)
        assertEquals("/project/7?tab=todos", hits[1].route)
    }

    @Test
    fun `notes search refuses an empty list when the tool said no`() {
        // The error path of the tool is TEXT, not JSON. Parsing it as "no
        // hits" would be the silent-empty-result bug.
        val failure = assertFailsWith<McpException> {
            McpPayloads.parseHits("repotory_notes_search", "query is required")
        }
        assertTrue(failure.message!!.contains("repotory_notes_search"))
        assertTrue(failure.message!!.contains("query is required"))
    }

    @Test
    fun `notes search refuses a payload of the wrong shape`() {
        // A JSON object where a list is expected is a contract change, not an
        // empty result set.
        assertFailsWith<McpException> {
            McpPayloads.parseHits("repotory_notes_search", """{"hits":[]}""")
        }
    }

    // ------------------------------------------------------------------ read

    @Test
    fun `notes read parses the note`() {
        val payload = """
            {"id":42,"project_id":7,"title":"FTS5 quirk","content":"the body","tags":"sqlite, search",
             "kind":"knowledge","pinned":true,"source":"manual","created_at":"2026-10-01","updated_at":"2026-10-02"}
        """.trimIndent()
        val note = McpPayloads.parseNote("repotory_notes_read", payload)
        assertEquals(42L, note.id)
        assertEquals("FTS5 quirk", note.title)
        assertEquals("the body", note.body)
        assertEquals("sqlite, search", note.tags)
        assertEquals("knowledge", note.kind)
        assertEquals(7L, note.projectId)
        assertTrue(note.pinned)
        assertEquals("2026-10-02", note.updatedAt)
    }

    @Test
    fun `notes read refuses the not-found text`() {
        val failure = assertFailsWith<McpException> {
            McpPayloads.parseNote("repotory_notes_read", "note not found: sql: no rows in result set")
        }
        assertTrue(failure.message!!.contains("note not found"))
    }

    // ---------------------------------------------------------------- create

    @Test
    fun `notes create parses the created note`() {
        val payload = """{"id":101,"project_id":7,"title":"New","content":"body","tags":"","kind":"knowledge","pinned":false}"""
        val note = McpPayloads.parseNote("repotory_notes_create", payload)
        assertEquals(101L, note.id)
        assertEquals("New", note.title)
        assertEquals(7L, note.projectId)
        assertEquals("knowledge", note.kind)
        assertTrue(!note.pinned)
    }

    // ---------------------------------------------------------------- update

    @Test
    fun `notes update parses the updated note with its pin state`() {
        val payload = """{"id":42,"project_id":7,"title":"FTS5 quirk","content":"body","tags":"","kind":"log","pinned":true}"""
        val note = McpPayloads.parseNote("repotory_notes_update", payload)
        assertEquals(42L, note.id)
        assertEquals("log", note.kind)
        // The pin state has to survive the round trip: the editor round-trips
        // it, and a lost pin is a silent unpin on the next save.
        assertTrue(note.pinned)
    }

    @Test
    fun `notes update refuses the handoff refusal`() {
        // Handoff notes are append-only on the MCP surface (ADR-0035
        // decision 4). The refusal is TEXT and must reach the user.
        val failure = assertFailsWith<McpException> {
            McpPayloads.parseNote(
                "repotory_notes_update",
                "refusing to update note 7: it is a session handoff record (tagged 'handoff') — " +
                    "the persisted protocol record between sessions, protected from plain overwrites. " +
                    "Write a new handoff with repotory_handoff; if this record itself is wrong, " +
                    "edit it from the desktop app.",
            )
        }
        assertTrue(failure.message!!.contains("refusing to update note 7"))
        assertTrue(failure.message!!.contains("repotory_handoff"))
    }

    // --------------------------------------------------------------- organize

    @Test
    fun `notes organize parses the proposal`() {
        val payload = """
            {"note_id":42,"title":"Tidy","content":"# Tidy\n\nbody","tags":"a, b","warning":"the reply was truncated"}
        """.trimIndent()
        val proposal = McpPayloads.parseOrganizeProposal("repotory_notes_organize", payload)
        assertEquals(42L, proposal.noteId)
        assertEquals("Tidy", proposal.title)
        assertTrue(proposal.content.startsWith("# Tidy"))
        assertEquals("a, b", proposal.tags)
        assertEquals("the reply was truncated", proposal.warning)
    }

    @Test
    fun `notes organize refuses an empty proposal`() {
        // An empty proposal is not a proposal: the editor would open with
        // nothing to review and the user would save an empty note.
        assertFailsWith<McpException> {
            McpPayloads.parseOrganizeProposal("repotory_notes_organize", """{"note_id":42,"content":"  "}""")
        }
    }

    // ----------------------------------------------------------------- pages

    @Test
    fun `wiki search parses the page list`() {
        val payload = """
            [{"id":45,"slug":"time-state-graph","title":"Time-state graph","kind":"concept",
              "project_id":0,"updated_at":"2026-10-21","snippet":"why we need it"}]
        """.trimIndent()
        val hits = McpPayloads.parseWikiHits("repotory_wiki_search", payload)
        assertEquals(1, hits.size)
        val page = hits[0]
        assertEquals("wiki", page.type)
        assertEquals(45L, page.id)
        assertEquals("time-state-graph", page.slug)
        assertEquals("concept", page.kind)
        assertEquals(0L, page.projectId)
        assertEquals("/wiki/45", page.route)
        assertEquals("why we need it", page.snippet)
    }

    @Test
    fun `wiki search refuses the error text`() {
        assertFailsWith<McpException> {
            McpPayloads.parseWikiHits("repotory_wiki_search", "error: page 999 not found")
        }
    }

    @Test
    fun `wiki read parses the page with its graph`() {
        val payload = """
            {"id":45,"slug":"time-state-graph","title":"Time-state graph","kind":"concept",
             "project_id":0,"updated_at":"2026-10-21","content":"the body","source":"wiki-compile",
             "status":"approved",
             "out_links":[{"page_id":46,"slug":"other","title":"Other","kind":"entity","relation":"ref"}],
             "in_links":[],
             "source_note_ids":[42,43]}
        """.trimIndent()
        val page = McpPayloads.parseWikiPage("repotory_wiki_read", payload)
        assertEquals(45L, page.id)
        assertEquals("time-state-graph", page.slug)
        assertEquals("the body", page.content)
        assertEquals("wiki-compile", page.source)
        assertEquals(1, page.outLinks.size)
        assertEquals(46L, page.outLinks[0].pageId)
        assertEquals("ref", page.outLinks[0].relation)
        assertEquals(0, page.inLinks.size)
        assertEquals(listOf(42L, 43L), page.sourceNoteIds)
    }

    @Test
    fun `wiki read refuses the error text`() {
        assertFailsWith<McpException> {
            McpPayloads.parseWikiPage("repotory_wiki_read", "error: page 999 not found")
        }
    }

    // -------------------------------------------------------------- projects

    @Test
    fun `projects list parses the project list`() {
        val payload = """
            [{"id":7,"name":"CBiPay","root_path":"/tmp/pay","level_override":0,"is_auto_grouped":false,
              "is_starred":true,"collected":false,"collected_at":"","created_at":"2026-10-01",
              "project_kind":"scanned"},
             {"id":9,"name":"Other","root_path":"/tmp/other"}]
        """.trimIndent()
        val projects = McpPayloads.parseProjects("repotory_projects_list", payload)
        assertEquals(2, projects.size)
        assertEquals(7L, projects[0].id)
        assertEquals("CBiPay", projects[0].name)
        assertEquals(9L, projects[1].id)
        assertEquals("Other", projects[1].name)
    }

    @Test
    fun `projects list refuses the error text`() {
        assertFailsWith<McpException> {
            McpPayloads.parseProjects("repotory_projects_list", "error: database is locked")
        }
    }

    // --------------------------------------------------------------- general

    @Test
    fun `an empty answer is a failure, not an empty list`() {
        val failure = assertFailsWith<McpException> {
            McpPayloads.parseArray("repotory_projects_list", "   ")
        }
        assertTrue(failure.message!!.contains("empty"))
    }
}
