package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"repotory/internal/db"
	"repotory/internal/service"
	"repotory/internal/version"
)

// testServer bundles the pieces a test needs: a real MCPServer with every
// tool registered, the service behind it, and the raw *sql.DB so tests can
// seed fixtures. The same *sql.DB backs both the service and the seed
// (matching internal/app's setup), so there is no cross-handle visibility
// problem with :memory: databases.
type testServer struct {
	t   *testing.T
	s   *server.MCPServer
	svc *service.Service
	db  *sql.DB
}

// newTestServer wires the real tool registration over an in-memory database,
// so tests exercise the same code path a client does (registerTools →
// server.GetTool → handler) rather than a reimplementation of it.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc := service.New(database, "tester")
	s := server.NewMCPServer("repotory-mcp", "test")
	registerTools(s, svc)
	registerAskTool(s, svc)
	registerWikiTools(s, svc)
	registerContextTools(s, svc)
	registerScanTool(s, svc)
	registerAuditTools(s, svc)
	registerReviewTools(s, svc)
	return &testServer{t: t, s: s, svc: svc, db: database}
}

// seedProject creates a project the same way the desktop app does and returns
// its ID.
func (ts *testServer) seedProject(name, root string) int64 {
	ts.t.Helper()
	tx, err := ts.db.Begin()
	if err != nil {
		ts.t.Fatal(err)
	}
	pid, err := db.SyncProjectTx(tx, name, root, 0, true)
	if err != nil {
		ts.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		ts.t.Fatal(err)
	}
	return pid
}

// call invokes a registered tool by name the way a client would: through the
// server's tool table, not a captured handler reference.
func (ts *testServer) call(name string, args map[string]any) *mcp.CallToolResult {
	return ts.callWith(context.Background(), name, args)
}

// callWith runs a tool under an explicit context, for cancellation-sensitive
// paths the fixed background call cannot exercise.
func (ts *testServer) callWith(ctx context.Context, name string, args map[string]any) *mcp.CallToolResult {
	ts.t.Helper()
	st := ts.s.GetTool(name)
	if st == nil {
		ts.t.Fatalf("tool %q not registered", name)
	}
	res, err := st.Handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		ts.t.Fatalf("handler %q returned error: %v", name, err)
	}
	if res == nil {
		ts.t.Fatalf("handler %q returned nil result", name)
	}
	return res
}

