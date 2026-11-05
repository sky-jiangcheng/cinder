package integrity

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"repotory/internal/db"
)

// -- fixtures ---------------------------------------------------------------

// setupDB creates a fully migrated in-memory database: same InitDB path the app
// uses, so the fixtures exercise the real schema, the real migration stamps and
// the real FTS triggers. modernc.org/sqlite is pure Go, so this needs no CGO.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// mustExec writes to the test database. The integrity package itself never
// writes; fixtures have to, otherwise there would be nothing to detect.
func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// insertProject writes a project row directly, because the exported project
// helpers are transaction-scoped. collected mirrors the flag migration v5 added,
// and collected_at is stamped alongside it exactly as MarkProjectCollectedTx
// does - a collected project without a timestamp is itself a finding.
func insertProject(t *testing.T, database *sql.DB, name, root string, collected bool) int64 {
	t.Helper()
	res, err := database.Exec(
		"INSERT INTO projects (name, root_path, collected, collected_at) "+
			"VALUES (?, ?, ?, CASE WHEN ? = 1 THEN datetime('now') ELSE NULL END)",
		name, root, collected, collected)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

func insertRepository(t *testing.T, database *sql.DB, projectID int64, path string) int64 {
	t.Helper()
	res, err := database.Exec("INSERT INTO repositories (path, project_id) VALUES (?, ?)", path, projectID)
	if err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("repository id: %v", err)
	}
	return id
}

// setupDBWithRepo builds the minimum a completed scan produces: a scan root,
// one collected project and one indexed repository - with no notes, no todos
// and no mined knowledge yet.
func setupDBWithRepo(t *testing.T) (*sql.DB, int64, int64) {
	t.Helper()
	database := setupDB(t)
	if err := db.ReplaceScanRoots(database, []string{"/home/dev/code"}); err != nil {
		t.Fatalf("ReplaceScanRoots: %v", err)
	}
	projectID := insertProject(t, database, "repotory", "/home/dev/code/repotory", true)
	repoID := insertRepository(t, database, projectID, "/home/dev/code/repotory")
	return database, projectID, repoID
}

// seedHealthyDB builds the state a good database is in: a scan root, a
// collected project, an indexed repository, a note and a todo written through
// the service layer (so the FTS triggers fire on exactly the path the checks
// must agree with), and a fresh knowledge cache entry.
func seedHealthyDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	database, projectID, repoID := setupDBWithRepo(t)

	if _, err := db.CreateNoteEx(database, projectID, "Architecture", "the database is modelled in internal/db", "", "decision", "manual"); err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	if _, err := db.CreateTodo(database, projectID, "verify the integrity checks"); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if err := db.UpsertRepoMeta(database, repoID, `["Go"]`, "# Cinder", `{"Go": 100}`, "[]", "[]", "{}"); err != nil {
		t.Fatalf("UpsertRepoMeta: %v", err)
	}
	return database, projectID
}

// -- assertions -------------------------------------------------------------

func checkOf(t *testing.T, c Check, want Status) Check {
	t.Helper()
	if c.Status != want {
		t.Fatalf("check %s: status = %q, want %q (detail: %s, error: %s)", c.Name, c.Status, want, c.Detail, c.Error)
	}
	return c
}

func wantMetric(t *testing.T, c Check, name string, want int64) {
	t.Helper()
	got, ok := c.Metric(name)
	if !ok {
		t.Fatalf("check %s reported no metric %q (metrics: %v)", c.Name, name, c.Metrics)
	}
	if got != want {
		t.Fatalf("check %s metric %s = %d, want %d (detail: %s)", c.Name, name, got, want, c.Detail)
	}
}

func wantOffender(t *testing.T, c Check, id int64) {
	t.Helper()
	for _, got := range c.Offenders {
		if got == id {
			return
		}
	}
	t.Fatalf("check %s did not report id %d among offenders %v (detail: %s)", c.Name, id, c.Offenders, c.Detail)
}

func wantSubstring(t *testing.T, c Check, sub string) {
	t.Helper()
	if !strings.Contains(c.Detail, sub) {
		t.Fatalf("check %s detail %q does not mention %q", c.Name, c.Detail, sub)
	}
}

// -- the whole report -------------------------------------------------------

