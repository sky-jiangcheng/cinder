package db

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	"repotory/internal/domain"
)

// migrationVersionKey is the app_config key tracking the applied schema version.
const migrationVersionKey = "schema_version"

// ftsSchemaStatements creates the FTS5 full-text indexes (issue #18) and the
// triggers that keep them in sync with their source tables. The indexes are
// external-content tables backed by project_notes / project_todos, using the
// trigram tokenizer which gives case-insensitive substring matching for both
// ASCII and CJK text (queries of 3+ characters per term).
//
// All statements are idempotent (IF NOT EXISTS) so they are safe to run from
// both the schema migration and the test harness.
//
// IMPORTANT: The insert trigger (_ai) must appear before the delete trigger
// (_ad) in this list. FTS5 requires the virtual table to be created and the
// insert trigger to be defined before delete/update triggers can reference
// the fts table by name in their SQL body.
var ftsSchemaStatements = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS project_notes_fts USING fts5(title, content, content='project_notes', content_rowid='id', tokenize='trigram')`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS project_todos_fts USING fts5(title, content='project_todos', content_rowid='id', tokenize='trigram')`,
	// notes: insert / delete / update
	`CREATE TRIGGER IF NOT EXISTS project_notes_fts_ai AFTER INSERT ON project_notes BEGIN INSERT INTO project_notes_fts(rowid, title, content) VALUES (new.id, COALESCE(new.title, ''), new.content); END`,
	`CREATE TRIGGER IF NOT EXISTS project_notes_fts_ad AFTER DELETE ON project_notes BEGIN INSERT INTO project_notes_fts(project_notes_fts, rowid, title, content) VALUES ('delete', old.id, COALESCE(old.title, ''), old.content); END`,
	`CREATE TRIGGER IF NOT EXISTS project_notes_fts_au AFTER UPDATE ON project_notes BEGIN INSERT INTO project_notes_fts(project_notes_fts, rowid, title, content) VALUES ('delete', old.id, COALESCE(old.title, ''), old.content); INSERT INTO project_notes_fts(rowid, title, content) VALUES (new.id, COALESCE(new.title, ''), new.content); END`,
	// todos: insert / delete / update
	`CREATE TRIGGER IF NOT EXISTS project_todos_fts_ai AFTER INSERT ON project_todos BEGIN INSERT INTO project_todos_fts(rowid, title) VALUES (new.id, new.title); END`,
	`CREATE TRIGGER IF NOT EXISTS project_todos_fts_ad AFTER DELETE ON project_todos BEGIN INSERT INTO project_todos_fts(project_todos_fts, rowid, title) VALUES ('delete', old.id, old.title); END`,
	`CREATE TRIGGER IF NOT EXISTS project_todos_fts_au AFTER UPDATE ON project_todos BEGIN INSERT INTO project_todos_fts(project_todos_fts, rowid, title) VALUES ('delete', old.id, old.title); INSERT INTO project_todos_fts(rowid, title) VALUES (new.id, new.title); END`,
}

// EnsureFTSIndex creates the FTS5 full-text indexes and sync triggers. It is
// safe to call on any database that already has the project_notes and
// project_todos tables. Exported so tests (and future callers) can set up the
// index without running the full migration machinery.
func EnsureFTSIndex(db *sql.DB) error {
	for _, stmt := range ftsSchemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			if isAlreadyExistsErr(err) {
				continue
			}
			return fmt.Errorf("create FTS index: %w", err)
		}
	}
	return nil
}

