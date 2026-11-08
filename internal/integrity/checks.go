package integrity

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// schemaVersionKey mirrors internal/db's unexported migrationVersionKey. The
// duplication is deliberate: the migration machinery in internal/db is
// unexported by design, and a duplicate constant is far cheaper than exporting
// migration internals just to let a health check read one row.
const schemaVersionKey = "schema_version"

// ExpectedSchemaVersion is the highest migration id in internal/db/migrate.go.
// It is duplicated for the same reason as schemaVersionKey and must be bumped
// together with the migration list. A forgotten bump is the one failure mode
// that makes this check lie, so TestExpectedSchemaVersionMatchesMigrations
// parses internal/db/migrate.go and fails when the two drift apart.
//
// v14 added the incremental-embedding queue (note_embed_dirty + its three
// project_notes triggers, internal/db/embed_dirty.go).
// v15 turned startup auto-import OFF for existing databases (ADR-0014 W0).
// v16 added the wiki layer (wiki_pages / page_links / note_pages /
// note_repositories, internal/db/wiki.go) — purely additive, reversible by drop.
// v17 added the page search index (wiki_pages_fts + its three triggers).
// v18 added the page review state (wiki_pages.status / .source, ADR-0015), the
// prerequisite for letting a model write into the layer at all.
// v19 added the async compile-job table (ADR-0016), so a long LLM-backed batch no
// longer has to be a synchronous HTTP call.
// v20 added compile_jobs.kind, turning that table into the general long-task
// queue ADR-0016 待决 ① asked for (the lint model pass joins it).
// v21 constrained page_links.relation to an eight-type vocabulary (ADR-0018
// lane 1), rebuilding the table so the graph can be queried by relation.
// v22 added page version snapshots (page_versions + the approve/mutate writer
// and the two append-only guards, internal/db/wiki_versions.go, ADR-0021), so
// "what exactly did a human approve" is answerable for pages as it already was
// for notes. It deliberately carries no foreign key: SQLite fires row triggers
// for FK CASCADE actions, so cascading out of a page delete would make deleting
// a project fail against the append-only guard.
// v23 added the page vector space's incremental queue (page_embed_dirty + its
// three wiki_pages triggers, internal/db/page_embed_dirty.go, ADR-0018 lane 2),
// so approved pages get embedded/re-embedded and un-approved or deleted pages get
// their vectors removed. The vec0 table itself (page_embeddings, pagevec.go) is
// created lazily by the service rebuild once the embedding dim is known, exactly
// like note_embeddings; the queue is part of the wiki layer's derived structure
// and is dropped/restored with it.
// v24 added projects.project_kind (scanned / import_placeholder / default) and
// seeded the single "Uncategorized" default landing project (internal/db's
// EnsureProjectKind, ADR-0023), so an external-knowledge import that matches no
// project lands a note on a placeholder or the default instead of silently
// dropping it. Additive: one ALTERed column with a constant default + one
// idempotent seed, enum validated in Go (no CHECK rebuild).
// v28 added the AI call audit log (internal/db's EnsureAICallLog, ADR-0030):
// one new table with append-only trigger guards and no foreign key. It records
// the evidence of an outbound model call (when, endpoint, model, surface,
// sizes, review verdicts) and — by design — not the prompt or the reply text,
// because writing those into the knowledge base would pollute it and pull API
// keys into the same SQLite file the notes live in. Purely additive: no
// existing table is touched.
// v29 added isolated chat_threads / chat_messages with immutable message IDs.
// v30 added review_runs / review_findings (internal/db's EnsureReviewSchema,
// ADR-0053): two new tables, no foreign keys, and a UNIQUE(run_date, scope) that
// makes "one report per day per scope" a schema fact rather than a convention.
// Purely additive — no existing table is touched.
const ExpectedSchemaVersion = 30

// StaleAfterDays is the age at which the repo_meta knowledge cache is reported
// as stale. The cache feeds generated knowledge (tech stack, readme excerpt,
// contributors) that an AI quotes as fact, so the line between "fresh" and
// "possibly wrong" has to live in code where a user can argue with it.
const StaleAfterDays = 30

// ftsIndex pairs a search index with the table it shadows.
type ftsIndex struct {
	label  string // metric prefix, e.g. "notes"
	index  string // the FTS5 table
	source string // its external-content table
}