func TestRunAllOnEmptyDatabase(t *testing.T) {
	// A brand new install is the one case where a health check is guaranteed to
	// find nothing: it must still produce six answers, none of them an error,
	// and none of them a false claim of knowledge.
	report := RunAll(setupDB(t))

	if report.Total != 6 {
		t.Fatalf("Total = %d, want 6", report.Total)
	}
	if report.Failed != 0 || report.Errored != 0 {
		t.Fatalf("empty database reported %d failure(s) and %d error(s): %+v", report.Failed, report.Errored, report.Checks)
	}
	if report.Score <= 0 || report.Score > 100 {
		t.Fatalf("Score = %v, want a percentage in (0, 100]", report.Score)
	}

	seen := map[string]bool{}
	for _, c := range report.Checks {
		seen[c.Name] = true
		if c.Detail == "" {
			t.Errorf("check %s returned no evidence sentence", c.Name)
		}
		if c.Title == "" {
			t.Errorf("check %s returned no title", c.Name)
		}
		if c.Status != StatusPass && c.Status != StatusWarn {
			t.Errorf("check %s = %q, want pass or warn on an empty database (detail: %s, error: %s)", c.Name, c.Status, c.Detail, c.Error)
		}
	}
	for _, name := range []string{
		CheckNameSchemaVersion, CheckNameFTSIndex, CheckNameOrphans,
		CheckNameCoverage, CheckNameCacheFresh, CheckNameNoteVersions,
	} {
		if !seen[name] {
			t.Errorf("RunAll did not run check %q", name)
		}
	}

	// Nothing was ever scanned, and the report has to say so instead of
	// claiming a healthy knowledge base.
	coverage, _ := report.Check(CheckNameCoverage)
	checkOf(t, coverage, StatusWarn)
	wantMetric(t, coverage, "projects", 0)
	wantMetric(t, coverage, "scan_roots", 0)
}

func TestRunAllOnHealthyDatabase(t *testing.T) {
	database, _ := seedHealthyDB(t)
	report := RunAll(database)

	if report.Failed != 0 || report.Errored != 0 || report.Warned != 0 {
		t.Fatalf("healthy database did not pass cleanly: %+v", report.Checks)
	}
	if report.Passed != report.Total {
		t.Fatalf("Passed = %d, want %d", report.Passed, report.Total)
	}
	if report.Score != 100 {
		t.Errorf("Score = %v, want 100", report.Score)
	}
	if !strings.Contains(report.Verdict, "all 6 checks pass") {
		t.Errorf("Verdict = %q, want it to report a clean pass", report.Verdict)
	}
}

func TestRunAllReturnsAResultWhenTablesAreMissing(t *testing.T) {
	// A database mid-migration, or truncated: the table the notes live in is
	// gone. Every check must still return a result - the ones that need the
	// table report their own error or finding, the ones that do not keep
	// answering - and the verdict must refuse to call the result clean.
	database := setupDB(t)
	mustExec(t, database, "PRAGMA foreign_keys=OFF")
	mustExec(t, database, "DROP TABLE project_notes")

	report := RunAll(database)
	if report.Total != 6 {
		t.Fatalf("Total = %d, want 6", report.Total)
	}
	if report.Errored == 0 {
		t.Fatalf("expected at least one errored check: %+v", report.Checks)
	}
	for _, c := range report.Checks {
		if c.Detail == "" {
			t.Errorf("check %s returned no evidence sentence", c.Name)
		}
	}
	// The schema check does not error out on a missing table: it names what is
	// missing, which is more useful than "could not check".
	schema := mustGet(t, report, CheckNameSchemaVersion)
	checkOf(t, schema, StatusFail)
	wantSubstring(t, schema, "project_notes.title")
	checkOf(t, mustGet(t, report, CheckNameOrphans), StatusError)
	checkOf(t, mustGet(t, report, CheckNameCacheFresh), StatusPass)
	if !strings.Contains(report.Verdict, "lower bound") {
		t.Errorf("Verdict = %q, want it to say the score is only a lower bound", report.Verdict)
	}
	if report.Score >= 100 {
		t.Errorf("Score = %v, want < 100 when a check could not run", report.Score)
	}
}

func mustGet(t *testing.T, r *Report, name string) Check {
	t.Helper()
	c, ok := r.Check(name)
	if !ok {
		t.Fatalf("report has no check %q", name)
	}
	return c
}

