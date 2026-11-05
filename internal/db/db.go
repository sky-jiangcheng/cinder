package db

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// InitDB initializes the SQLite database, creating tables if they don't exist.
// Returns the database handle and any error encountered.
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Embedded SQLite is single-writer: cap the pool to one connection so
	// concurrent Wails/scan/plugin calls serialize instead of hitting
	// "database is locked" (SQLITE_BUSY). busy_timeout retries under lock
	// contention as a backstop.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	// Enable WAL mode for better concurrent read performance. This happens
	// before busy_timeout is raised, because the mode change waits on its own
	// short timeout — see enableWAL.
	if err := enableWAL(db); err != nil {
		db.Close() //nolint:errcheck // close on init failure, error irrelevant
		return nil, err
	}

	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close() //nolint:errcheck // close on init failure, error irrelevant
		return nil, fmt.Errorf("failed to set busy_timeout: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	if err := createTables(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	// The database (and its WAL sidecars) holds the user's whole knowledge
	// base. The data directory is created 0750, but the files themselves get
	// the process umask default — 0644 on macOS — which other local users can
	// read. Pin down what exists at init (createTables has already written, so
	// the -wal/-shm sidecars are here). platform.SetPrivateUmask covers files
	// created in later sessions; this also protects embedders that bypass it.
	if dbPath != ":memory:" {
		for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			_ = os.Chmod(p, 0600)
		}
	}

	if err := upgradeSchema(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to upgrade schema: %w", err)
	}

	// Operational job queue (v19) is re-asserted too: a dropped table must not
	// leave a database stamped with a version whose object is missing.
	if err := EnsureCompileJobs(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure compile jobs: %w", err)
	}

	// Re-assert the derived wiki layer on every open, not just at migration time.
	//
	// Why this is not redundant: migrations are stamped, so v16 never runs twice.
	// DropWikiSchema is a supported operation (the layer is derived cache, and the
	// reversibility test uses it), and without this line a database whose layer was
	// dropped would come back with schema_version=16 and NO tables — every wiki
	// query from then on fails with "no such table" and nothing short of manually
	// re-running the migration repairs it. Same contract as EnsureFTSIndex: safe to
	// call on any database that already has project_notes.
	if err := EnsureWikiSchema(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure wiki schema: %w", err)
	}

	// Same self-healing contract for the page index (v17).
	if err := EnsureWikiFTS(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure wiki FTS: %w", err)
	}

	// ...and for the review columns (v18). A database that predates them must not
	// stay half-migrated just because CREATE TABLE IF NOT EXISTS is a no-op.
	if err := EnsureWikiReviewColumns(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure wiki review columns: %w", err)
	}

	// ...and for page version snapshots (v22, ADR-0021). It must run AFTER
	// EnsureWikiReviewColumns: the snapshot trigger's WHEN clause reads
	// old.status / new.status, so creating it against a table without the status
	// column would fail. The call is also what re-applies the backfill, which is
	// guarded per page and therefore safe on every open.
	if err := EnsurePageVersions(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure page versions: %w", err)
	}

	// Same self-healing contract for the page-embed queue (v23, ADR-0018 lane 2).
	// Its triggers are ON wiki_pages, so this must run after EnsureWikiSchema (above)
	// has the table; re-asserting here is what restores the queue after a
	// DropWikiSchema, exactly as EnsurePageVersions restores the snapshot layer.
	if err := EnsurePageEmbedDirty(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure page embed dirty: %w", err)
	}

	// Same self-healing contract for the project classification column and the
	// default ("Uncategorized") landing project (v24, ADR-0023). The column cannot
	// be dropped without a projects rebuild, but the default row is plain data that
	// a stray manual DELETE could remove — and the importer landing path and the
	// cleanup exemption both depend on exactly one row existing. EnsureProjectKind
	// is idempotent on both halves, so re-running every open is safe.
	if err := EnsureProjectKind(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure project kind and default project: %w", err)
	}

	// The AI call audit log (v28, ADR-0030) is created by its own migration and
	// re-asserted here so an existing database gains it on upgrade and a stray
	// manual DROP cannot silently disable auditing. All statements are IF NOT
	// EXISTS, so this is safe on every open.
	if err := EnsureAICallLog(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to ensure ai call log: %w", err)
	}

	// Triggers cannot backfill: pages that existed before this index was created
	// would stay unsearchable forever. Rebuild is cheap (pages are a derived,
	// bounded artifact) and idempotent, and doing it here is what stops the v7
	// "empty index that never got repaired" failure from repeating.
	if err := RebuildWikiFTS(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, err
	}

	if err := insertDefaults(db); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to insert defaults: %w", err)
	}

	return db, nil
}