// text flattens a result's content to a single string for assertions.
func (ts *testServer) text(res *mcp.CallToolResult) string {
	ts.t.Helper()
	if len(res.Content) != 1 {
		ts.t.Fatalf("expected 1 content block, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		ts.t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

// jsonPayload decodes a result's JSON text into a map for key assertions.
func (ts *testServer) jsonPayload(res *mcp.CallToolResult) map[string]any {
	ts.t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(ts.text(res)), &out); err != nil {
		ts.t.Fatalf("result is not JSON: %v\n%s", err, ts.text(res))
	}
	return out
}

// wantCoreVersion is the memory-layer contract version, pinned next to the
// tool list it describes. The two travel together on purpose: the contract
// version is the number a client pins against, so it moves only when the
// outward surface moves (minor = additive, major = breaking). Adding or
// removing a tool therefore forces a deliberate decision about which number
// changes, instead of leaving it to whoever notices.
//
// The literal is duplicated in internal/version/core.go; the drift guard in
// scripts/check-version-drift.mjs fails if the two copies disagree.
const wantCoreVersion = "1.4.0"

func TestToolsRegistered(t *testing.T) {
	ts := newTestServer(t)
	want := []string{
		"repotory_notes_list",
		"repotory_notes_search",
		"repotory_notes_read",
		"repotory_projects_list",
		"repotory_projects_stats",
		"repotory_ask",
		"repotory_notes_create",
		"repotory_notes_update",
		"repotory_notes_organize",
		"repotory_agent_score",
		"repotory_integrity",
		"repotory_context",
		"repotory_handoff",
		"repotory_scan",
		"repotory_ai_audit",
		"repotory_ai_review_rules",
		"repotory_wiki_search",
		"repotory_wiki_read",
		"repotory_chat",
		"repotory_metrics",
		"repotory_review_latest",
		"repotory_review_run",
	}
	for _, name := range want {
		st := ts.s.GetTool(name)
		if st == nil {
			t.Errorf("tool %q missing", name)
			continue
		}
		if st.Tool.Description == "" {
			t.Errorf("tool %q has empty description — agents pick tools by description", name)
		}
		if st.Tool.InputSchema.Type != "object" {
			t.Errorf("tool %q schema type = %q, want object", name, st.Tool.InputSchema.Type)
		}
	}
	if got := len(ts.s.ListTools()); got != len(want) {
		t.Errorf("registered %d tools, want %d", got, len(want))
	}
	if version.CoreVersion != wantCoreVersion {
		t.Errorf("core version = %q, want %q — the contract version moves when the tool surface moves "+
			"(minor = additive, major = breaking), so bump it here on purpose", version.CoreVersion, wantCoreVersion)
	}
}

func TestProjectsListEmpty(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_projects_list", nil)
	// Fresh DB: JSON `[]`, not `null` — an agent testing for an empty array
	// should not have to handle null.
	if got := strings.TrimSpace(ts.text(res)); got != "[]" {
		t.Errorf("empty projects = %q, want []", got)
	}
}

func TestProjectsListAndStats(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")

	list := ts.call("repotory_projects_list", nil)
	var projects []map[string]any
	if err := json.Unmarshal([]byte(ts.text(list)), &projects); err != nil {
		t.Fatalf("projects list is not a JSON array: %v\n%s", err, ts.text(list))
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if id, _ := projects[0]["id"].(float64); int64(id) != pid {
		t.Errorf("project id = %v, want %d", projects[0]["id"], pid)
	}

	stats := ts.call("repotory_projects_stats", map[string]any{"id": float64(pid)})
	payload := ts.jsonPayload(stats)
	if _, ok := payload["project"]; !ok {
		t.Errorf("project_stats missing project key: %s", ts.text(stats))
	}
}

func TestNotesSearchRequiresQuery(t *testing.T) {
	ts := newTestServer(t)
	for _, args := range []map[string]any{nil, {"query": ""}} {
		res := ts.call("repotory_notes_search", args)
		if res.IsError {
			t.Errorf("args %v: expected non-error result, got error", args)
		}
		if got := ts.text(res); !strings.Contains(got, "query is required") {
			t.Errorf("args %v: message = %q, want it to mention query is required", args, got)
		}
	}
}

func TestAskRequiresQuery(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_ask", nil)
	if res.IsError {
		t.Error("expected non-error result for missing query")
	}
	if got := ts.text(res); !strings.Contains(got, "query is required") {
		t.Errorf("message = %q, want it to mention query is required", got)
	}
}

func TestNotesCreateAndReadRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	if got := len(ts.svc.ListProjects()); got != 0 {
		t.Fatalf("fresh install should have 0 projects, got %d", got)
	}
	pid := ts.seedProject("demo", "/tmp/demo")

	created := ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid),
		"title":      "FTS5 quirk",
		"content":    "external-content FTS5 tables answer content queries from the content table",
		"category":   "knowledge",
		"tags":       "sqlite,search",
	})
	createdPayload := ts.jsonPayload(created)
	noteID, ok := createdPayload["id"].(float64)
	if !ok {
		t.Fatalf("create result has no numeric id: %s", ts.text(created))
	}

	read := ts.call("repotory_notes_read", map[string]any{"id": noteID})
	readPayload := ts.jsonPayload(read)
	if title, _ := readPayload["title"].(string); title != "FTS5 quirk" {
		t.Errorf("read title = %q, want %q", title, "FTS5 quirk")
	}

	// Update content only, then confirm the new content is readable.
	ts.call("repotory_notes_update", map[string]any{
		"id":      noteID,
		"content": "updated: v12 rebuilds the index",
	})
	updated := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": noteID}))
	if content, _ := updated["content"].(string); !strings.Contains(content, "v12 rebuilds") {
		t.Errorf("updated content = %q, want it to contain the new text", content)
	}

	// The new content must be findable by search — this is the round trip
	// that silently broke when the FTS backfill was a no-op (CHANGELOG 1.8.0).
	hits := ts.call("repotory_notes_search", map[string]any{"query": "rebuilds"})
	if got := ts.text(hits); !strings.Contains(got, "FTS5 quirk") {
		t.Errorf("search for indexed content returned %s, want a hit on the note", got)
	}
}