func TestGuardedAbsorbsPanicAndNilHandle(t *testing.T) {
	panicked := guarded(setupDB(t), func(*sql.DB) Check { panic("boom") }, "panic_test", "Panic test")
	checkOf(t, panicked, StatusError)
	if !strings.Contains(panicked.Error, "boom") {
		t.Errorf("Error = %q, want it to carry the panic value", panicked.Error)
	}
	if panicked.Name != "panic_test" || panicked.Title != "Panic test" {
		t.Errorf("panic result lost its identity: %+v", panicked)
	}

	nilDB := CheckSchemaVersion(nil)
	checkOf(t, nilDB, StatusError)
	if nilDB.Name != CheckNameSchemaVersion {
		t.Errorf("nil-handle result lost its name: %+v", nilDB)
	}
}

func TestReportScoreMath(t *testing.T) {
	// Score weights: pass 1, warn 0.5, fail/error 0.
	r := &Report{Checks: []Check{
		{Status: StatusPass}, {Status: StatusPass},
		{Status: StatusWarn}, {Status: StatusFail}, {Status: StatusError},
	}}
	r.summarize()
	if r.Total != 5 || r.Passed != 2 || r.Warned != 1 || r.Failed != 1 || r.Errored != 1 {
		t.Fatalf("unexpected counts: %+v", r)
	}
	if r.Score != 50 {
		t.Errorf("Score = %v, want 50", r.Score)
	}
	if !strings.Contains(r.Verdict, "lower bound") {
		t.Errorf("Verdict = %q, want the errored check to lower confidence", r.Verdict)
	}

	empty := &Report{}
	empty.summarize()
	if empty.Score != 0 || empty.Verdict != "no checks ran" {
		t.Errorf("empty report summarized to %+v", empty)
	}
}

// -- 1. schema version ------------------------------------------------------

func TestSchemaVersion(t *testing.T) {
	database := setupDB(t)

	healthy := checkOf(t, CheckSchemaVersion(database), StatusPass)
	wantMetric(t, healthy, "current", int64(ExpectedSchemaVersion))
	wantMetric(t, healthy, "expected", int64(ExpectedSchemaVersion))
	if _, ok := healthy.Metric("pending"); ok {
		t.Errorf("a current database reported a pending migration count: %v", healthy.Metrics)
	}

	mustExec(t, database, "UPDATE app_config SET value = '5' WHERE key = 'schema_version'")
	behind := checkOf(t, CheckSchemaVersion(database), StatusFail)
	wantMetric(t, behind, "pending", int64(ExpectedSchemaVersion-5))
	wantSubstring(t, behind, "migration(s) have not run")

	mustExec(t, database, "UPDATE app_config SET value = '999' WHERE key = 'schema_version'")
	ahead := checkOf(t, CheckSchemaVersion(database), StatusWarn)
	wantMetric(t, ahead, "ahead_by", 999-ExpectedSchemaVersion)

	mustExec(t, database, "DELETE FROM app_config WHERE key = 'schema_version'")
	missing := checkOf(t, CheckSchemaVersion(database), StatusFail)
	wantMetric(t, missing, "current", 0)
	wantSubstring(t, missing, "no schema_version row")

	mustExec(t, database, "INSERT INTO app_config (key, value) VALUES ('schema_version', 'not-a-number')")
	garbage := checkOf(t, CheckSchemaVersion(database), StatusFail)
	wantSubstring(t, garbage, "not a version number")
}

func TestSchemaVersionVerifiesTheShapeNotJustTheStamp(t *testing.T) {
	// The version stamp is a claim about the schema, and migration v10 exists
	// because a real database once made a claim like this and failed every read
	// of the knowledge cache. Comparing numbers cannot see that; selecting the
	// columns can.
	database, _ := seedHealthyDB(t)

	intact := checkOf(t, CheckSchemaVersion(database), StatusPass)
	wantMetric(t, intact, "missing_shape", 0)
	wantSubstring(t, intact, "every required column is present")

	mustExec(t, database, "PRAGMA foreign_keys=OFF")
	mustExec(t, database, "ALTER TABLE repo_meta DROP COLUMN dependencies")
	mustExec(t, database, "ALTER TABLE repo_meta DROP COLUMN top_contributors")
	mustExec(t, database, "ALTER TABLE repo_meta DROP COLUMN activity")

	drifted := checkOf(t, CheckSchemaVersion(database), StatusFail)
	wantMetric(t, drifted, "current", int64(ExpectedSchemaVersion))
	wantMetric(t, drifted, "missing_shape", 3)
	wantSubstring(t, drifted, "repo_meta.dependencies")
	wantSubstring(t, drifted, "recorded without being applied")

	// A missing table is described the same way rather than crashing the probe.
	mustExec(t, database, "DROP TABLE note_versions")
	gone := checkOf(t, CheckSchemaVersion(database), StatusFail)
	wantSubstring(t, gone, "note_versions.note_id")
}