// enableWAL sets the file-level journal mode, retrying until the lock frees.
//
// Why a retry is needed: changing the journal mode takes the exclusive lock,
// and a second process that is still creating the same fresh database holds it
// for as long as its own schema setup runs. One long busy_timeout is the wrong
// shape for that wait — it blocks the caller, then gives up in a single
// attempt while the other process is still working. Short waits with backoff
// notice the release as soon as it happens. WAL lives in the file header, so
// after the first creator commits, every later attempt finds the mode already
// set and returns without taking the lock at all.
//
// Without this, the loser of a first-run race refuses to start: a user who
// opens the desktop app while an IDE plugin launches the MCP server against a
// brand-new database gets "failed to set WAL mode: database is locked".
func enableWAL(db *sql.DB) error {
	deadline := time.Now().Add(walWaitBudget)
	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(walRetryBackoff(attempt)) * time.Millisecond)
		}
		// Both statements on one Exec so they share the single pooled
		// connection: the short timeout only has to cover the mode change.
		_, err := db.Exec("PRAGMA busy_timeout=250; PRAGMA journal_mode=WAL;")
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("failed to set WAL mode after %s of waiting (another Cinder process is still creating this database): %w", walWaitBudget, lastErr)
		}
	}
}

// How long the second process is willing to wait for the first creator.
const walWaitBudget = 15 * time.Second

// 25ms doubled per attempt, capped at 400ms.
func walRetryBackoff(attempt int) int {
	return 25 << min(attempt-1, 4)
}

// createTables creates all required tables if they do not exist.
func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS scan_roots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL UNIQUE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS projects (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		root_path TEXT NOT NULL,
		level_override INTEGER DEFAULT 0,
		is_auto_grouped BOOLEAN DEFAULT 1,
		collected BOOLEAN DEFAULT FALSE,
		collected_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS repositories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL UNIQUE,
		project_id INTEGER,
		last_scanned_at DATETIME,
		FOREIGN KEY (project_id) REFERENCES projects(id)
	);

	CREATE TABLE IF NOT EXISTS daily_stats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		repository_id INTEGER NOT NULL,
		stat_date DATE NOT NULL,
		author TEXT NOT NULL,
		files_changed INTEGER DEFAULT 0,
		lines_added INTEGER DEFAULT 0,
		lines_deleted INTEGER DEFAULT 0,
		commits INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (repository_id) REFERENCES repositories(id),
		UNIQUE(repository_id, stat_date, author)
	);

	CREATE TABLE IF NOT EXISTS app_config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS project_todos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		completed BOOLEAN DEFAULT 0,
		priority INTEGER DEFAULT 0,
		sort_order INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
	);

	-- The topic layer between a project and its notes (schema v27). A topic is
	-- a real entity scoped to its project, so renaming it is one UPDATE and
	-- "how many notes are in this topic" is answerable. Declared before
	-- project_notes because the latter references it.
	CREATE TABLE IF NOT EXISTS note_topics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		description TEXT DEFAULT '',
		sort_order INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_note_topics_project_name ON note_topics(project_id, name);

	CREATE TABLE IF NOT EXISTS project_notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		title TEXT DEFAULT '',
		content TEXT NOT NULL,
		tags TEXT DEFAULT '',
		kind TEXT DEFAULT 'other',
		pinned INTEGER DEFAULT 0,
		source TEXT DEFAULT 'manual',
		sort_order INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		topic_id INTEGER,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		FOREIGN KEY (topic_id) REFERENCES note_topics(id) ON DELETE SET NULL
	);

	CREATE TABLE IF NOT EXISTS repo_meta (
		repository_id INTEGER PRIMARY KEY,
		tech_stack TEXT DEFAULT '[]',
		readme_excerpt TEXT DEFAULT '',
		languages TEXT DEFAULT '{}',
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
	);
	`

	_, err := db.Exec(schema)
	return err
}