func TestNotesCreateMissingProject(t *testing.T) {
	ts := newTestServer(t)
	// Nonexistent project: the handler must surface an error-bearing result,
	// not panic and not return a success-shaped payload.
	res := ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(999999),
		"title":      "orphan",
		"content":    "attached to nothing",
	})
	if res.IsError {
		t.Fatal("expected a text result with an error message, not a protocol error")
	}
	if got := ts.text(res); !strings.Contains(got, "error") {
		t.Errorf("expected error indication, got: %s", got)
	}
}

func TestNotesReadMissingID(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_notes_read", map[string]any{"id": float64(424242)})
	if res.IsError {
		t.Fatal("not-found should be a text result, not a protocol error")
	}
	if got := ts.text(res); !strings.Contains(got, "note not found") {
		t.Errorf("message = %q, want it to say note not found", got)
	}
}

func TestProjectsStatsMissingProject(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_projects_stats", map[string]any{"id": float64(424242)})
	if res.IsError {
		t.Fatal("not-found should be a text result, not a protocol error")
	}
	if got := ts.text(res); !strings.Contains(got, "project not found") {
		t.Errorf("message = %q, want it to say project not found", got)
	}
}

// TestNotesUpdatePartialKeepsMetadata is the regression test for the
// partial-update data loss: repotory_notes_update with only "category" went
// through UpdateNoteMeta, which replaces title/tags/kind/pinned wholesale —
// silently wiping the title and tags and unpinning the note.
func TestNotesUpdatePartialKeepsMetadata(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")

	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid),
		"title":      "FTS5 quirk",
		"content":    "external-content FTS5 tables answer content queries from the content table",
		"category":   "knowledge",
		"tags":       "sqlite,search",
	}))
	noteID, _ := created["id"].(float64)

	// No MCP pin tool exists, so pin through the service to cover pin
	// preservation as well.
	if err := ts.svc.PinNote(int64(noteID), true); err != nil {
		t.Fatalf("pin: %v", err)
	}

	// The documented partial-update use: change only the category.
	ts.call("repotory_notes_update", map[string]any{
		"id":       noteID,
		"category": "log",
	})

	got := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": noteID}))
	if title, _ := got["title"].(string); title != "FTS5 quirk" {
		t.Errorf("category-only update wiped the title: %q", title)
	}
	// Tags come back normalized ("sql,search" style input joins with ", "):
	// the db layer canonicalizes every write since the tag-list fix.
	if tags, _ := got["tags"].(string); tags != "sqlite, search" {
		t.Errorf("category-only update wiped the tags: %q", tags)
	}
	if kind, _ := got["kind"].(string); kind != "log" {
		t.Errorf("kind = %q, want log", kind)
	}
	if pinned, _ := got["pinned"].(bool); !pinned {
		t.Error("category-only update unpinned the note")
	}
	if content, _ := got["content"].(string); !strings.Contains(content, "FTS5 tables") {
		t.Errorf("content changed by a category-only update: %q", content)
	}
}