// migrationIDPattern matches the id field of every migration entry in
// internal/db/migrate.go.
var migrationIDPattern = regexp.MustCompile(`\{\s*id:\s*(\d+),`)

// TestExpectedSchemaVersionMatchesMigrations keeps the duplicated constant
// honest. It is the one thing in this package that can silently make the report
// lie, and a forgotten bump is exactly the kind of bug that survives review.
func TestExpectedSchemaVersionMatchesMigrations(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "db", "migrate.go"))
	if err != nil {
		t.Fatalf("read internal/db/migrate.go: %v", err)
	}
	highest := 0
	for _, m := range migrationIDPattern.FindAllStringSubmatch(string(src), -1) {
		id, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("unparsable migration id %q: %v", m[1], err)
		}
		if id > highest {
			highest = id
		}
	}
	if highest == 0 {
		t.Fatal("no migration ids found in internal/db/migrate.go: the pattern needs updating")
	}
	if highest != ExpectedSchemaVersion {
		t.Fatalf("internal/db has migrations up to v%d but ExpectedSchemaVersion = %d: bump it with the migration list", highest, ExpectedSchemaVersion)
	}
}

// -- 2. FTS index integrity -------------------------------------------------

func TestFTSIndexInSync(t *testing.T) {
	database, _ := seedHealthyDB(t)
	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusPass)
	wantMetric(t, c, "notes_rows", 1)
	wantMetric(t, c, "notes_indexed", 1)
	wantMetric(t, c, "notes_missing", 0)
	wantMetric(t, c, "notes_extra", 0)
	wantMetric(t, c, "todos_rows", 1)
	wantMetric(t, c, "todos_missing", 0)
	wantMetric(t, c, "notes_sync_triggers", 3)
	wantMetric(t, c, "todos_sync_triggers", 3)
	wantSubstring(t, c, "every sync trigger is in place")
}

func TestFTSIndexWithoutSyncTriggersIsNotHealthy(t *testing.T) {
	// A rowid comparison is structurally blind to a stale index: with the update
	// trigger gone, an edited note keeps its rowid and its old text, so every
	// count in the drift report stays perfect while search matches words that
	// no longer exist. The triggers are the mechanism, so they are checked.
	database, projectID := seedHealthyDB(t)
	mustExec(t, database, "DROP TRIGGER project_notes_fts_au")
	if _, err := db.CreateNoteEx(database, projectID, "Doc", "ORIGINAL alpha", "", "other", "manual"); err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	mustExec(t, database, "UPDATE project_notes SET content = 'REPLACED beta' WHERE title = 'Doc'")

	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusFail)
	wantMetric(t, c, "notes_sync_triggers", 2)
	wantMetric(t, c, "notes_missing", 0)
	wantMetric(t, c, "notes_extra", 0)
	wantMetric(t, c, "todos_sync_triggers", 3)
	wantSubstring(t, c, "project_notes_fts_au missing")
	wantSubstring(t, c, "stale search terms")
}

func TestFTSIndexWithoutAnySyncTriggers(t *testing.T) {
	database, _ := seedHealthyDB(t)
	for _, name := range []string{"project_notes_fts_ai", "project_notes_fts_ad", "project_notes_fts_au"} {
		mustExec(t, database, "DROP TRIGGER "+name)
	}

	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusFail)
	wantMetric(t, c, "notes_sync_triggers", 0)
	wantSubstring(t, c, "found 0 of 3")
}