// upgradeSchema applies incremental schema changes for existing databases.
// Migrations are tracked by a monotonically increasing version number stored in
// app_config, so each migration runs exactly once even if ALTER errors leave a
// column already present.
func upgradeSchema(db *sql.DB) error {
	from, err := readSchemaVersion(db)
	if err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}

	migrations := []migration{
		// v1: add is_starred to projects (introduced v0.11.0)
		{id: 1, sql: "ALTER TABLE projects ADD COLUMN is_starred INTEGER DEFAULT 0"},
		// v2: structured notes metadata
		{id: 2, sql: []string{
			"ALTER TABLE project_notes ADD COLUMN title TEXT DEFAULT ''",
			"ALTER TABLE project_notes ADD COLUMN tags TEXT DEFAULT ''",
			"ALTER TABLE project_notes ADD COLUMN kind TEXT DEFAULT 'other'",
			"ALTER TABLE project_notes ADD COLUMN pinned INTEGER DEFAULT 0",
			"ALTER TABLE project_notes ADD COLUMN source TEXT DEFAULT 'manual'",
		}},
		// v3: knowledge mining cache + config key for git user
		{id: 3, sql: []string{
			"CREATE TABLE IF NOT EXISTS repo_meta (" +
				"repository_id INTEGER PRIMARY KEY," +
				"tech_stack TEXT DEFAULT '[]'," +
				"readme_excerpt TEXT DEFAULT ''," +
				"languages TEXT DEFAULT '{}'," +
				"dependencies TEXT DEFAULT '[]'," +
				"top_contributors TEXT DEFAULT '[]'," +
				"activity TEXT DEFAULT '{}'," +
				"updated_at DATETIME DEFAULT CURRENT_TIMESTAMP," +
				"FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE)",
		}},
		// v4: add UNIQUE constraint on projects.root_path to prevent duplicates
		{id: 4, sql: []string{
			// Temporarily disable FKs since we drop and recreate the projects table
			"PRAGMA foreign_keys = OFF",
			// 1. create new table with UNIQUE(root_path)
			"CREATE TABLE IF NOT EXISTS projects_new (" +
				"id INTEGER PRIMARY KEY AUTOINCREMENT," +
				"name TEXT NOT NULL," +
				"root_path TEXT NOT NULL UNIQUE," +
				"level_override INTEGER DEFAULT 0," +
				"is_auto_grouped BOOLEAN DEFAULT 1," +
				"is_starred INTEGER DEFAULT 0," +
				"created_at DATETIME DEFAULT CURRENT_TIMESTAMP)",
			// 2. copy data, keeping the lowest id when duplicates exist
			"INSERT OR IGNORE INTO projects_new (id, name, root_path, level_override, is_auto_grouped, is_starred, created_at)" +
				" SELECT id, name, root_path, level_override, is_auto_grouped, is_starred, created_at FROM projects ORDER BY id ASC",
			// 3. reassign repos/notes/todos to the surviving project id for each root_path
			"UPDATE repositories SET project_id = COALESCE((SELECT MIN(p2.id) FROM projects p2 WHERE p2.root_path = " +
				"(SELECT p1.root_path FROM projects p1 WHERE p1.id = repositories.project_id)), project_id)" +
				" WHERE project_id IS NOT NULL",
			"UPDATE project_notes SET project_id = (SELECT MIN(p2.id) FROM projects p2 WHERE p2.root_path = " +
				"(SELECT p1.root_path FROM projects p1 WHERE p1.id = project_notes.project_id))",
			"UPDATE project_todos SET project_id = (SELECT MIN(p2.id) FROM projects p2 WHERE p2.root_path = " +
				"(SELECT p1.root_path FROM projects p1 WHERE p1.id = project_todos.project_id))",
			// 4. replace old table
			"DROP TABLE projects",
			"ALTER TABLE projects_new RENAME TO projects",
			// 5. recreate indexes
			"CREATE INDEX IF NOT EXISTS idx_projects_starred ON projects(is_starred)",
			// Restore FKs
			"PRAGMA foreign_keys = ON",
		}},
		// v5: add collected flag to projects for controlled scan flow
		{id: 5, sql: []string{
			"ALTER TABLE projects ADD COLUMN collected BOOLEAN NOT NULL DEFAULT FALSE",
			"ALTER TABLE projects ADD COLUMN collected_at DATETIME",
			"CREATE INDEX IF NOT EXISTS idx_projects_collected ON projects(collected)",
		}},
		// v6: add indexes for performance
		{id: 6, sql: []string{
			"CREATE INDEX IF NOT EXISTS idx_projects_collected ON projects(collected)",
			"CREATE INDEX IF NOT EXISTS idx_projects_collected_at ON projects(collected_at)",
		}},
		// v7: FTS5 full-text search index (issue #18). External-content tables
		// with the trigram tokenizer power substring + relevance search over
		// notes and todos; triggers keep the index in sync. Existing rows are
		// backfilled idempotently.
		//
		// KNOWN DEFECT in the backfill below, fixed retroactively by v12: the
		// `NOT IN (SELECT rowid FROM project_notes_fts)` predicate reads the
		// content table, not the index, so it matches nothing and the backfill
		// never runs. Do not copy this pattern to a new migration — use the
		// FTS5 'rebuild' command as v12 does. Left as shipped so that databases
		// which already recorded v7 keep a truthful migration history.
		{id: 7, sql: append(append([]string{}, ftsSchemaStatements...),
			`INSERT INTO project_notes_fts(rowid, title, content) `+
				`SELECT id, COALESCE(title, ''), content FROM project_notes `+
				`WHERE id NOT IN (SELECT rowid FROM project_notes_fts)`,
			`INSERT INTO project_todos_fts(rowid, title) `+
				`SELECT id, title FROM project_todos `+
				`WHERE id NOT IN (SELECT rowid FROM project_todos_fts)`,
		)},
		// v8: note version history (issue #16). Creates note_versions table with
		// trigger to snapshot on content changes, plus cleanup to keep recent
		// versions.
		{id: 8, sql: []string{
			`CREATE TABLE IF NOT EXISTS note_versions (` +
				`id INTEGER PRIMARY KEY AUTOINCREMENT,` +
				`note_id INTEGER NOT NULL,` +
				`title TEXT DEFAULT '',` +
				`content TEXT NOT NULL,` +
				`tags TEXT DEFAULT '',` +
				`kind TEXT DEFAULT 'other',` +
				`created_at DATETIME DEFAULT CURRENT_TIMESTAMP,` +
				`FOREIGN KEY (note_id) REFERENCES project_notes(id) ON DELETE CASCADE)`,
			`CREATE INDEX IF NOT EXISTS idx_note_versions_note_id ON note_versions(note_id, created_at DESC)`,
			// Snapshot current content when note is updated.
			`CREATE TRIGGER IF NOT EXISTS note_versions_snap AFTER UPDATE ON project_notes` +
				` BEGIN` +
				`  INSERT INTO note_versions(note_id, title, content, tags, kind)` +
				`  VALUES (new.id, COALESCE(new.title, ''), new.content, new.tags, new.kind);` +
				` END`,
			// Cleanup: keep at most 50 versions per note.
			`CREATE TRIGGER IF NOT EXISTS note_versions_cleanup AFTER INSERT ON note_versions` +
				` BEGIN` +
				`  DELETE FROM note_versions WHERE id IN (` +
				`   SELECT id FROM note_versions` +
				`   WHERE note_id = new.note_id` +
				`   ORDER BY created_at DESC` +
				`   LIMIT -1 OFFSET 50` +
				`  );` +
				` END`,
		}},
		// v9: add real commit counts to daily_stats (previously the heatmap
		// reported COUNT(DISTINCT author) as "commits").
		{id: 9, sql: "ALTER TABLE daily_stats ADD COLUMN commits INTEGER NOT NULL DEFAULT 0"},
		// v10: repair repo_meta schema drift. Older databases created repo_meta
		// with only 5 columns (createTables now defines all), so the knowledge
		// cache columns were missing and UpsertRepoMeta/GetRepoMeta failed. On
		// up-to-date databases these ALTERs are no-ops (duplicate column ->
		// isAlreadyExistsErr, safe).
		{id: 10, sql: []string{
			"ALTER TABLE repo_meta ADD COLUMN dependencies TEXT DEFAULT '[]'",
			"ALTER TABLE repo_meta ADD COLUMN top_contributors TEXT DEFAULT '[]'",
			"ALTER TABLE repo_meta ADD COLUMN activity TEXT DEFAULT '{}'",
		}},
		// v11: narrow the note version snapshot trigger. It fired on every
		// UPDATE, so pinning a note or moving it between projects created a
		// duplicate version with identical content (updated_at always changes,
		// so the row-level UPDATE cannot be filtered by comparing the whole
		// row). Only the four columns actually stored in note_versions matter.
		{id: 11, sql: []string{
			`DROP TRIGGER IF EXISTS note_versions_snap`,
			`CREATE TRIGGER IF NOT EXISTS note_versions_snap AFTER UPDATE ON project_notes` +
				` FOR EACH ROW WHEN` +
				`  new.content IS NOT old.content` +
				`  OR new.title IS NOT old.title` +
				`  OR new.tags IS NOT old.tags` +
				`  OR new.kind IS NOT old.kind` +
				` BEGIN` +
				`  INSERT INTO note_versions(note_id, title, content, tags, kind)` +
				`  VALUES (new.id, COALESCE(new.title, ''), new.content, new.tags, new.kind);` +
				` END`,
		}},
		// v12: repair the FTS5 indexes that migration v7 left empty.
		//
		// v7 backfilled with
		//
		//	... FROM project_notes
		//	WHERE id NOT IN (SELECT rowid FROM project_notes_fts)
		//
		// which is a permanent no-op: project_notes_fts is an external-content
		// table, so `SELECT rowid FROM project_notes_fts` is answered by the
		// CONTENT table (project_notes), not by the index. The predicate is
		// therefore "id not in project_notes", which is never true, and the
		// index stays empty on every database that had rows when v7 ran.
		//
		// The visible symptom is silent: SearchNotes falls back to LIKE only
		// when the FTS query *errors*, not when it matches nothing, so search
		// keeps returning a subset of the notes with no error anywhere. v7 is
		// left untouched on purpose — migrations are immutable and already
		// shipped — so this rebuild is what repairs the damage.
		//
		// 'rebuild' is FTS5's own command for this: it drops and repopulates
		// the index from the content table. It is idempotent, and cheap enough
		// to run once on open (a personal knowledge base is thousands of
		// notes, not millions).
		{id: 12, sql: []string{
			`INSERT INTO project_notes_fts(project_notes_fts) VALUES('rebuild')`,
			`INSERT INTO project_todos_fts(project_todos_fts) VALUES('rebuild')`,
		}},
		// v13: normalize note tags + invalidate multi-repo mining caches.
		//
		// Note writers used to store the tags input verbatim, so "cbipay, 样例
		// 测试, 支付" was ONE tag row: the tag list showed a single joined chip
		// that the frontend filter (which splits tags on commas) could never
		// match, hiding the note behind its own tags. Rewritten via Go because
		// the split/dedupe/rejoin logic must mirror db.NormalizeTags exactly.
		//
		// repo_meta rows for MULTI-repo projects are dropped: mining ran git in
		// the project root, which for a container of sibling repos is not a git
		// repository, so activity and top contributors were silently zero and
		// that broken result was cached. A true zero-commit multi-repo project
		// cannot be distinguished from the broken cache, so invalidation is the
		// only reliable repair; the next detail-page load remines (cheap on a
		// personal knowledge base). Single-repo projects mined their actual
		// repository and keep their cache.
		{id: 13, fn: migrateV13NormalizeTagsAndDropMultiRepoMeta},
		// v14: incremental-embedding work queue (see embed_dirty.go).
		//
		// A trigger-maintained dirty table rather than service-layer hooks,
		// because internal/db is where every note writer converges — including
		// the plugin runtime's direct upserts, which the five agent-memory
		// importers use. Goes through the Go fn so the statement list stays in
		// one place with EnsureNoteEmbedDirty (idempotent, already-exists safe).
		{id: 14, fn: noteEmbedDirtyMigration},
		// v15: turn startup auto-import off for existing databases (ADR-0014 W0).
		//
		// The privacy problem this closes is that reading another tool's memory
		// files and writing them into this database was never an explicit choice:
		// insertDefaults seeded `auto_import='1'` as a ROW, so an untouched install
		// looks identical to one where a user deliberately turned it on. That is
		// also why "just change the default" could not work — hence a one-time
		// normalization here plus a flipped predicate in the service layer
		// (an absent row must mean OFF, and db.GetConfig maps a missing row to
		// "", which the old `v != "0"` test read as ON).
		//
		// Only rows that were actually ON are rewritten: an explicit '0' stays,
		// and a missing row is not inserted (the service now treats missing as
		// OFF). Re-running is harmless, which is what makes the upgrade safe to
		// retry after a mid-migration crash.
		{id: 15, sql: "UPDATE app_config SET value = '0' WHERE key = 'auto_import' AND value <> '0'"},
		// v16: the wiki layer (ADR-0014 决策 1 「先补结构，再让 LLM 写」).
		//
		// Four new tables (pages, links, note->page, note->repository) and their
		// indexes. Nothing existing is ALTERed and no trigger is added to a
		// pre-existing table, which is what makes this migration reversible by
		// construction: DropWikiSchema restores byte-identical schema state, and
		// TestUpgradeSchema_WikiIsReversible asserts exactly that.
		// project_notes remains the single source of truth (ADR-0014 决策 5).
		{id: 16, fn: EnsureWikiSchema},
		// v17: the page search index (ADR-0014 决策 3 / TODO M6-W2). Kept in its
		// own migration so a page index can be dropped and rebuilt without
		// touching the page tables, and so DropWikiSchema reverses both.
		{id: 17, fn: EnsureWikiFTS},
		// v18: review state for the page layer (ADR-0015 决策 1), the hard
		// prerequisite for W3. Without status + source the rule "the compiler
		// writes pending pages and only a human lets them into search" has no
		// representation at all, and the retrieval paths would feed unreviewed
		// model output into AskAI the instant a compiler exists.
		{id: 18, fn: EnsureWikiReviewColumns},
		// v19: async batch jobs (ADR-0016). The rehearsal measured 173s per note
		// on a local model, which is the evidence that a synchronous call is the
		// wrong shape regardless of timeouts. Purely additive operational state.
		{id: 19, fn: EnsureCompileJobs},
		// v20: the job table becomes a general long-task queue (ADR-0016 待决 ①,
		// resolved as "yes"). The lint model pass is the same shape of work — a
		// 10-minute ceiling on a local model — so it gets the same queue, the same
		// polling surface and the same crash recovery rather than a second state
		// machine. Additive: one discriminator column, and pre-existing rows are all
		// compile jobs because lint jobs could not be written before it existed.
		{id: 20, fn: EnsureCompileJobQueue},
		// v21: the relation vocabulary (ADR-0018 lane 1). page_links.relation was
		// free text — the compiler wrote "compiled-from", filing wrote "cites", the
		// model could invent anything — so the graph could not be queried by relation
		// at all. This rebuilds the table with a CHECK over eight canonical types and
		// folds existing values onto them. A rebuild (not an ALTER) because SQLite
		// cannot attach a CHECK to an existing column; done in one transaction;
		// reversible by the same DropWikiSchema/EnsureWikiSchema pair the layer uses.
		{id: 21, fn: EnsureWikiRelationVocabulary},
		// v22: page version snapshots (ADR-0021). wiki_pages.content was overwritten
		// in place, so unlike notes (note_versions, v8/v11) there was no way to see
		// what a human actually approved once a page was edited or absorbed by a
		// merge. Purely additive: one new table, three triggers (the snapshot writer
		// plus the two append-only guards), and a per-page backfill so pages that
		// were already approved get a baseline.
		//
		// It takes v22 rather than the v23 ADR-0021 决策 5 predicted: this layer needs
		// no CHECK rebuild (the ADR's合批 idea was for compile_jobs.kind, which
		// ADR-0019 待决 3 and ADR-0020 决策 8 both decided against for now), while
		// ADR-0018 lane 2 (page_embeddings) is still blocked on Phase 0's labelled query
		// set. Reserving a number for a migration that may not land for months would
		// just leave a hole; lane 2 takes v23 and docs/adr/0018 was amended to say so.
		{id: 22, fn: EnsurePageVersions},
		// v23: the page vector space + its incremental queue (ADR-0018 lane 2,
		// first half). note_embeddings is notes-only (vecindex.go: KnnNoteIDs and
		// PutNoteEmbedding are keyed by note rowid), so wiki_pages had no vector
		// face and semantic recall could never reach compiled pages. This adds the
		// page-embed dirty queue and triggers (page_embed_dirty.go); the vec0 table
		// itself (page_embeddings) is created lazily by the service layer's rebuild
		// once it has learned the embedding dim, exactly like note_embeddings, so a
		// database that never enables semantic search never pays for it. The queue
		// and its triggers are part of the wiki layer's derived structure:
		// DropWikiSchema drops them (dropPageEmbedDirty) and InitDB re-asserts
		// EnsurePageEmbedDirty on every open, exactly as page_versions (v22) is
		// handled — its trigger is also ON wiki_pages, so it cannot outlive a drop.
		//
		// It is physically SEPARATE from note_embeddings on purpose: hybrid.FuseRRF
		// merges bare int64 ids, and wiki_pages.id / project_notes.id are independent
		// sequences, so sharing one index would fuse page #7 with note #7. The queue
		// marks a page dirty on any text change or approved-ness change; the drainer
		// decides re-embed vs remove, and only approved pages are ever embedded
		// (ADR-0015).
		{id: 23, fn: pageEmbedDirtyMigration},
		// v24: project origin/role classification + the seeded default project
		// (ADR-0023). External-knowledge imports used to silently DROP any note
		// that matched no project (runtime.go's `ProjectID<=0 -> Skipped++`),
		// because project_notes.project_id is NOT NULL + FK and there was no
		// "headless note" state and no default/placeholder landing project. This
		// adds project_kind (scanned / import_placeholder / default) and seeds the
		// single default row, so a later importer can land an unmatched note on a
		// placeholder (named for its repo) or the default instead of discarding it.
		//
		// Purely additive: one ALTERed column with a constant default (pre-v24 rows
		// read 'scanned') + one idempotent INSERT guard, both re-asserted on every
		// open via EnsureProjectKind — the same "ensure object on open, not just at
		// migration" contract as v22/v23 (whose functions are both the migration fn
		// and an InitDB self-heal call). Enum values are checked in Go, never with
		// an inline CHECK: adding a table CHECK would force a projects rebuild.
		{id: 24, fn: EnsureProjectKind},
		// v25: drop mining caches poisoned by the single-repo grouping case.
		//
		// v13 dropped repo_meta for MULTI-repo projects, because mining ran git in
		// the project root and a container of sibling repos is not a git repository;
		// its comment reasoned that "single-repo projects mined their actual
		// repository and keep their cache". That assumption was wrong: a project can
		// be single-repo and still have a container root (/x/MiddlePlatform holding
		// /x/MiddlePlatform/<repo>), which is the ordinary shape after a scan of a
		// group directory. Those rows cached a zeroed activity too — 4 of the 5
		// repo_meta rows on a real library, one of them a repository with 6,660
		// commits — and nothing invalidates repo_meta, so they stayed broken even
		// after the aggregation condition was widened to one-or-more repositories.
		//
		// The predicate is structural rather than evidence-based on purpose: gating on
		// "daily_stats proves this repo has commits" would repair only 1 of the 4 rows,
		// because daily_stats covers 63 of 140 repositories here. `project root <>
		// repository path` is exactly the condition that produces the bad mining, so it
		// catches every affected row; a legitimately zero-commit project among them
		// costs one re-mine, the same trade v13 already accepted.
		{id: 25, fn: DropPoisonedActivityMeta},
		// v26: access reinforcement foundation for the wiki layer — how often a
		// page has been USED (surfaced into an answer's evidence) and when that
		// last happened. Pure signals: nothing reads them to change behaviour
		// yet, so the migration is purely additive and every pre-v26 page reads
		// as never-accessed, which is the truth. Ranking/decay policies are
		// separate decisions and must not silently start depending on these
		// columns.
		{id: 26, sql: []string{
			"ALTER TABLE wiki_pages ADD COLUMN access_count INTEGER DEFAULT 0",
			"ALTER TABLE wiki_pages ADD COLUMN last_accessed_at TEXT",
		}},
		// v27: the topic layer between a project and its notes.
		//
		// Until now a note belonged to exactly one project, and the only
		// groupings inside that project were flat and lossy: `tags` is a
		// comma-joined string (no entity, no rename, no count) and `kind` is
		// a two-value type flag. A project that accumulates notes about
		// several subjects has no way to say so — every note lands in one
		// undifferentiated list, and "the notes about the gateway" cannot be
		// separated from "the notes about the migration".
		//
		// So the hierarchy becomes project -> topic -> note. A topic is a
		// real entity (its own table, its own id, its own lifecycle), which
		// is what makes renaming a topic a single UPDATE instead of a
		// rewrite across every note, and what makes "how many notes are in
		// this topic" answerable at all.
		//
		// `note_topics` is scoped to a project (project_id NOT NULL, ON
		// DELETE CASCADE): the same subject name in two projects is two
		// topics, because a topic's meaning comes from its project's
		// context. `project_notes.topic_id` is nullable with ON DELETE SET
		// NULL — a note with no topic is a legitimate state (every note
		// written before v27 has none, and the user may never file one),
		// and deleting a topic must not delete the notes in it.
		//
		// Purely additive: one new table, one nullable column. No note is
		// rewritten and no note loses its project.
		{id: 27, sql: []string{
			`CREATE TABLE IF NOT EXISTS note_topics (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				project_id INTEGER NOT NULL,
				name TEXT NOT NULL,
				description TEXT DEFAULT '',
				sort_order INTEGER DEFAULT 0,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_note_topics_project_name ON note_topics(project_id, name)`,
			"ALTER TABLE project_notes ADD COLUMN topic_id INTEGER REFERENCES note_topics(id) ON DELETE SET NULL",
		}},
		// v28: the AI call audit log (ADR-0030).
		//
		// Cinder is the local-first memory layer for AI coding agents, and its
		// LLM exit converges into exactly three HTTP primitives in
		// internal/service/ai.go — every outbound model call in the codebase
		// passes through them, and all eight business paths funnel into them.
		// That makes "what did this machine send out" answerable from one place.
		//
		// The table records the EVIDENCE of a call (when, endpoint, model,
		// surface, sizes, review verdicts) and deliberately does NOT record the
		// prompt or the reply: writing prompt text into the knowledge base
		// would pollute it and pull API keys into the same SQLite file the
		// notes live in, which runs against the "one local file, nothing leaves
		// the machine" promise this product is built on. The default record
		// carries a SHA-256 of the prompt instead — it answers "was this the
		// same prompt as last time" without answering "what was in it".
		//
		// Append-only via BEFORE UPDATE / BEFORE DELETE triggers, and NO
		// foreign key — the same two structural decisions ADR-0021 argued for
		// page_versions, for the same two reasons: an audit row that can be
		// edited after the fact is not evidence, and SQLite fires row triggers
		// for FK CASCADE actions, so a cascading FK would make deleting the
		// audited thing fail outright.
		//
		// Purely additive: one new table, no existing table touched.
		{id: 28, fn: EnsureAICallLog},
		// v29: persistent cross-client conversations, isolated from knowledge.
		{id: 29, fn: EnsureChatSchema},
		// v30: incremental commit review (ADR-0053). Two tables, no
		// backfill: a review report is generated, never migrated.
		{id: 30, fn: EnsureReviewSchema},
		// v31: the note write contract (ADR-0057) — an idempotency key, an
		// optimistic-lock version, and version history that stores the state a
		// note had BEFORE it was changed.
		//
		// Three independent defects, one migration: splitting them would mean
		// three full table rewrites and three chances to leave a database
		// half-migrated.
		{id: 31, fn: EnsureNoteWriteContract},
	}

	for _, m := range migrations {
		if m.id <= from {
			continue
		}
		if err := m.apply(db); err != nil {
			// Distinguish "already exists" (idempotent, safe to stamp) from
			// real migration failures (must not stamp or the migration is
			// silently skipped on future runs).
			if !isAlreadyExistsErr(err) {
				return fmt.Errorf("schema migration %d failed: %w", m.id, err)
			}
			logMigrationError(m.id, err)
			// apply() rolls its batch back before returning, so its own stamp
			// never landed. An "already exists" error means an earlier run got
			// the schema through, so the version still has to be recorded here
			// — otherwise the next open re-runs the same migration forever.
			if err := writeSchemaVersion(db, m.id); err != nil {
				return fmt.Errorf("failed to stamp schema version %d: %w", m.id, err)
			}
		}
	}
	return nil
}