// ftsIndexes are the two external-content FTS5 indexes created by migration v7.
//
// Drift is measured against the docsize shadow table, never against the FTS
// table itself. project_notes_fts is declared with content='project_notes', and
// an FTS5 query that carries no MATCH constraint is answered by scanning that
// content table. That is precisely why the drift is silent: asking the index
// whether it is complete gets the source table's own answer back. The docsize
// shadow table is the only read-only view of the rowids FTS5 actually indexed,
// so it is what both directions of the comparison must use.
var ftsIndexes = []ftsIndex{
	{label: "notes", index: "project_notes_fts", source: "project_notes"},
	{label: "todos", index: "project_todos_fts", source: "project_todos"},
}

// ftsSyncTriggers are the trigger suffixes that keep an index in step with its
// source table, created alongside the index by migration v7.
//
// A rowid comparison cannot see a stale *index*: drop the update trigger, edit
// a note, and the rowid is still indexed with the words it had before the edit,
// so every count in the drift report stays perfectly healthy while search
// matches text that no longer exists. The triggers are therefore probed
// separately - they are the mechanism, and the docsize comparison is only as
// trustworthy as the mechanism behind it.
var ftsSyncTriggers = []string{"_ai", "_ad", "_au"}

// requiredShape is the (table, column) set that the migration list promises.
//
// A version stamp records that somebody believed the migrations ran; selecting
// the column is what proves they did. Migration v10 exists precisely because a
// real database once carried a current-looking stamp with a repo_meta three
// migrations behind, and every read of the knowledge cache failed. Reading a
// number out of app_config cannot detect that, so the schema check verifies the
// shape as well as the stamp. Each entry is a column the running code reads on
// a hot path, tagged with the migration that introduced it.
var requiredShape = []struct {
	table  string
	column string
}{
	{"projects", "is_starred"},        // v1
	{"projects", "collected"},         // v5
	{"projects", "collected_at"},      // v5
	{"project_notes", "title"},        // v2
	{"project_notes", "tags"},         // v2
	{"project_notes", "kind"},         // v2
	{"project_notes", "pinned"},       // v2
	{"project_notes", "source"},       // v2
	{"daily_stats", "commits"},        // v9
	{"note_versions", "note_id"},      // v8
	{"repo_meta", "dependencies"},     // v10
	{"repo_meta", "top_contributors"}, // v10
	{"repo_meta", "activity"},         // v10
	{"chat_threads", "id"},            // v29
	{"chat_threads", "title"},
	{"chat_threads", "creation_title"},
	{"chat_threads", "path"},
	{"chat_threads", "created_at"},
	{"chat_threads", "updated_at"},
	{"chat_messages", "seq"},
	{"chat_messages", "id"},
	{"chat_messages", "thread_id"},
	{"chat_messages", "role"},
	{"chat_messages", "content"},
	{"chat_messages", "source"},
	{"chat_messages", "reply_to"},
	{"chat_messages", "created_at"},
}

// substantiveMetaSQL is the predicate for a repo_meta row that carries actual
// knowledge. A row that exists but still holds the column defaults ('[]', ”,
// '{}') is a mining run that produced nothing, and counting it as coverage
// would report a full cache while the AI is handed empty strings.
const substantiveMetaSQL = `(
	TRIM(COALESCE(tech_stack, '')) NOT IN ('', '[]')
	OR TRIM(COALESCE(readme_excerpt, '')) != ''
	OR TRIM(COALESCE(languages, '')) NOT IN ('', '{}')
)`

// CheckSchemaVersion compares the version stamped in app_config by internal/db's
// migration runner against the version this binary expects, and then verifies
// that the schema actually has the shape that version promises.
func CheckSchemaVersion(db *sql.DB) Check {
	return guarded(db, checkSchemaVersion, CheckNameSchemaVersion, "Schema version")
}