func TestFTSIndexDetectsMissingNote(t *testing.T) {
	// The source row stays and the index entry disappears: the exact shape of
	// drift a broken trigger, a restore from a pre-v7 backup or a hand-written
	// row leaves behind. The note still exists, it is just invisible to search.
	database, projectID := seedHealthyDB(t)
	note, err := db.CreateNoteEx(database, projectID, "Drifted", "searchable content that the index lost", "", "other", "manual")
	if err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	mustExec(t, database, "DELETE FROM project_notes_fts WHERE rowid = ?", note.ID)

	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusFail)
	wantMetric(t, c, "notes_missing", 1)
	wantMetric(t, c, "notes_rows", 2)
	wantMetric(t, c, "notes_indexed", 1)
	wantMetric(t, c, "todos_missing", 0)
	wantOffender(t, c, note.ID)
	wantSubstring(t, c, "search will not return them")
}

func TestFTSIndexDetectsMissingTodo(t *testing.T) {
	database, projectID := seedHealthyDB(t)
	todo, err := db.CreateTodo(database, projectID, "todo that leaves the index")
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	mustExec(t, database, "DELETE FROM project_todos_fts WHERE rowid = ?", todo.ID)

	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusFail)
	wantMetric(t, c, "todos_missing", 1)
	wantMetric(t, c, "notes_missing", 0)
	wantOffender(t, c, todo.ID)
}

func TestFTSIndexDetectsExtraRowid(t *testing.T) {
	// The other direction: an indexed rowid with no source row. Search matches
	// it, the join to project_notes then drops it, so the index is carrying
	// dead weight that skews nothing visible - exactly why it needs a check.
	database, _ := seedHealthyDB(t)
	mustExec(t, database, "INSERT INTO project_notes_fts(rowid, title, content) VALUES (9999, 'ghost', 'ghost body')")

	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusFail)
	wantMetric(t, c, "notes_extra", 1)
	wantOffender(t, c, 9999)
}

func TestFTSIndexIgnoresRowsThatLegitimatelyHaveNoTokens(t *testing.T) {
	// Anti-false-positive guard: an empty body, and a NULL title left behind by
	// a pre-v2 row, are valid states that still occupy an index rowid. A check
	// that reported those as drift would train the user to ignore it.
	database, projectID := seedHealthyDB(t)
	mustExec(t, database, "INSERT INTO project_notes (project_id, title, content) VALUES (?, NULL, '')", projectID)
	mustExec(t, database, "INSERT INTO project_notes (project_id, title, content) VALUES (?, 'empty body', '')", projectID)

	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusPass)
	wantMetric(t, c, "notes_rows", 3)
	wantMetric(t, c, "notes_indexed", 3)
}

func TestMissingFTSIndexIsAFindingNotAnError(t *testing.T) {
	// A database that never reached migration v7 has no index at all. Search
	// still works (it falls back to LIKE), so nothing else in the product would
	// ever say so; this check must report it as broken data, not as "could not
	// check".
	database, _ := seedHealthyDB(t)
	mustExec(t, database, "DROP TABLE project_notes_fts")

	c := checkOf(t, CheckFTSIndexIntegrity(database), StatusFail)
	wantSubstring(t, c, "is missing")
	wantSubstring(t, c, "never migrated to v7")
}

// -- 3. orphan rows ---------------------------------------------------------

func TestOrphanRowsClean(t *testing.T) {
	database, _ := seedHealthyDB(t)
	// A repository with no project yet is legitimate: project_id is nullable
	// and a repo can be indexed before the grouper assigns it.
	mustExec(t, database, "INSERT INTO repositories (path, project_id) VALUES ('/home/dev/code/loose', NULL)")

	c := checkOf(t, CheckOrphanRows(database), StatusPass)
	wantMetric(t, c, "notes_orphans", 0)
	wantMetric(t, c, "todos_orphans", 0)
	wantMetric(t, c, "repositories_orphans", 0)
	// A NULL project_id is not an orphan, but it is still visible in the report.
	wantMetric(t, c, "repositories_unassigned", 1)
	wantSubstring(t, c, "not counted as orphans")
}

func TestRepositoriesWithNoProjectAtAll(t *testing.T) {
	// "No dangling project_id" is technically true here and completely
	// useless: not one repository can be traced back to a project, so nothing
	// in the knowledge base can answer what a repository belongs to.
	database, _ := seedHealthyDB(t)
	mustExec(t, database, "UPDATE repositories SET project_id = NULL")

	c := checkOf(t, CheckOrphanRows(database), StatusWarn)
	wantMetric(t, c, "repositories_orphans", 0)
	wantMetric(t, c, "repositories_unassigned", 1)
	wantSubstring(t, c, "none of the 1 repositories is attached to a project")
}