// TestNotesUpdatePinnedParameter covers the optional `pinned` argument
// (ADR-0035 decision 3): the JetBrains plugin writes the pin state today, so
// a migration to the MCP surface must not lose it.
//
// The point of the test is the OMISSION, not the set. `pinned` is a boolean, so
// "not given" and "given as false" are different requests that both arrive as
// a JSON value. A tool that reads the value without checking for presence
// silently unpins every note it touches — the same wholesale-replacement trap
// that once wiped titles and tags, recorded in the sibling test above.
func TestNotesUpdatePinnedParameter(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")

	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid),
		"title":      "pinning",
		"content":    "a note whose pin state has to survive a partial update",
		"category":   "knowledge",
	}))
	noteID, _ := created["id"].(float64)

	pinnedOf := func() bool {
		got := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": noteID}))
		pinned, _ := got["pinned"].(bool)
		return pinned
	}

	// 1. Omitted → the current state is kept. This is the case that matters
	//    most: it is what every other field-only update does.
	ts.call("repotory_notes_update", map[string]any{
		"id": noteID, "content": "first revision",
	})
	if pinnedOf() {
		t.Error("a content-only update pinned an unpinned note")
	}

	// 2. pinned=true → pinned, and the title/tags ride along untouched.
	ts.call("repotory_notes_update", map[string]any{
		"id": noteID, "pinned": true,
	})
	if !pinnedOf() {
		t.Error("pinned=true did not pin the note")
	}
	got := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": noteID}))
	if title, _ := got["title"].(string); title != "pinning" {
		t.Errorf("a pin-only update wiped the title: %q", title)
	}

	// 3. pinned=false → unpinned. Reading false as "absent" would make this
	//    the step that silently does nothing.
	ts.call("repotory_notes_update", map[string]any{
		"id": noteID, "pinned": false,
	})
	if pinnedOf() {
		t.Error("pinned=false did not unpin the note")
	}

	// 4. pinned=false together with other fields → both land.
	ts.call("repotory_notes_update", map[string]any{
		"id": noteID, "pinned": true, "content": "second revision",
	})
	if !pinnedOf() {
		t.Error("a mixed update lost the pin")
	}
	got = ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": noteID}))
	if content, _ := got["content"].(string); content != "second revision" {
		t.Errorf("a mixed update lost the content: %q", content)
	}
}

// A client that sends pinned as a string meant to set it. Dropping the
// argument and reporting success would be the worst outcome: the caller would
// believe the note was pinned while nothing changed.
func TestNotesUpdatePinnedRejectsNonBoolean(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid), "title": "t", "content": "body",
	}))
	noteID, _ := created["id"].(float64)

	res := ts.call("repotory_notes_update", map[string]any{
		"id": noteID, "pinned": "true",
	})
	if res.IsError {
		t.Fatal("a bad argument should be a text result, not a protocol error")
	}
	if got := ts.text(res); !strings.Contains(got, "pinned must be a boolean") {
		t.Errorf("message = %q, want it to say pinned must be a boolean", got)
	}
	got := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": noteID}))
	if pinned, _ := got["pinned"].(bool); pinned {
		t.Error("a rejected argument still changed the note")
	}
}

// TestNotesUpdateSchemaExposesOptionalPinned pins the agent-facing contract
// of the `pinned` argument (ADR-0035 decision 3).
//
// The schema is what a client reads before it calls: an argument that is
// missing from the schema is an argument no client can discover, and one that
// lands in `required` would turn "omit it" into a protocol error instead of
// "keep the current value" — which is the whole point of the partial-update
// discipline.
func TestNotesUpdateSchemaExposesOptionalPinned(t *testing.T) {
	ts := newTestServer(t)
	tool := ts.s.GetTool("repotory_notes_update")
	if tool == nil {
		t.Fatal("repotory_notes_update is not registered")
	}

	props, ok := tool.Tool.InputSchema.Properties["pinned"].(map[string]any)
	if !ok {
		t.Fatalf("pinned is not in the schema properties: %#v", tool.Tool.InputSchema.Properties)
	}
	if kind, _ := props["type"].(string); kind != "boolean" {
		t.Errorf("pinned type = %q, want boolean", kind)
	}
	if desc, _ := props["description"].(string); desc == "" {
		t.Error("pinned has no description — a client cannot know what false means")
	}

	for _, required := range tool.Tool.InputSchema.Required {
		if required == "pinned" {
			t.Error("pinned must be optional: requiring it breaks the keep-the-current-value rule")
		}
	}

	// The documented property set, so the shipped reference docs
	// (skill/repotory/references/mcp-tools.md) cannot drift from the server.
	want := map[string]bool{
		"id": true, "content": true, "title": true,
		"tags": true, "category": true, "pinned": true,
	}
	for name := range want {
		if _, present := tool.Tool.InputSchema.Properties[name]; !present {
			t.Errorf("schema lost the %q property", name)
		}
	}
	for name := range tool.Tool.InputSchema.Properties {
		if !want[name] {
			t.Errorf("schema gained an undocumented %q property", name)
		}
	}
}