// migration represents one schema change. Either sql (a statement list run
// inside one transaction) or fn (a Go-side data migration whose logic cannot
// be expressed in SQL) must be set; fn takes precedence.
type migration struct {
	id  int
	sql any
	fn  func(db *sql.DB) error
}

// apply runs the migration and records the version stamp as part of the same
// transaction as its last DDL statement, so "schema changed" and "stamped as
// applied" are one commit. They used to be two: the stamp was written after
// apply() returned, which left a window in which a kill -9 or a full disk
// produced a database whose schema was v31 but whose stamp still said v30 — the
// next start then re-ran the migration against an already-migrated database and
// only survived because every Ensure*/ALTER happens to be idempotent. Moving
// the stamp inside removes the window instead of relying on that property.
//
// fn migrations are the documented exception: they open their own transactions
// (they carry data rewrites, not just DDL), so their stamp is still written
// after the fact. Every fn migration is idempotent by contract — re-running it
// after a crash is a no-op — which is what covers the remaining window.
func (m migration) apply(db *sql.DB) error {
	if m.fn != nil {
		if err := m.fn(db); err != nil {
			return err
		}
		return writeSchemaVersion(db, m.id)
	}
	stmts := stmtList(m.sql)
	// Which statement closes the batch that has to carry the stamp. A trailing
	// PRAGMA (v4 ends with `PRAGMA foreign_keys = ON`) commits the open
	// transaction before it runs, so "the last statement" is not necessarily
	// "the last statement inside the transaction" — find the last one that is.
	lastDDL := -1
	for i, s := range stmts {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "PRAGMA") {
			lastDDL = i
		}
	}
	// Non-PRAGMA statements of one migration run inside a single transaction,
	// so a multi-step migration cannot leave the schema half-applied if the
	// process dies between statements — v4 drops and renames the projects
	// table, and a crash between those two steps used to require manual
	// repair (the version stamp would not be written, so the next start would
	// re-run the migration against a database whose projects table was gone).
	// PRAGMA foreign_keys cannot change inside a transaction, so PRAGMAs run
	// outside one (committing whatever batch is open first). With the
	// single-connection pool the tx and the bare Exec never contend.
	var tx *sql.Tx
	commit := func() error {
		if tx == nil {
			return nil
		}
		err := tx.Commit()
		tx = nil
		return err
	}
	// stamped records whether the version was written inside the batch, which
	// is the normal path; the fallbacks below cover a migration with no DDL at
	// all and the "last statement was already applied" rollback.
	stamped := false
	for i, s := range stmts {
		upper := strings.ToUpper(strings.TrimSpace(s))
		if strings.HasPrefix(upper, "PRAGMA") {
			if err := commit(); err != nil {
				return err
			}
			if _, err := db.Exec(s); err != nil && !isAlreadyExistsErr(err) {
				return err
			}
			continue
		}
		if tx == nil {
			var err error
			tx, err = db.Begin()
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(s); err != nil {
			_ = tx.Rollback()
			tx = nil
			// SQLite reports "duplicate column name" when a column already
			// exists; treat that as already-applied rather than a hard
			// failure. The rolled-back batch re-runs cleanly on the next
			// open because every statement is idempotent under that rule.
			if isAlreadyExistsErr(err) {
				continue
			}
			return err
		}
		if i == lastDDL {
			// Same transaction, same commit: the schema change and the claim
			// that it is at version m.id become visible together.
			if _, err := tx.Exec(
				"INSERT OR REPLACE INTO app_config (key, value) VALUES (?, ?)",
				migrationVersionKey, strconv.Itoa(m.id),
			); err != nil {
				_ = tx.Rollback()
				tx = nil
				return err
			}
			stamped = true
		}
	}
	if !stamped {
		if err := commit(); err != nil {
			return err
		}
		return writeSchemaVersion(db, m.id)
	}
	return commit()
}