func TestOrphanRowsDetected(t *testing.T) {
	database, _ := seedHealthyDB(t)
	// InitDB turns foreign keys on, so dirty rows can only be written with them
	// off - which is how they really get in (a restore, a manual fix, an old
	// build). The notes and todos still fire their FTS triggers, so this is a
	// referential problem and not index drift.
	mustExec(t, database, "PRAGMA foreign_keys=OFF")
	mustExec(t, database, "INSERT INTO project_notes (project_id, content) VALUES (4242, 'orphan note')")
	mustExec(t, database, "INSERT INTO project_todos (project_id, title) VALUES (4242, 'orphan todo')")
	mustExec(t, database, "INSERT INTO repositories (path, project_id) VALUES ('/tmp/ghost', 4242)")

	c := checkOf(t, CheckOrphanRows(database), StatusFail)
	wantMetric(t, c, "notes_orphans", 1)
	wantMetric(t, c, "todos_orphans", 1)
	wantMetric(t, c, "repositories_orphans", 1)
	if len(c.Offenders) != 3 {
		t.Errorf("Offenders = %v, want the 3 dirty row ids", c.Offenders)
	}
	wantSubstring(t, c, "dangling project_id")

	// The drift check must stay independent of the referential one.
	checkOf(t, CheckFTSIndexIntegrity(database), StatusPass)
}

// -- 4. scan coverage -------------------------------------------------------

func TestScanCoverage(t *testing.T) {
	t.Run("empty knowledge base", func(t *testing.T) {
		c := checkOf(t, CheckScanCoverage(setupDB(t)), StatusWarn)
		wantMetric(t, c, "coverage_percent", 0)
	})

	t.Run("partially collected", func(t *testing.T) {
		database := setupDB(t)
		if err := db.ReplaceScanRoots(database, []string{"/home/dev/code"}); err != nil {
			t.Fatalf("ReplaceScanRoots: %v", err)
		}
		collected := insertProject(t, database, "a", "/home/dev/code/a", true)
		insertProject(t, database, "b", "/home/dev/code/b", false)
		insertProject(t, database, "c", "/home/dev/code/c", false)
		insertRepository(t, database, collected, "/home/dev/code/a")

		c := checkOf(t, CheckScanCoverage(database), StatusWarn)
		wantMetric(t, c, "projects", 3)
		wantMetric(t, c, "collected", 1)
		wantMetric(t, c, "uncollected", 2)
		wantMetric(t, c, "coverage_percent", 33)
		wantSubstring(t, c, "never collected")
	})

	t.Run("fully collected", func(t *testing.T) {
		database, _ := seedHealthyDB(t)
		c := checkOf(t, CheckScanCoverage(database), StatusPass)
		wantMetric(t, c, "coverage_percent", 100)
		wantMetric(t, c, "scan_roots", 1)
		wantMetric(t, c, "repositories", 1)
	})

	t.Run("collected but nothing indexed", func(t *testing.T) {
		database := setupDB(t)
		if err := db.ReplaceScanRoots(database, []string{"/home/dev/code"}); err != nil {
			t.Fatalf("ReplaceScanRoots: %v", err)
		}
		insertProject(t, database, "a", "/home/dev/code/a", true)

		c := checkOf(t, CheckScanCoverage(database), StatusWarn)
		wantSubstring(t, c, "no repository was indexed")
	})

	t.Run("collected without a timestamp", func(t *testing.T) {
		// MarkProjectCollectedTx stamps collected_at with the flag, so a
		// collected project without one had its claim written by something else
		// and the report cannot even say when it was collected.
		database, _ := seedHealthyDB(t)
		mustExec(t, database, "UPDATE projects SET collected_at = NULL")

		c := checkOf(t, CheckScanCoverage(database), StatusWarn)
		wantMetric(t, c, "collected_without_timestamp", 1)
		wantSubstring(t, c, "cannot be dated")
	})
}

// -- 5. knowledge cache freshness -------------------------------------------