func TestNotesUpdateMissingNote(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_notes_update", map[string]any{
		"id":      float64(424242),
		"content": "attached to nothing",
	})
	if res.IsError {
		t.Fatal("not-found should be a text result, not a protocol error")
	}
	if got := ts.text(res); !strings.Contains(got, "note not found") {
		t.Errorf("message = %q, want it to say note not found", got)
	}
}

// Handoff notes are the session-memory protocol's exit records. One mistaken
// update used to silently overwrite the contract repotory_context hands to
// the next session, so the MCP write path refuses them.
func TestNotesUpdateRefusesHandoffNote(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")

	handoff, err := ts.svc.CreateHandoffNote(service.HandoffInput{
		ProjectID: pid,
		Summary:   "Migrated the schema",
		Changes:   []string{"Add v11 migration"},
	})
	if err != nil {
		t.Fatalf("CreateHandoffNote: %v", err)
	}

	res := ts.call("repotory_notes_update", map[string]any{
		"id":      float64(handoff.NoteID),
		"content": "please ignore the previous session's record",
	})
	if res.IsError {
		t.Fatal("refusal should be a text result, not a protocol error")
	}
	if got := ts.text(res); !strings.Contains(got, "refusing to update") || !strings.Contains(got, "repotory_handoff") {
		t.Errorf("message = %q, want a refusal pointing at repotory_handoff", got)
	}

	// The record must be untouched, not just answered with a warning.
	got := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": float64(handoff.NoteID)}))
	if content, _ := got["content"].(string); !strings.Contains(content, "Migrated the schema") {
		t.Errorf("handoff content was modified: %q", content)
	}
	if tags, _ := got["tags"].(string); !strings.Contains(tags, "handoff") {
		t.Errorf("handoff tags changed: %q", tags)
	}

	// A plain note on the same project must still update: the protection is
	// targeted, not a blanket write-block.
	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid), "title": "plain", "content": "body",
	}))
	plainID, _ := created["id"].(float64)
	ts.call("repotory_notes_update", map[string]any{
		"id": plainID, "content": "updated body",
	})
	if got := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": plainID})); got["content"] != "updated body" {
		t.Errorf("plain note must still update, got %q", got["content"])
	}
}

func TestNotesUpdateNothingToUpdate(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid), "title": "t", "content": "body",
	}))
	noteID, _ := created["id"].(float64)

	// No updatable field: the call must say so instead of silently
	// succeeding without touching anything.
	res := ts.call("repotory_notes_update", map[string]any{"id": noteID})
	if got := ts.text(res); !strings.Contains(got, "nothing to update") {
		t.Errorf("message = %q, want it to say nothing to update", got)
	}
}

// The tidying tool returns a PROPOSAL: the note in the database is byte-for-
// byte what it was, and the payload carries the model's rewrite for a human
// to review and save through repotory_notes_update.
func TestNotesOrganizeReturnsProposalAndWritesNothing(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid), "title": "网关重试", "content": "最多重试三次，幂等键是 invoice id。",
	}))
	noteID, _ := created["id"].(float64)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := "###TITLE\n支付网关重试与幂等\n###TAGS\ngateway,retry\n###CONTENT\n# 支付网关重试与幂等\n\n最多重试三次。\n\n```mermaid\nflowchart TD\n  A[提交] --> B[再次提交]\n```\n"
		b, _ := json.Marshal(reply)
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}]}`, b)
	}))
	t.Cleanup(srv.Close)
	for k, v := range map[string]string{"ai_chat_base_url": srv.URL + "/v1", "ai_chat_model": "stub"} {
		if err := ts.svc.UpdateConfig(k, v); err != nil {
			t.Fatalf("config %s: %v", k, err)
		}
	}

	got := ts.jsonPayload(ts.call("repotory_notes_organize", map[string]any{"id": noteID}))
	if title, _ := got["title"].(string); title != "支付网关重试与幂等" {
		t.Errorf("proposal title = %q", title)
	}
	content, _ := got["content"].(string)
	if !strings.Contains(content, "```mermaid") {
		t.Errorf("proposal lost the mermaid block:\n%s", content)
	}

	// The whole point: the store is untouched until a human saves.
	after := ts.jsonPayload(ts.call("repotory_notes_read", map[string]any{"id": noteID}))
	if after["content"] != created["content"] || after["title"] != created["title"] {
		t.Errorf("organize wrote to the note: before %v, after %v", created, after)
	}
}