// readSchemaVersion returns the raw stored stamp and whether the row exists.
func readSchemaVersion(db *sql.DB) (string, bool, error) {
	var raw string
	err := db.QueryRow("SELECT value FROM app_config WHERE key = ?", schemaVersionKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(raw), true, nil
}

// missingShape probes every required (table, column) pair and returns the ones
// the database does not have. COUNT(column) is the cheapest read-only probe that
// fails at prepare time when the column is gone, and it also covers the table
// itself being absent. A missing table is not an error here: a database missing
// a whole table is exactly what this probe is meant to describe.
func missingShape(db *sql.DB) ([]string, error) {
	var missing []string
	for _, shape := range requiredShape {
		var probe int64
		err := db.QueryRow("SELECT COUNT(" + shape.column + ") FROM " + shape.table).Scan(&probe)
		if err == nil {
			continue
		}
		if isMissingColumn(err) || isMissingTable(err) {
			missing = append(missing, shape.table+"."+shape.column)
			continue
		}
		return nil, err
	}
	return missing, nil
}

func checkSchemaVersion(db *sql.DB) Check {
	const name, title = CheckNameSchemaVersion, "Schema version"
	metrics := map[string]int64{"expected": ExpectedSchemaVersion}

	raw, hasRow, err := readSchemaVersion(db)
	if err != nil {
		return checkErr(name, title, "could not read app_config.schema_version", err)
	}

	// Evidence before claims. A stamp that agrees with the code is only a
	// statement of intent; the columns are what the rest of the product will
	// actually read.
	missing, err := missingShape(db)
	if err != nil {
		return checkErr(name, title, "could not probe the schema shape", err)
	}
	metrics["missing_shape"] = int64(len(missing))

	current := 0
	if hasRow {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil {
			metrics["current"] = 0
			return Check{
				Name: name, Title: title, Status: StatusFail,
				Detail:  fmt.Sprintf("app_config.schema_version holds %q, which is not a version number", raw),
				Metrics: metrics,
				Error:   convErr.Error(),
			}
		}
		current = parsed
	} else {
		// No version row means a database that predates the migration
		// machinery, or one edited by hand. Nothing past v1 can be assumed, so
		// this is a failure - "unknown" must never be reported as "clean".
		metrics["pending"] = ExpectedSchemaVersion
	}
	metrics["current"] = int64(current)

	// A stamp at or above the build's expectation, with columns missing, means
	// the migrations were recorded but never applied - the failure mode
	// migration v10 had to repair by hand, and the one a version comparison
	// alone reports as healthy.
	//
	// A stamp *below* the expectation is a different story: the missing columns
	// are exactly what the pending migrations would have added, so accusing the
	// stamp of lying is both wrong and unhelpful. That case falls through to the
	// version branch below, which reports how far behind the database is and
	// lists the confirmed gaps as consequences rather than as corruption.
	if len(missing) > 0 && current >= ExpectedSchemaVersion {
		return Check{
			Name: name, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("app_config reports v%d, but the schema is missing %s: the version stamp does not match the actual tables, so a migration was recorded without being applied",
				current, strings.Join(missing, ", ")),
			Metrics: metrics,
		}
	}

	if !hasRow {
		return Check{
			Name: name, Title: title, Status: StatusFail,
			Detail:  "app_config holds no schema_version row: this database has never been migrated by the current build",
			Metrics: metrics,
		}
	}

	switch {
	case current == ExpectedSchemaVersion:
		return Check{
			Name: name, Title: title, Status: StatusPass,
			Detail:  fmt.Sprintf("database schema is at v%d, matching this build, and every required column is present", current),
			Metrics: metrics,
		}
	case current < ExpectedSchemaVersion:
		// Opening the database with the current build runs upgradeSchema, so a
		// version behind the code means the user is running this binary against
		// a file it has not migrated yet (a copy, a backup, a synced folder).
		pending := ExpectedSchemaVersion - current
		metrics["pending"] = int64(pending)
		detail := fmt.Sprintf("database is at v%d but this build expects v%d: %d migration(s) have not run, so newer columns and indexes may be missing",
			current, ExpectedSchemaVersion, pending)
		if len(missing) > 0 {
			// The gaps are a consequence of the pending migrations, not a
			// separate corruption - say so, but name them: a user deciding
			// whether to open the file with this build needs to know exactly
			// which columns are absent.
			detail += fmt.Sprintf(" (confirmed missing: %s)", strings.Join(missing, ", "))
		}
		return Check{
			Name: name, Title: title, Status: StatusFail,
			Detail:  detail,
			Metrics: metrics,
		}
	default:
		// Not a failure: a database migrated by a newer build is a normal state
		// after a downgrade, and every migration that did run was idempotent by
		// construction. It is still worth surfacing, because this binary cannot
		// vouch for what those newer migrations changed.
		metrics["ahead_by"] = int64(current - ExpectedSchemaVersion)
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("database is at v%d, ahead of this build's v%d: it was migrated by a newer version, and this binary cannot vouch for those changes",
				current, ExpectedSchemaVersion),
			Metrics: metrics,
		}
	}
}