func TestKnowledgeCacheFreshness(t *testing.T) {
	t.Run("fresh and complete", func(t *testing.T) {
		database, _ := seedHealthyDB(t)
		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusPass)
		wantMetric(t, c, "meta_rows", 1)
		wantMetric(t, c, "covered", 1)
		wantMetric(t, c, "coverage_percent", 100)
		wantMetric(t, c, "orphaned_meta", 0)
		if days, _ := c.Metric("oldest_age_days"); days < 0 || days > 1 {
			t.Errorf("oldest_age_days = %d, want a fresh entry", days)
		}
	})

	t.Run("empty knowledge base", func(t *testing.T) {
		c := checkOf(t, CheckKnowledgeCacheFreshness(setupDB(t)), StatusPass)
		wantMetric(t, c, "meta_rows", 0)
		wantSubstring(t, c, "empty by definition")
	})

	t.Run("nothing mined yet", func(t *testing.T) {
		// The cache was never built, so the AI gets no repo context at all.
		database, _, _ := setupDBWithRepo(t)
		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		wantMetric(t, c, "repositories", 1)
		wantMetric(t, c, "meta_rows", 0)
		wantMetric(t, c, "coverage_percent", 0)
		wantSubstring(t, c, "no repo-level context yet")
	})

	t.Run("partially mined", func(t *testing.T) {
		database, projectID := seedHealthyDB(t)
		// A repository the miner has not reached: a gap, not corruption.
		insertRepository(t, database, projectID, "/home/dev/code/unmined")

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		wantMetric(t, c, "repositories", 2)
		wantMetric(t, c, "covered", 1)
		wantMetric(t, c, "coverage_percent", 50)
		wantSubstring(t, c, "covers 1 of 2 repositories")
	})

	t.Run("stale", func(t *testing.T) {
		database, _ := seedHealthyDB(t)
		mustExec(t, database, "UPDATE repo_meta SET updated_at = datetime('now', '-90 days')")

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		if days, _ := c.Metric("oldest_age_days"); days < StaleAfterDays {
			t.Fatalf("oldest_age_days = %d, want at least %d", days, StaleAfterDays)
		}
		wantSubstring(t, c, "quoting outdated facts")
	})

	t.Run("cache rows that hold no knowledge", func(t *testing.T) {
		// A repo_meta row still carrying the column defaults is a mining run
		// that produced nothing. Counting it as coverage would report a 100%
		// covered cache while the AI is served empty strings.
		database, _, repoID := setupDBWithRepo(t)
		mustExec(t, database, "INSERT INTO repo_meta (repository_id) VALUES (?)", repoID)

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		wantMetric(t, c, "meta_rows", 1)
		wantMetric(t, c, "empty_meta", 1)
		wantMetric(t, c, "covered", 0)
		wantMetric(t, c, "coverage_percent", 0)
		wantSubstring(t, c, "empty defaults")
	})

	t.Run("empty rows do not count as coverage", func(t *testing.T) {
		// One mined repository, one row that says nothing: coverage is the
		// mined one only, so the report cannot read as 100%.
		database, projectID, repoID := setupDBWithRepo(t)
		if err := db.UpsertRepoMeta(database, repoID, `["Go"]`, "", "{}", "[]", "[]", "{}"); err != nil {
			t.Fatalf("UpsertRepoMeta: %v", err)
		}
		empty := insertRepository(t, database, projectID, "/home/dev/code/unmined")
		mustExec(t, database, "INSERT INTO repo_meta (repository_id) VALUES (?)", empty)

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		wantMetric(t, c, "repositories", 2)
		wantMetric(t, c, "covered", 1)
		wantMetric(t, c, "empty_meta", 1)
		wantMetric(t, c, "coverage_percent", 50)
	})

	t.Run("timestamps in the future", func(t *testing.T) {
		// Every age in this check is measured from updated_at, so a stamp from
		// next week does not mean "very fresh" - it means "not a real scan
		// time", and reporting it as fresh is how a wrong clock hides.
		database, _ := seedHealthyDB(t)
		mustExec(t, database, "UPDATE repo_meta SET updated_at = datetime('now', '+10 days')")

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		wantMetric(t, c, "future_meta", 1)
		wantSubstring(t, c, "stamped in the future")
	})

	t.Run("unreadable timestamps", func(t *testing.T) {
		database, _ := seedHealthyDB(t)
		mustExec(t, database, "UPDATE repo_meta SET updated_at = 'not a date'")

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		wantSubstring(t, c, "no readable updated_at")
	})

	t.Run("orphaned cache entry", func(t *testing.T) {
		database, _ := seedHealthyDB(t)
		mustExec(t, database, "PRAGMA foreign_keys=OFF")
		mustExec(t, database, "INSERT INTO repo_meta (repository_id, tech_stack) VALUES (4242, '[\"Go\"]')")

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusWarn)
		wantMetric(t, c, "orphaned_meta", 1)
		wantSubstring(t, c, "no longer exists")
	})

	t.Run("every cache entry is orphaned", func(t *testing.T) {
		database, _ := seedHealthyDB(t)
		// The repository the cache describes is gone and the cache entry is
		// not: the AI would quote knowledge about a deleted project.
		mustExec(t, database, "PRAGMA foreign_keys=OFF")
		mustExec(t, database, "DELETE FROM repositories WHERE path = ?", "/home/dev/code/repotory")

		c := checkOf(t, CheckKnowledgeCacheFreshness(database), StatusFail)
		wantMetric(t, c, "repositories", 0)
		wantMetric(t, c, "meta_rows", 1)
		wantMetric(t, c, "orphaned_meta", 1)
		wantSubstring(t, c, "projects that are gone")
	})
}