func stmtList(s any) []string {
	switch v := s.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	default:
		return nil
	}
}

func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists")
}

func readSchemaVersion(db *sql.DB) (int, error) {
	// app_config.value is TEXT, so scan into a string and parse.
	var raw string
	err := db.QueryRow("SELECT value FROM app_config WHERE key = ?", migrationVersionKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid schema version %q: %w", raw, err)
	}
	return v, nil
}

func writeSchemaVersion(db *sql.DB, version int) error {
	_, err := db.Exec(
		"INSERT OR REPLACE INTO app_config (key, value) VALUES (?, ?)",
		migrationVersionKey, strconv.Itoa(version),
	)
	return err
}

func logMigrationError(id int, err error) {
	log.Printf("schema migration %d warning: %v", id, err)
}

// insertDefaults inserts default configuration values if they don't exist.
// migrateV13NormalizeTagsAndDropMultiRepoMeta rewrites note tags into the
// canonical form produced by NormalizeTags and drops mining caches for
// multi-repo projects, whose activity and top contributors were mined from a
// non-git container directory and are therefore always zero. See migration
// v13's comment in upgradeSchema.
func migrateV13NormalizeTagsAndDropMultiRepoMeta(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id, tags FROM project_notes WHERE tags LIKE '%,%' OR tags LIKE '%，%'`)
	if err != nil {
		return err
	}
	type tagFix struct {
		id   int64
		tags string
	}
	var fixes []tagFix
	for rows.Next() {
		var id int64
		var tags string
		if err := rows.Scan(&id, &tags); err != nil {
			rows.Close()
			return err
		}
		if n := NormalizeTags(tags); n != tags {
			fixes = append(fixes, tagFix{id, n})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, f := range fixes {
		if _, err := tx.Exec("UPDATE project_notes SET tags = ? WHERE id = ?", f.tags, f.id); err != nil {
			return err
		}
	}

	_, err = tx.Exec(`DELETE FROM repo_meta WHERE repository_id IN (
		SELECT r.id FROM repositories r
		WHERE r.project_id IS NOT NULL
		  AND (SELECT COUNT(*) FROM repositories r2 WHERE r2.project_id = r.project_id) > 1)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// EnsureProjectKind is migration v24 (ADR-0023). It re-asserts two things and is
// idempotent on both, so it runs both as the migration body and from InitDB on
// every open:
//   - the projects.project_kind column. The constant DEFAULT makes every pre-v24
//     row read back as 'scanned'; once the column exists the ALTER is a "duplicate
//     column name" error that isAlreadyExistsErr tolerates as already-applied.
//   - the single project_kind='default' "Uncategorized" landing project, inserted
//     only when it is absent (WHERE NOT EXISTS), so re-running never duplicates it.
//
// The enum values are validated in Go (domain.ProjectKind*), never with a SQL
// CHECK: attaching a CHECK to the existing projects table would force a full
// rebuild (see ADR-0021 implementation corrections).
func EnsureProjectKind(db *sql.DB) error {
	if _, err := db.Exec(
		"ALTER TABLE projects ADD COLUMN project_kind TEXT NOT NULL DEFAULT '" + domain.ProjectKindScanned + "'",
	); err != nil && !isAlreadyExistsErr(err) {
		return fmt.Errorf("add project_kind column: %w", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (name, root_path, level_override, is_auto_grouped, project_kind) "+
			"SELECT ?, ?, 0, 0, ? "+
			"WHERE NOT EXISTS (SELECT 1 FROM projects WHERE project_kind = ?)",
		domain.DefaultProjectName, domain.DefaultProjectRootPath,
		domain.ProjectKindDefault, domain.ProjectKindDefault,
	); err != nil {
		return fmt.Errorf("seed default project: %w", err)
	}
	return nil
}

// GetDefaultProjectID returns the id of the seeded default ("Uncategorized")
// project, or 0 if it is somehow absent. Used by the importer landing path
// (ADR-0023 decision 3) to place repo-pathless notes without duplicating the
// seed lookup.
func GetDefaultProjectID(db *sql.DB) (int64, error) {
	var id int64
	err := db.QueryRow(
		"SELECT id FROM projects WHERE project_kind = ? LIMIT 1",
		domain.ProjectKindDefault,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

func insertDefaults(db *sql.DB) error {
	// `auto_import` is seeded at "0" since v15: reading another tool's memory at
	// startup is a privacy-relevant choice and must not be something the app did
	// on the user's behalf. It is seeded rather than left absent only so the
	// settings page shows a real value; the service predicate is now `== "1"`, so
	// absent, "0" and any junk all mean OFF (the old `!= "0"` test made an absent
	// row mean ON, which is how a "default off" setting silently stayed on).
	defaults := map[string]string{
		"daily_code_standard": "500",
		"scan_depth":          "2",
		"auto_import":         "0",
	}

	for key, value := range defaults {
		_, err := db.Exec(
			"INSERT OR IGNORE INTO app_config (key, value) VALUES (?, ?)",
			key, value,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// EnsureNoteWriteContract is migration v31 (ADR-0057). It gives the note write
// path the three properties it could not express before, in one pass because
// each one is a full table rewrite on its own:
//
//  1. idempotency_key + a PARTIAL unique index on (project_id, idempotency_key).
//     Session capture, a retried repotory_handoff and the rolling milestone
//     checkpoint can all describe the same session; without a key they become
//     two near-identical handoffs that BuildProjectContext shows side by side,
//     with no way to tell which one is final. The index — not a "look first,
//     then insert" check — is what makes that safe: two processes racing both
//     find nothing and both insert, unless the database refuses the second.
//     Empty keys stay out of the index, so the callers that do not need
//     deduplication (desktop editor, MCP notes_create) are unaffected.
//
//  2. version for optimistic locking. UpdateNoteFull used to be
//     `UPDATE ... WHERE id = ?`, i.e. last-write-wins, and the desktop app and
//     cinder-mcp are two processes with two connections. WAL + busy_timeout
//     guarantees no SQLITE_BUSY, not that one writer does not silently
//     overwrite the other. Callers that read a note and write it back pass the
//     version they read; a mismatch is a conflict, not a lost update.
//
//  3. snapshot rows that hold the state BEFORE a change (old.* instead of
//     new.*). v8/v11 wrote new.*, so note_versions held the post-edit sequence
//     — a duplicate of what project_notes already says, and the initial
//     content missing until the second edit. With old.* the table is the
//     history a row-level snapshot is supposed to be: v1..vN-1, with the
//     current state in project_notes.
//
// Deviation from ADR-0057 D3, decided while implementing: only the UPDATE
// trigger is created. D3 also asked for an AFTER INSERT trigger so the created
// state enters history immediately. With old.* the first edit already records
// exactly that state (old.version = 1, old.content = as created), so the extra
// trigger would insert the same row twice and spend half of the 50-row budget
// on duplicates. And a version-keyed dedupe ("skip if this version is already
// recorded") would silently drop real snapshots, because a writer that goes
// through raw SQL — a script plugin holding ctx.DB() — can change content
// without bumping version.
//
// Not backfilled, deliberately: pre-v31 history rows keep version = 0 ("state
// after an edit, version unknown") because their content is a post-change
// state that cannot be mapped back to a version. Nor are existing duplicate
// handoffs merged — that is the user's data, not a schema defect.
func EnsureNoteWriteContract(db *sql.DB) error {
	stmts := []string{
		"ALTER TABLE project_notes ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE project_notes ADD COLUMN version INTEGER NOT NULL DEFAULT 1",
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_project_notes_idempotency ` +
			`ON project_notes(project_id, idempotency_key) WHERE idempotency_key <> ''`,
		"ALTER TABLE note_versions ADD COLUMN version INTEGER NOT NULL DEFAULT 0",
		// The v8/v11 trigger wrote new.*; drop it before creating its
		// replacement, or every edit would snapshot twice (once with the state
		// before, once with the state after).
		"DROP TRIGGER IF EXISTS note_versions_snap",
		// Same WHEN clause as v11 (the four columns note_versions stores), so
		// pinning or moving a note still does not create a no-op version.
		`CREATE TRIGGER IF NOT EXISTS note_versions_snap_upd AFTER UPDATE ON project_notes` +
			` FOR EACH ROW WHEN` +
			`  new.content IS NOT old.content` +
			`  OR new.title IS NOT old.title` +
			`  OR new.tags IS NOT old.tags` +
			`  OR new.kind IS NOT old.kind` +
			` BEGIN` +
			`  INSERT INTO note_versions(note_id, version, title, content, tags, kind)` +
			`  VALUES (old.id, old.version, COALESCE(old.title, ''), old.content, old.tags, old.kind);` +
			` END`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil && !isAlreadyExistsErr(err) {
			return fmt.Errorf("note write contract: %w", err)
		}
	}
	return nil
}

// DropPoisonedActivityMeta deletes repo_meta rows whose cached activity is empty or
// zero for a project whose root directory is not the repository it was meant to mine
// — the shape that made knowledge.Mine's git probes run in a non-git container and
// cache the resulting zeros. See migration v25's comment in upgradeSchema.
//
// One-time by design rather than an InitDB self-heal: once the aggregation is fixed
// the re-mined rows are correct, and re-running this on every open would repeatedly
// drop and re-mine any genuinely commit-less grouping.
func DropPoisonedActivityMeta(db *sql.DB) error {
	res, err := db.Exec(`
		DELETE FROM repo_meta
		WHERE repository_id IN (
				SELECT r.id FROM repositories r
				JOIN projects p ON p.id = r.project_id
				WHERE p.root_path <> r.path
		  )
		  AND (
				activity IS NULL
				OR activity = ''
				OR activity = '{}'
				OR activity LIKE '%"total_commits":0%'
		  )`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("schema migration 25: dropped %d poisoned repo_meta activity cache(s)", n)
	}
	return nil
}