// CheckFTSIndexIntegrity reports whether the search indexes still cover their
// source tables. This is the highest-value check in the package: a drifted
// index makes notes and todos quietly unsearchable, and every symptom reads as
// "the note does not exist" rather than as a bug.
func CheckFTSIndexIntegrity(db *sql.DB) Check {
	return guarded(db, checkFTSIndexIntegrity, CheckNameFTSIndex, "FTS search index")
}

// missingSyncTriggers returns the trigger names that should keep an index in
// step with its source table but are not there. They are read from sqlite_master
// because the drift comparison is structurally incapable of noticing that they
// are gone.
func missingSyncTriggers(db *sql.DB, index string) ([]string, error) {
	present := map[string]bool{}
	for _, suffix := range ftsSyncTriggers {
		present[index+suffix] = false
	}
	// Only compile-time constant names are bound, never user input.
	rows, err := db.Query(
		"SELECT name FROM sqlite_master WHERE type = 'trigger' AND name IN (?, ?, ?)",
		index+ftsSyncTriggers[0], index+ftsSyncTriggers[1], index+ftsSyncTriggers[2])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if _, known := present[name]; known {
			present[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var missing []string
	for _, suffix := range ftsSyncTriggers {
		if name := index + suffix; !present[name] {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

func checkFTSIndexIntegrity(db *sql.DB) Check {
	const name, title = CheckNameFTSIndex, "FTS search index"
	metrics := map[string]int64{}
	var offenders []int64
	var problems []string

	for _, ix := range ftsIndexes {
		docsize := ix.index + "_docsize"

		sourceRows, err := count(db, "SELECT COUNT(*) FROM "+ix.source)
		if err != nil {
			return checkErr(name, title, "could not count "+ix.source, err)
		}
		indexed, err := count(db, "SELECT COUNT(*) FROM "+docsize)
		if err != nil {
			if isMissingTable(err) {
				// A missing index is a finding, not an error: search falls back
				// to a LIKE scan and the user sees no error at all.
				return Check{
					Name: name, Title: title, Status: StatusFail,
					Detail:  fmt.Sprintf("%s is missing: this database was never migrated to v7, so %s is not searchable through the index", ix.index, ix.source),
					Metrics: metrics,
					Error:   err.Error(),
				}
			}
			return checkErr(name, title, "could not read "+docsize, err)
		}

		missing, missingIDs, err := drift(db, ix.source, docsize)
		if err != nil {
			return checkErr(name, title, "could not compare "+ix.source+" with "+docsize, err)
		}
		extra, extraIDs, err := drift(db, docsize, ix.source)
		if err != nil {
			return checkErr(name, title, "could not compare "+docsize+" with "+ix.source, err)
		}

		metrics[ix.label+"_rows"] = sourceRows
		metrics[ix.label+"_indexed"] = indexed
		metrics[ix.label+"_missing"] = missing
		metrics[ix.label+"_extra"] = extra

		if missing > 0 {
			problems = append(problems, fmt.Sprintf("%d %s row(s) missing from the index (ids %s): search will not return them", missing, ix.label, formatIDs(missingIDs, maxOffenderSamples)))
			offenders = append(offenders, missingIDs...)
		}
		if extra > 0 {
			problems = append(problems, fmt.Sprintf("%d indexed rowid(s) with no source row (ids %s)", extra, formatIDs(extraIDs, maxOffenderSamples)))
			offenders = append(offenders, extraIDs...)
		}

		// The rowid comparison above is only as good as the triggers that keep
		// the index current. Without them the index keeps its rowids and quietly
		// serves the previous text, which no rowid count can detect.
		gone, err := missingSyncTriggers(db, ix.index)
		if err != nil {
			if isMissingTable(err) {
				continue
			}
			return checkErr(name, title, "could not read the sync triggers of "+ix.index, err)
		}
		metrics[ix.label+"_sync_triggers"] = int64(len(ftsSyncTriggers) - len(gone))
		if len(gone) > 0 {
			problems = append(problems, fmt.Sprintf("%s missing (found %d of %d): %s written or edited from now on will be missing from the index, and edits will leave stale search terms",
				strings.Join(gone, ", "), len(ftsSyncTriggers)-len(gone), len(ftsSyncTriggers), ix.label))
		}
	}

	if len(problems) == 0 {
		return Check{
			Name: name, Title: title, Status: StatusPass,
			Detail: fmt.Sprintf("search index is in sync: %d note(s) and %d todo(s) are all indexed, and every sync trigger is in place",
				metrics["notes_rows"], metrics["todos_rows"]),
			Metrics: metrics,
		}
	}
	return Check{
		Name:    name,
		Title:   title,
		Status:  StatusFail,
		Detail:  "search index is not trustworthy: " + strings.Join(problems, "; "),
		Metrics: metrics,
		// Capped across both indexes: the Detail line and this slice are for
		// orientation, Metrics carries the exact totals.
		Offenders: trim(offenders, maxOffenderSamples),
	}
}

// orphanChild is a table whose project_id must resolve to a projects row.
type orphanChild struct {
	label string
	table string
}

// orphanChildren are the tables a dangling project_id can hide in.
// repositories.project_id is nullable, and a NULL there is legitimate (a
// repository found before its project was grouped), which is why every query
// filters on project_id IS NOT NULL.
var orphanChildren = []orphanChild{
	{label: "notes", table: "project_notes"},
	{label: "todos", table: "project_todos"},
	{label: "repositories", table: "repositories"},
}

// CheckOrphanRows finds rows that point at a project that no longer exists.
func CheckOrphanRows(db *sql.DB) Check {
	return guarded(db, checkOrphanRows, CheckNameOrphans, "Orphan rows")
}

func checkOrphanRows(db *sql.DB) Check {
	const name, title = CheckNameOrphans, "Orphan rows"
	metrics := map[string]int64{}
	var offenders []int64
	var problems []string
	var resolved []string

	for _, child := range orphanChildren {
		// InitDB sets PRAGMA foreign_keys=ON, so a non-NULL project_id that
		// resolves to nothing means something wrote outside the service layer:
		// a hand-edited database, a restore from an old backup, or a row that
		// predates the foreign key. The database will not repair it, because
		// ON DELETE CASCADE only fires while the parent still exists.
		cond := fmt.Sprintf(
			"project_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = %s.project_id)", child.table)
		total, err := count(db, "SELECT COUNT(*) FROM "+child.table+" WHERE "+cond)
		if err != nil {
			return checkErr(name, title, "could not count orphans in "+child.table, err)
		}
		metrics[child.label+"_orphans"] = total
		if total == 0 {
			resolved = append(resolved, fmt.Sprintf("%d %s", resolvedCount(db, child.table), child.label))
			continue
		}
		ids, err := sampleIDs(db, maxOffenderSamples,
			fmt.Sprintf("SELECT id FROM %s WHERE %s LIMIT %d", child.table, cond, maxOffenderSamples))
		if err != nil {
			return checkErr(name, title, "could not sample orphans in "+child.table, err)
		}
		problems = append(problems, fmt.Sprintf("%d %s row(s) (ids %s)", total, child.label, formatIDs(ids, maxOffenderSamples)))
		offenders = append(offenders, ids...)
	}

	// A NULL project_id is not an orphan - a repository can be indexed before
	// the grouper assigns it - but a knowledge base where *nothing* is assigned
	// cannot answer the most basic question about a repository, so the pass
	// sentence has to say how many rows are still unattached.
	unassigned, err := count(db, "SELECT COUNT(*) FROM repositories WHERE project_id IS NULL")
	if err != nil {
		return checkErr(name, title, "could not count unassigned repositories", err)
	}
	repoTotal := resolvedCount(db, "repositories")
	metrics["repositories_unassigned"] = unassigned

	// Only downgrade to warn when there is nothing worse to report. An
	// unassigned repository is a normal transient state; a dangling
	// project_id is data corruption. Returning the warn here used to shadow
	// the fail below, so a database with both would ship a Detail claiming
	// "no dangling project_id" next to metrics showing two orphans — and score
	// it as 0.5 instead of 0, inflating the trust number on real corruption.
	if len(problems) == 0 && repoTotal > 0 && unassigned == repoTotal {
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail:  fmt.Sprintf("no dangling project_id, but none of the %d repositories is attached to a project either: no repository can be traced back to the project it belongs to", repoTotal),
			Metrics: metrics,
		}
	}

	if len(problems) == 0 {
		detail := "every row resolves to an existing project (" + strings.Join(resolved, ", ") + ")"
		if unassigned > 0 {
			detail += fmt.Sprintf("; %d repository row(s) have no project yet and are not counted as orphans", unassigned)
		}
		return Check{
			Name:    name,
			Title:   title,
			Status:  StatusPass,
			Detail:  detail,
			Metrics: metrics,
		}
	}
	return Check{
		Name:      name,
		Title:     title,
		Status:    StatusFail,
		Detail:    "dangling project_id: " + strings.Join(problems, "; ") + " - these rows belong to projects that no longer exist",
		Metrics:   metrics,
		Offenders: trim(offenders, maxOffenderSamples),
	}
}

// resolvedCount reports the total rows of a child table, used only to phrase the
// passing Detail line. A failure here cannot change the verdict, so the error is
// swallowed rather than promoted: the status was already decided by the orphan
// counts above.
func resolvedCount(db *sql.DB, table string) int64 {
	n, err := count(db, "SELECT COUNT(*) FROM "+table)
	if err != nil {
		return 0
	}
	return n
}

// CheckScanCoverage answers the question a new user asks first: how much of my
// machine is actually in here?
func CheckScanCoverage(db *sql.DB) Check {
	return guarded(db, checkScanCoverage, CheckNameCoverage, "Scan coverage")
}

func checkScanCoverage(db *sql.DB) Check {
	const name, title = CheckNameCoverage, "Scan coverage"
	metrics := map[string]int64{}

	queries := []struct {
		metric string
		query  string
	}{
		{"scan_roots", "SELECT COUNT(*) FROM scan_roots"},
		{"repositories", "SELECT COUNT(*) FROM repositories"},
		// Scan coverage is a statement about the scanner's output, so it counts
		// only scanner-origin projects. The seeded default ("Uncategorized") and
		// any import-placeholder landing projects are never "collected" by a scan
		// (they are not scan artifacts), so including them would report a permanent
		// "discovered but never collected" warning over rows no scan was ever
		// supposed to touch (ADR-0023).
		{"projects", "SELECT COUNT(*) FROM projects WHERE project_kind = 'scanned'"},
		// COALESCE covers rows written before migration v5 made collected NOT
		// NULL, where the column reads as NULL instead of FALSE.
		{"uncollected", "SELECT COUNT(*) FROM projects WHERE project_kind = 'scanned' AND COALESCE(collected, 0) = 0"},
		// MarkProjectCollectedTx always stamps collected_at together with the
		// flag, so a collected project without one had its flag set by something
		// else and its "collected" claim is unbacked.
		{"collected_without_timestamp", "SELECT COUNT(*) FROM projects WHERE project_kind = 'scanned' AND COALESCE(collected, 0) = 1 AND collected_at IS NULL"},
	}
	for _, q := range queries {
		v, err := count(db, q.query)
		if err != nil {
			return checkErr(name, title, "could not count "+q.metric, err)
		}
		metrics[q.metric] = v
	}

	roots, repos, projects, uncollected :=
		metrics["scan_roots"], metrics["repositories"], metrics["projects"], metrics["uncollected"]
	metrics["collected"] = projects - uncollected
	metrics["coverage_percent"] = pct(metrics["collected"], projects)

	evidence := fmt.Sprintf("%d scan root(s), %d project(s) known, %d repositories indexed, %d never collected",
		roots, projects, repos, uncollected)

	switch {
	case roots == 0 && projects == 0 && repos == 0:
		// An empty knowledge base is not a trustworthy one, and "no roots
		// configured" is a setup problem worth showing rather than hiding
		// behind a pass.
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail:  "nothing has been scanned yet: " + evidence + " - coverage cannot be measured on an empty knowledge base",
			Metrics: metrics,
		}
	case uncollected > 0:
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("%d of %d project(s) (%d%%) were discovered but never collected: %s",
				uncollected, projects, metrics["coverage_percent"], evidence),
			Metrics: metrics,
		}
	case repos == 0:
		// Collected, yet nothing was modelled: every project yielded no
		// repository, which usually means the roots point somewhere else.
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail:  "projects were collected but no repository was indexed: " + evidence,
			Metrics: metrics,
		}
	case metrics["collected_without_timestamp"] > 0:
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("%d project(s) are flagged collected without a collected_at timestamp, so the claim that they were scanned cannot be dated: %s",
				metrics["collected_without_timestamp"], evidence),
			Metrics: metrics,
		}
	default:
		return Check{
			Name: name, Title: title, Status: StatusPass,
			Detail:  evidence + " - every known project was collected",
			Metrics: metrics,
		}
	}
}