// -- 6. note version snapshots ----------------------------------------------

func TestNoteVersionOrphans(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		database, projectID := seedHealthyDB(t)
		note, err := db.CreateNoteEx(database, projectID, "Versioned", "first revision", "", "other", "manual")
		if err != nil {
			t.Fatalf("CreateNoteEx: %v", err)
		}
		// Migration v11's trigger snapshots the old content on a real edit.
		if err := db.UpdateNoteFull(database, note.ID, "second revision", "Versioned", "", "other", false); err != nil {
			t.Fatalf("UpdateNoteFull: %v", err)
		}

		c := checkOf(t, CheckNoteVersionOrphans(database), StatusPass)
		wantMetric(t, c, "snapshots", 1)
		wantMetric(t, c, "orphans", 0)
	})

	t.Run("orphaned snapshot", func(t *testing.T) {
		database, _ := seedHealthyDB(t)
		// note_versions.note_id is ON DELETE CASCADE, so a surviving snapshot
		// means the note vanished with foreign keys off (or from a restore).
		mustExec(t, database, "PRAGMA foreign_keys=OFF")
		mustExec(t, database, "INSERT INTO note_versions (note_id, content) VALUES (4242, 'snapshot of a deleted note')")

		c := checkOf(t, CheckNoteVersionOrphans(database), StatusFail)
		wantMetric(t, c, "orphans", 1)
		wantMetric(t, c, "snapshots", 1)
		wantOffender(t, c, 4242)
		wantSubstring(t, c, "unreachable from the UI")
	})
}

// -- the read-only guarantee ------------------------------------------------

// writeKeywords are SQL verbs this package has no business issuing.
var writeKeywords = []string{"insert", "update", "delete", "drop", "create", "alter", "replace", "pragma", "attach", "vacuum", "begin", "commit", "rollback", "reindex", "analyze"}

// writeStatementPattern anchors the verbs on a word boundary so a Detail line
// that merely starts with an English word ("Deleted notes are ...") is not
// mistaken for SQL.
var writeStatementPattern = regexp.MustCompile(`(?i)^\s*(` + strings.Join(writeKeywords, "|") + `)\b`)

// writeMethods are the database/sql entry points that can change a database.
var writeMethods = map[string]bool{"Exec": true, "Begin": true, "BeginTx": true, "Prepare": true}

// TestSourceIsReadOnly fails the build if a check ever grows a write. The whole
// promise of this package is that a trust report can be taken against a live
// database without touching it, and a comment cannot guarantee that - only the
// absence of writes in the source can. Tests are excluded on purpose: the
// fixtures have to write dirty rows to have something to detect.
func TestSourceIsReadOnly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no sources found: the test is not looking at the package")
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && writeMethods[sel.Sel.Name] {
					t.Errorf("%s: calls %s, which can write", name, sel.Sel.Name)
				}
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			statement, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			// A multi-line SQL string is compared on its first keyword.
			statement = strings.TrimSpace(strings.SplitN(statement, "\n", 2)[0])
			if writeStatementPattern.MatchString(statement) {
				t.Errorf("%s: statement literal starts with a write verb: %q", name, statement)
			}
			return true
		})
	}
}
