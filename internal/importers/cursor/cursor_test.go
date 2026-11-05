package cursor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/importers/imptest"
	"repotory/internal/importers/memsrc"
)

// The samples below mirror the shapes verified against a real macOS install
// (bubble = {type, text, createdAt-ISO8601}, composerHeaders row per session,
// workspaceId = the 32-hex workspaceStorage directory name holding a
// workspace.json with a file:// folder URI).

const (
	wsHashKnown   = "87d6b92a30e184ca731bbd6532a00631"
	wsHashUnknown = "0000000000000000000000000000dead"
)

// wsFolder is the folder the sample workspace.json names, spelled the way the
// host spells paths: folderPathFromWorkspaceJSON cleans natively, so a
// forward-slash literal is not what it returns on Windows.
var wsFolder = filepath.FromSlash("/Users/dev/Workspace/CodeStat")

// newFixture builds a fake Cursor tree and an app database containing the
// CodeStat project, and points CURSOR_STATE_DB at the state file.
func newFixture(t *testing.T) (appDB *sql.DB, statePath string) {
	t.Helper()
	root := t.TempDir()
	userDir := filepath.Join(root, "User")
	gsDir := filepath.Join(userDir, "globalStorage")
	if err := os.MkdirAll(gsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath = filepath.Join(gsDir, "state.vscdb")
	writeCursorState(t, statePath)

	// workspaceStorage/<hash>/workspace.json — the VS Code layout Cursor shares.
	// The sample tree holds both workspaces: the one that matches a Cinder
	// project, and one that matches none (its sessions must not be guessed
	// onto a project).
	imptest.CopySample(t, "testdata/workspaceStorage", filepath.Join(userDir, "workspaceStorage"))

	t.Setenv("CURSOR_STATE_DB", statePath)

	app, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	// One transaction, committed: db.InitDB caps the pool at a single connection,
	// so an uncommitted tx here would deadlock the next Begin — which is precisely
	// how this test hung the first time it ran.
	tx, err := app.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.SyncProjectTx(tx, "CodeStat", wsFolder, 0, true)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := db.UpsertRepositoryTx(tx, wsFolder, pid); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return app, statePath
}

// bubbleJSON renders one message row.
func bubbleJSON(t *testing.T, typ int, text, createdAt string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"_v": 1, "type": typ, "text": text, "createdAt": createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writeCursorState creates a state.vscdb from the committed .sql sample: the
// two tables the importer reads and a deliberately messy set of sessions —
// subagent, empty-window, unknown workspace, out-of-order timestamps, and one
// unparseable bubble value.
//
// A .sql dump rather than a committed .db file: the schema and the rows are
// exactly what has to be reviewable, and a binary blob shows nothing when the
// format drifts (ADR-0011 decision 5).
func writeCursorState(t *testing.T, path string) {
	t.Helper()
	st, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	imptest.ExecScript(t, st, "testdata/state.sql")
}

func TestImportProducesOneAttributedNote(t *testing.T) {
	app, _ := newFixture(t)

	docs, err := New(app).Import()
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	attributedIdx := -1
	unattributed := 0
	for i, doc := range docs {
		if doc.ProjectID > 0 {
			attributedIdx = i
		} else {
			unattributed++
		}
	}
	if attributedIdx == -1 {
		t.Fatalf("no attributed (project-resolved) doc among %d docs", len(docs))
	}
	// The orphan workspace (a real folder that matches no project) is now emitted
	// with ProjectID==0 + a RepoPath so the runtime lands it on a placeholder
	// instead of silently dropping it (ADR-0023). Previously it produced nothing.
	if unattributed != 1 {
		t.Fatalf("want exactly one unattributed to-be-landed doc, got %d (of %d total)", unattributed, len(docs))
	}
	for _, doc := range docs {
		if doc.ProjectID == 0 && strings.TrimSpace(doc.RepoPath) == "" {
			t.Error("unattributed doc must carry RepoPath so the runtime can land a placeholder")
		}
	}
	d := docs[attributedIdx]
	if d.Source != "cursor" || d.Kind != "log" || d.Tags != "cursor" {
		t.Errorf("source/kind/tags = %q/%q/%q", d.Source, d.Kind, d.Tags)
	}
	if d.ProjectID == 0 {
		t.Error("ProjectID = 0, want the resolved CodeStat project")
	}
	if !strings.Contains(d.Title, "11111111") {
		t.Errorf("title = %q, want it to carry the composer id tail", d.Title)
	}
	// Ordering is by each bubble's createdAt, NOT by key or insertion order: the
	// fixture writes the first prompt third in table order.
	if !strings.Contains(d.Content, "## 首条提问\n\nfirst prompt of the session") {
		t.Errorf("content does not lead with the earliest user turn:\n%s", d.Content)
	}
	if !strings.Contains(d.Content, "answer two — the last reply") {
		t.Errorf("content does not end with the latest assistant turn:\n%s", d.Content)
	}
	if strings.Contains(d.Content, "system-ish noise") {
		t.Error("unknown sender type leaked into the note")
	}
	if !strings.Contains(d.Content, wsFolder) {
		t.Error("note should name the workspace folder it came from")
	}
	if !strings.Contains(d.Content, "best-effort") {
		t.Error("note must say it is a best-effort excerpt, not a full transcript")
	}
	if strings.Contains(d.Content, "this-is-not-json") || strings.Contains(d.Content, "   \n") {
		t.Error("malformed/empty bubbles leaked")
	}
}

// The drift contract is the important half of "best effort": an unrecognized
// layout must be an ERROR, because "ok, zero notes" is indistinguishable from
// "this user has no Cursor sessions" and would hide breakage forever.
func TestImportFailsLoudlyWhenLayoutDrifts(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, path string)
		wantSub string
	}{
		{"missing composerHeaders table", func(t *testing.T, p string) {
			mustExec(t, p, "DROP TABLE composerHeaders")
		}, "composerHeaders"},
		{"missing cursorDiskKV table", func(t *testing.T, p string) {
			mustExec(t, p, "DROP TABLE cursorDiskKV")
		}, "cursorDiskKV"},
		{"renamed sender column", func(t *testing.T, p string) {
			// Rebuild the header table without isSubagent, as a future Cursor
			// version might.
			mustExec(t, p, "ALTER TABLE composerHeaders DROP COLUMN isSubagent")
		}, "isSubagent"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, statePath := newFixture(t)
			tc.mutate(t, statePath)

			docs, err := New(app).Import()
			if err == nil {
				t.Fatalf("drifted layout returned no error (%d docs); it must fail loudly instead of importing nothing", len(docs))
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not name the missing %q", err, tc.wantSub)
			}
			if len(docs) != 0 {
				t.Errorf("a failed probe still returned %d docs", len(docs))
			}
		})
	}
}