// CheckKnowledgeCacheFreshness measures the repo_meta cache the AI reads as
// fact: how many repositories it covers, and how old the oldest entry is. A
// perfectly consistent database can still be lying to the model it feeds.
func CheckKnowledgeCacheFreshness(db *sql.DB) Check {
	return guarded(db, checkKnowledgeCacheFreshness, CheckNameCacheFresh, "Knowledge cache freshness")
}

func checkKnowledgeCacheFreshness(db *sql.DB) Check {
	const name, title = CheckNameCacheFresh, "Knowledge cache freshness"
	metrics := map[string]int64{}

	queries := []struct {
		metric string
		query  string
	}{
		{"repositories", "SELECT COUNT(*) FROM repositories"},
		{"meta_rows", "SELECT COUNT(*) FROM repo_meta"},
		// Coverage means a row that actually holds knowledge. A repo_meta row
		// still carrying the column defaults is a mining run that produced
		// nothing, and counting it would let a database full of empty strings
		// report a 100% covered cache.
		{"empty_meta", "SELECT COUNT(*) FROM repo_meta WHERE NOT " + substantiveMetaSQL},
		{"covered", "SELECT COUNT(*) FROM repositories r WHERE EXISTS (SELECT 1 FROM repo_meta m WHERE m.repository_id = r.id AND " + substantiveMetaSQL + ")"},
		// Cached knowledge for a repository that was deleted is worse than no
		// cache: it can be quoted for a project that no longer exists.
		{"orphaned_meta", "SELECT COUNT(*) FROM repo_meta m WHERE NOT EXISTS (SELECT 1 FROM repositories r WHERE r.id = m.repository_id)"},
		// A timestamp in the future cannot be a real scan time. Counting it as
		// "fresh" is how a wrong clock turns into a clean bill of health.
		{"future_meta", "SELECT COUNT(*) FROM repo_meta WHERE julianday(updated_at) > julianday('now') + 1"},
	}
	for _, q := range queries {
		v, err := count(db, q.query)
		if err != nil {
			return checkErr(name, title, "could not count "+q.metric, err)
		}
		metrics[q.metric] = v
	}

	// Ages are computed by SQLite itself with julianday: updated_at is a free
	// text column that different writers have filled in different formats, and
	// letting the engine parse it avoids a second, disagreeing date parser.
	// COALESCE(..., -1) marks "no timestamps to judge" as unknown instead of
	// zero days old.
	var oldest, newest sql.NullInt64
	if err := db.QueryRow(
		"SELECT COALESCE(MIN(CAST(julianday('now') - julianday(updated_at) AS INTEGER)), -1), "+
			"COALESCE(MAX(CAST(julianday('now') - julianday(updated_at) AS INTEGER)), -1) "+
			"FROM repo_meta").Scan(&oldest, &newest); err != nil {
		return checkErr(name, title, "could not read repo_meta timestamps", err)
	}
	metrics["oldest_age_days"] = oldest.Int64
	metrics["newest_age_days"] = newest.Int64

	repos, metaRows, covered, orphaned, emptyMeta, futureMeta :=
		metrics["repositories"], metrics["meta_rows"], metrics["covered"],
		metrics["orphaned_meta"], metrics["empty_meta"], metrics["future_meta"]
	metrics["coverage_percent"] = pct(covered, repos)

	switch {
	case repos == 0 && metaRows == 0:
		// Nothing indexed yet: the cache is empty by definition, not missing.
		return Check{
			Name: name, Title: title, Status: StatusPass,
			Detail:  "no repository is indexed yet, so the knowledge cache is empty by definition",
			Metrics: metrics,
		}
	case metaRows == 0:
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail:  fmt.Sprintf("none of the %d indexed repositories has a knowledge cache entry: the AI gets no repo-level context yet", repos),
			Metrics: metrics,
		}
	case emptyMeta == metaRows:
		// A row per repository that says nothing about any of them.
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("all %d knowledge cache row(s) still hold empty defaults: mining produced no tech stack, readme or language data, so the AI is served empty strings",
				metaRows),
			Metrics: metrics,
		}
	case orphaned == metaRows:
		return Check{
			Name: name, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("all %d repo_meta row(s) point at a repository that no longer exists: the cache describes projects that are gone",
				metaRows),
			Metrics: metrics,
		}
	case orphaned > 0:
		// Some coverage, but with cache rows for repositories that are gone.
		// Ranked above the coverage gap: a stale entry is a wrong answer,
		// a missing one is merely an absent answer.
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("%d repo_meta row(s) point at a repository that no longer exists, so the AI can be quoted knowledge about deleted projects",
				orphaned),
			Metrics: metrics,
		}
	case covered < repos:
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("knowledge cache covers %d of %d repositories (%d%%); the oldest entry is %s old",
				covered, repos, metrics["coverage_percent"], agePhrase(oldest.Int64)),
			Metrics: metrics,
		}
	case futureMeta > 0:
		// Every freshness verdict below is measured from updated_at, so a
		// timestamp that claims to be from next week makes all of them
		// meaningless rather than fresh.
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("%d knowledge cache row(s) are stamped in the future, so their freshness cannot be measured",
				futureMeta),
			Metrics: metrics,
		}
	case oldest.Int64 > StaleAfterDays:
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail: fmt.Sprintf("knowledge cache covers every repository, but the oldest entry is %s old (stale after %d days); the AI may be quoting outdated facts",
				agePhrase(oldest.Int64), StaleAfterDays),
			Metrics: metrics,
		}
	case oldest.Int64 < 0:
		// Nothing in repo_meta carries a timestamp julianday can read, so
		// "how old is this knowledge" has no answer at all.
		return Check{
			Name: name, Title: title, Status: StatusWarn,
			Detail:  "knowledge cache rows carry no readable updated_at, so their age cannot be verified",
			Metrics: metrics,
		}
	default:
		return Check{
			Name: name, Title: title, Status: StatusPass,
			Detail: fmt.Sprintf("knowledge cache covers all %d repositories; the oldest entry is %s old, the newest %s",
				repos, agePhrase(oldest.Int64), agePhrase(newest.Int64)),
			Metrics: metrics,
		}
	}
}