// Without a configured endpoint the tool must say WHERE to configure it, not
// surface a raw config error.
func TestNotesOrganizeUnconfiguredAI(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("demo", "/tmp/demo")
	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid), "title": "t", "content": "body",
	}))
	noteID, _ := created["id"].(float64)

	res := ts.call("repotory_notes_organize", map[string]any{"id": noteID})
	if got := ts.text(res); !strings.Contains(got, "Settings") {
		t.Errorf("message = %q, want it to point at Settings", got)
	}
}

func TestSearchQueryLengthCap(t *testing.T) {
	ts := newTestServer(t)
	long := strings.Repeat("q", maxArgQueryLen+1)
	for _, name := range []string{"repotory_notes_search", "repotory_ask"} {
		res := ts.call(name, map[string]any{"query": long})
		if res.IsError {
			t.Errorf("%s: expected a text result, not a protocol error", name)
		}
		if got := ts.text(res); !strings.Contains(got, "query too long") {
			t.Errorf("%s: message = %q, want it to mention the length cap", name, got)
		}
	}
}

func TestContextProjectNameLengthCap(t *testing.T) {
	ts := newTestServer(t)
	res := ts.call("repotory_context", map[string]any{
		"project_name": strings.Repeat("p", maxProjectNameLen+1),
	})
	if res.IsError {
		t.Fatal("expected a text result, not a protocol error")
	}
	if got := ts.text(res); !strings.Contains(got, "project_name too long") {
		t.Errorf("message = %q, want it to mention the length cap", got)
	}
}

func TestAgentScoreOnFreshInstall(t *testing.T) {
	ts := newTestServer(t)
	out := ts.text(ts.call("repotory_agent_score", nil))
	if !strings.Contains(out, "Agent Score") {
		t.Errorf("output missing header:\n%s", out)
	}
	if !strings.Contains(out, "Score: ") {
		t.Errorf("output missing score line:\n%s", out)
	}
	// Fresh install has no notes: check 1 (DB reachable) must still pass,
	// because it now pings the handle instead of reusing noteCount.
	if !strings.Contains(out, "Database reachable") {
		t.Errorf("output missing database check:\n%s", out)
	}
	// The two checks must not be the same signal — "No notes" should appear
	// as its own warning, which is only possible if check 1 didn't also hinge
	// on noteCount (the double-count bug fixed in 1.8.0).
	if !strings.Contains(out, "No notes") {
		t.Errorf("expected a distinct no-notes warning:\n%s", out)
	}
}

func TestAgentScoreScoreLineIsFractionOfTotal(t *testing.T) {
	ts := newTestServer(t)
	out := ts.text(ts.call("repotory_agent_score", nil))
	// Parse "Score: N/M" and N <= M; a renderer bug that dropped the total
	// would silently make every install look "ready".
	i := strings.Index(out, "Score: ")
	if i < 0 {
		t.Fatalf("no score line:\n%s", out)
	}
	rest := out[i+len("Score: "):]
	j := strings.IndexAny(rest, "\n")
	if j < 0 {
		t.Fatalf("score line not terminated:\n%s", out)
	}
	var earned, total int
	e, tt, err := sscanfScore(rest[:j])
	if err != nil {
		t.Fatalf("cannot parse score %q: %v", rest[:j], err)
	}
	earned, total = e, tt
	if total <= 0 || earned < 0 || earned > total {
		t.Errorf("score %d/%d out of range", earned, total)
	}
}

