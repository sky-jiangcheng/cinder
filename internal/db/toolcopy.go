package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Throwaway copies for tooling (the cmd/wiki-rehearse discipline, made reusable).
//
// Why. Several binaries under cmd/ are analysis or export tools: abeval measures
// retrieval quality, queryset builds a labelling worksheet, wiki-export renders the
// page layer to Markdown. None of them should change the user's database. Yet all
// three opened their `-db` argument (which defaults to the live application
// database) through InitDB — and InitDB runs upgradeSchema, which writes: it stamps
// schema_version, creates tables, and backfills. So "run the evaluation tool"
// silently migrated the production database as a side effect of opening it.
//
// abeval is worse than a migration surprise: with an embedding endpoint configured
// it calls service.RebuildEmbeddings, which rewrites the whole note index and
// persists embedding_dim. An A/B gate that mutates the thing it is measuring is not
// a measurement.
//
// So: open the source strictly read-only, copy it with VACUUM INTO a temp file, and
// hand the caller a database that is a real Cinder database at the CURRENT schema
// — with every write, migration and index rebuild landing on the copy. This is the
// same shape cmd/wiki-rehearse already used; promoting it here means the next
// analysis tool starts safe instead of rediscovering why.
//
// The copy is a snapshot: reads are point-in-time and immune to the desktop app
// writing underneath them, which for a tool that prints a report is the correct
// semantics anyway.

// OpenCopyOptions tunes CopyForTooling. The zero value is the safe default.
type OpenCopyOptions struct {
	// KeepCopy leaves the temp file on disk after the handle is closed and returns
	// its path in Cleanup's absence; used by tools whose artifact IS the copy
	// (cmd/wiki-rehearse). Everything else should let it go.
	KeepCopy bool
}

// Copy is a throwaway database derived from a source the caller must not modify.
type Copy struct {
	DB   *sql.DB
	Path string
	// Close releases the handle and, unless KeepCopy was set, removes the temp
	// file. Removing only the file this call created is not a user-data deletion;
	// leaving stray multi-megabyte database copies behind would be the worse bug.
	Close func() error
}

// CopyForTooling opens srcPath read-only, VACUUM INTOs a copy, migrates the COPY
// with InitDB, and returns it. The source is never written: the only statement run
// against it is VACUUM INTO, which writes to the new file.
func CopyForTooling(srcPath string, opt OpenCopyOptions) (*Copy, error) {
	abs, err := filepath.Abs(srcPath)
	if err != nil {
		return nil, fmt.Errorf("db: resolve %s: %w", srcPath, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("db: source database: %w", err)
	}

	// mode=ro is enforced by SQLite, not by convention: any write attempt fails
	// rather than being politely avoided.
	src, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("db: open source read-only: %w", err)
	}
	defer func() { _ = src.Close() }()

	// A single probe gives a far better error than "VACUUM INTO: no such table"
	// when the path is not a Cinder database at all.
	var probe int
	if err := src.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&probe); err != nil {
		return nil, fmt.Errorf("db: %s is not a readable SQLite database: %w", abs, err)
	}

	dir, err := os.MkdirTemp("", "repotory-tooling-")
	if err != nil {
		return nil, fmt.Errorf("db: temp dir for copy: %w", err)
	}
	copyPath := filepath.Join(dir, "copy.db")

	if _, err := src.Exec("VACUUM INTO '" + strings.ReplaceAll(copyPath, "'", "''") + "'"); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("db: VACUUM INTO copy: %w", err)
	}

	// Migrations run HERE. This is the whole point of the function: the schema
	// upgrade a tool needs in order to query the new tables is paid by the copy,
	// never by the user's database.
	migrated, err := InitDB(copyPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("db: migrate copy: %w", err)
	}

	c := &Copy{DB: migrated, Path: copyPath}
	if opt.KeepCopy {
		c.Close = func() error { return migrated.Close() }
		return c, nil
	}
	c.Close = func() error {
		cerr := migrated.Close()
		rerr := os.RemoveAll(dir)
		if cerr != nil {
			return cerr
		}
		return rerr
	}
	return c, nil
}

// ErrSchemaBehind is returned by RequireSchemaVersion when a database is older than
// the code reading it. A tool that silently queries a half-migrated store reports
// confident nonsense, which is the failure this exists to make impossible.
var ErrSchemaBehind = errors.New("database schema is older than this binary")

// RequireSchemaVersion checks a database's stamped migration against the version
// the caller's code expects (integrity.ExpectedSchemaVersion). It is separate from
// CopyForTooling because a read-only caller may legitimately open a database it is
// NOT allowed to migrate — in that case refusing to proceed is the only honest
// option, and the message says exactly what to do about it.
func RequireSchemaVersion(database *sql.DB, expected int) error {
	got, err := readSchemaVersion(database)
	if err != nil {
		return fmt.Errorf("db: read schema version: %w", err)
	}
	if got < expected {
		return fmt.Errorf("%w: database is at v%d, this binary needs v%d — open it with the desktop app once to migrate it, or run the tool against a copy (db.CopyForTooling)",
			ErrSchemaBehind, got, expected)
	}
	return nil
}