// CheckNoteVersionOrphans finds version snapshots whose note was deleted.
func CheckNoteVersionOrphans(db *sql.DB) Check {
	return guarded(db, checkNoteVersionOrphans, CheckNameNoteVersions, "Note version snapshots")
}

func checkNoteVersionOrphans(db *sql.DB) Check {
	const name, title = CheckNameNoteVersions, "Note version snapshots"

	// note_versions.note_id is declared ON DELETE CASCADE, so a surviving
	// snapshot always means the note row disappeared while foreign keys were
	// off, or the table was written to by something other than the service
	// layer. The snapshots are invisible in the UI (nothing joins to them) and
	// therefore accumulate unnoticed.
	cond := "NOT EXISTS (SELECT 1 FROM project_notes n WHERE n.id = note_versions.note_id)"
	total, err := count(db, "SELECT COUNT(*) FROM note_versions WHERE "+cond)
	if err != nil {
		return checkErr(name, title, "could not count orphaned note versions", err)
	}
	metrics := map[string]int64{"snapshots": resolvedCount(db, "note_versions"), "orphans": total}

	if total == 0 {
		return Check{
			Name: name, Title: title, Status: StatusPass,
			Detail:  fmt.Sprintf("all %d version snapshot(s) resolve to a live note", metrics["snapshots"]),
			Metrics: metrics,
		}
	}
	ids, err := sampleIDs(db, maxOffenderSamples,
		"SELECT note_id FROM note_versions WHERE "+cond+" LIMIT "+strconv.Itoa(maxOffenderSamples))
	if err != nil {
		return checkErr(name, title, "could not sample orphaned note versions", err)
	}
	return Check{
		Name:      name,
		Title:     title,
		Status:    StatusFail,
		Detail:    fmt.Sprintf("%d version snapshot(s) reference a note that no longer exists (note ids %s); they are unreachable from the UI", total, formatIDs(ids, maxOffenderSamples)),
		Metrics:   metrics,
		Offenders: trim(ids, maxOffenderSamples),
	}
}

// agePhrase renders a day count without the "-1 means unknown" sentinel leaking
// into user-visible text.
func agePhrase(days int64) string {
	if days < 0 {
		return "of an unreadable timestamp"
	}
	return pluralDays(days)
}

func pluralDays(days int64) string {
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// trim caps a sample list; Metrics keeps the exact counts.
func trim(ids []int64, limit int) []int64 {
	if len(ids) <= limit {
		return ids
	}
	return ids[:limit]
}