// Not installing Cursor is not an error, and neither is an empty database.
func TestImportIsANoOpWithoutCursor(t *testing.T) {
	t.Setenv("CURSOR_STATE_DB", filepath.Join(t.TempDir(), "absent.db"))
	app, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	docs, err := New(app).Import()
	if err != nil {
		t.Fatalf("absent Cursor must not error: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("got %d docs from a nonexistent database", len(docs))
	}
}

func TestFolderPathFromWorkspaceJSON(t *testing.T) {
	cases := []struct {
		name, raw, want string
		ok              bool
	}{
		{"file uri", `{"folder":"file:///Users/dev/Workspace/Foo"}`, filepath.FromSlash("/Users/dev/Workspace/Foo"), true},
		{"windows uri drops leading slash", `{"folder":"file:///C:/dev/proj"}`, filepath.Clean("C:/dev/proj"), true},
		{"raw path accepted too", `{"folder":"/srv/app"}`, filepath.FromSlash("/srv/app"), true},
		{"missing folder key", `{"label":"x"}`, "", false},
		{"empty folder", `{"folder":"  "}`, "", false},
		{"not json", `{{{`, "", false},
		{"multi-root workspace has no folder string", `{"folder":123}`, "", false},
	}
	for _, tc := range cases {
		got, ok := folderPathFromWorkspaceJSON([]byte(tc.raw))
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("%s: path = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A session with only one side present is still real history.
func TestExcerptsTolerateMissingSide(t *testing.T) {
	app, statePath := newFixture(t)
	const lone = "55555555-5555-5555-5555-555555555555"
	st, err := sql.Open("sqlite", "file:"+statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Exec(`INSERT INTO composerHeaders
		(composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent)
		VALUES (?,?,?,0,0,0)`, lone, wsHashKnown, 1700009000000); err != nil {
		t.Fatal(err)
	}
	onlyUser := bubbleJSON(t, 1, "a question never answered", "2026-07-16T11:00:00.000Z")
	if _, err := st.Exec("INSERT INTO cursorDiskKV (key, value) VALUES (?, ?)",
		"bubbleId:"+lone+":ffff0001", onlyUser); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	docs, err := New(app).Import()
	if err != nil {
		t.Fatal(err)
	}
	// 3 docs now: the resolved fixture session, the one-sided session added here,
	// and the fixture's orphan workspace — which ADR-0023 makes the importer emit
	// with ProjectID==0 + RepoPath for the runtime to land (it used to be skipped).
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want the fixture session + the one-sided one + the to-be-landed orphan", len(docs))
	}
	var found bool
	for _, d := range docs {
		if strings.Contains(d.Title, "55555555") {
			found = true
			if !strings.Contains(d.Content, "a question never answered") {
				t.Errorf("one-sided session lost its only turn:\n%s", d.Content)
			}
			if strings.Contains(d.Content, "## 末条回复") {
				t.Error("a session with no reply must not render an empty reply section")
			}
		}
	}
	if !found {
		t.Fatal("the one-sided session was not imported")
	}
}

func mustExec(t *testing.T, path, stmt string) {
	t.Helper()
	st, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Exec(stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// TestImportAgainstRealInstall is an opt-in smoke test against the developer's
// own Cursor database (REPOTORY_REAL_CURSOR_DB=1). It exists because every other
// test in this file runs on a fixture I wrote from observation, and the whole
// point of ADR-0011's "don't guess at live formats" rule is that the observation
// has to be executed against occasionally.
//
// It asserts only counts and lengths and never a byte of transcript: a note
// importer's test must not become a way to dump someone's chat history into a
// test log.
func TestImportAgainstRealInstall(t *testing.T) {
	if os.Getenv("REPOTORY_REAL_CURSOR_DB") != "1" {
		t.Skip("set REPOTORY_REAL_CURSOR_DB=1 to run against this machine's Cursor install")
	}
	// No project seeding: every session is expected to skip for attribution, and
	// that is itself the assertion (the run completes, nothing is invented).
	app, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	statePath, ok := stateDBPath()
	if !ok {
		t.Skip("cannot resolve Cursor's config dir on this platform")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Skipf("no Cursor state database at %s", statePath)
	}

	st, err := memsrc.OpenReadOnly(statePath)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer st.Close()
	if err := probeLayout(st); err != nil {
		t.Fatalf("real install failed the layout probe (drift, and we would have imported nothing): %v", err)
	}

	var withProject, parsed int
	// Collect ids FIRST, then query bubbles: the read-only handle is capped at one
	// connection, so running excerpts() while these rows are still open would
	// deadlock (which is exactly what this test did before it was written this way
	// — Import() itself is safe for the same reason).
	var ids []string
	rows, err := st.Query(
		`SELECT composerId FROM composerHeaders
		  WHERE COALESCE(isSubagent, 0) = 0
		    AND COALESCE(workspaceId, '') NOT IN ('', 'empty-window')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close() //nolint:errcheck // reporting the failure below
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close() //nolint:errcheck // iteration completed
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		first, last, ok := (&Importer{db: app}).excerpts(st, id)
		if !ok {
			continue
		}
		parsed++
		if len(first)+len(last) == 0 {
			t.Errorf("composer %s parsed to empty excerpts", id[:8])
		}
	}
	composers := len(ids)

	docs, err := New(app).Import()
	if err != nil {
		t.Fatalf("import against the real install: %v", err)
	}
	withProject = len(docs)
	t.Logf("real Cursor install: %d attributable session(s) found, %d parsed to usable excerpts, %d imported with a project match",
		composers, parsed, withProject)
	if composers == 0 {
		t.Skip("this machine has no attributable Cursor sessions to assert against")
	}
	if parsed == 0 {
		t.Error("sessions exist but none parsed — the format has drifted; fix the importer before trusting it")
	}
	if withProject != 0 {
		t.Errorf("expected zero imported docs with an empty project table, got %d", withProject)
	}
}