// sscanfScore parses "N/M" without pulling fmt's verb handling into the test.
func sscanfScore(s string) (int, int, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want N/M, got %q", s)
	}
	earned, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	total, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	return earned, total, nil
}

func TestIntegrityReportOnFreshInstall(t *testing.T) {
	ts := newTestServer(t)
	out := ts.text(ts.call("repotory_integrity", nil))
	if !strings.Contains(out, "Data Integrity") {
		t.Errorf("output missing header:\n%s", out)
	}
	if !strings.Contains(out, "Trust score:") {
		t.Errorf("output missing trust score:\n%s", out)
	}
	if !strings.Contains(out, "checks:") {
		t.Errorf("output missing check summary:\n%s", out)
	}
	// On an empty database nothing should be reported as FAILED.
	if strings.Contains(out, "FAILED") {
		t.Errorf("fresh install should not report failures:\n%s", out)
	}
}

func TestIntegrityReportFindsSeededDrift(t *testing.T) {
	ts := newTestServer(t)
	pid := ts.seedProject("drift", "/tmp/drift")

	// Create a note through MCP (so it lands in both content table and FTS),
	// then delete the FTS row directly to simulate index drift — the exact
	// failure mode the integrity tool exists to detect.
	created := ts.jsonPayload(ts.call("repotory_notes_create", map[string]any{
		"project_id": float64(pid),
		"title":      "will be orphaned",
		"content":    "search should still find this",
	}))
	noteID, _ := created["id"].(float64)

	if _, err := ts.db.Exec("DELETE FROM project_notes_fts WHERE rowid = ?", int64(noteID)); err != nil {
		t.Fatalf("simulating drift: %v", err)
	}

	out := ts.text(ts.call("repotory_integrity", nil))
	// The report must not come back clean when the index is provably wrong.
	if !strings.Contains(out, "FTS") {
		t.Errorf("expected an FTS-related finding:\n%s", out)
	}
}

// TestScanToolDiscoversRepositories covers the headless cold start: with no
// desktop app and an empty database, repotory_scan alone must discover repos
// and make repotory_context usable in the very next call.
func TestScanToolDiscoversRepositories(t *testing.T) {
	ts := newTestServer(t)
	tmp := t.TempDir()
	// Two repositories under one parent: the grouper collapses them into a
	// single (monorepo) project.
	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(tmp, name, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ReplaceScanRoots(ts.db, []string{tmp}); err != nil {
		t.Fatalf("seed scan root: %v", err)
	}

	res := ts.call("repotory_scan", nil)
	payload := ts.jsonPayload(res)
	if got, _ := payload["repos_found"].(float64); int(got) != 2 {
		t.Fatalf("repos_found = %v, want 2\n%s", payload["repos_found"], ts.text(res))
	}
	if got, _ := payload["projects"].(float64); int(got) != 1 {
		t.Fatalf("projects = %v, want 1 (both repos share a parent)", payload["projects"])
	}

	// The point of the scan: the next context call works with zero parameters.
	if text := ts.text(ts.call("repotory_context", nil)); !strings.Contains(text, "# Project Context:") {
		t.Errorf("context after scan should render a project\n%s", text)
	}
}

// A scan under an already-cancelled context (client disconnect) must report
// the cancellation, not "success: 0 repos" — the agent would otherwise
// conclude the scan found nothing and stop there.
func TestScanToolReportsCancellation(t *testing.T) {
	ts := newTestServer(t)
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "alpha", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceScanRoots(ts.db, []string{tmp}); err != nil {
		t.Fatalf("seed scan root: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := ts.callWith(ctx, "repotory_scan", nil)
	if got := ts.text(res); !strings.Contains(got, "canceled") {
		t.Errorf("cancelled scan should report the cancellation, got:\n%s", got)
	}
}
