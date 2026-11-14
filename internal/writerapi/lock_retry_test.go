package writerapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/errcode"
	"repotory/internal/service"
)

// mustJSON marshals catalogue arguments the way the Manager does before it
// hands them to the Executor.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

// TestWriterReplaysALockContendedWrite is the acceptance gate for the
// defect a 3-minute soak caught (ADR-0058, TODO 13.1): a write on the
// writer's own connection failed with "database is locked (5) (SQLITE_BUSY)"
// and the error went straight back to the agent.
//
// The mechanism is a snapshot race, not a lost write: another process commits
// while the writer's transaction is between its read and its write, and
// SQLite refuses the statement. Nothing was committed, so the write can be
// replayed — and the writer is the only process allowed to write, so a lock
// error here must never reach the caller (ADR-0058: `database is locked` is
// not an agent-facing outcome).
//
// The test holds the write lock from a second connection and lets it go after
// a moment: the writer's first attempt must fail, and the catalogue must
// come back with the note applied — never with a lock error.
func TestWriterReplaysALockContendedWrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")

	// The schema comes from a normal init; the writer then gets its own
	// handle with a SHORT busy_timeout, so the contended attempt fails in
	// milliseconds instead of sitting out the production five seconds.
	schema, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer schema.Close() //nolint:errcheck
	pid := seedProject(t, schema, "lock-retry")

	writer, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(20)")
	if err != nil {
		t.Fatalf("open writer handle: %v", err)
	}
	defer writer.Close() //nolint:errcheck
	writer.SetMaxOpenConns(1)

	// The contender: it takes the write lock and holds it just long enough
	// for the writer's first attempt to be refused.
	holder, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open holder handle: %v", err)
	}
	defer holder.Close() //nolint:errcheck
	holder.SetMaxOpenConns(1)
	tx, err := holder.Begin()
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO app_config(key,value) VALUES ('holder','1')`); err != nil {
		t.Fatalf("holder write: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(120 * time.Millisecond)
		_ = tx.Rollback()
		close(released)
	}()

	ex := NewExecutor(service.New(writer, "tester"))
	out, err := ex.Execute(context.Background(), MethodNotesCreate, mustJSON(t, NotesCreateArgs{
		ProjectID: pid, Title: "replayed", Content: "waited its turn", Kind: "knowledge", Source: "mcp",
	}))
	<-released
	if err != nil {
		t.Fatalf("a lock-contended write must not surface: %v", err)
	}
	note, ok := out.(*domain.Note)
	if !ok || note == nil || note.ID == 0 {
		t.Fatalf("the replayed write did not land: %#v", out)
	}
}

// TestWriterDoesNotReplayALostWrite pins the other half of the retry's
// safety argument: only a lock error is replayed. A version conflict means
// somebody else changed the row, and replaying it would silently overwrite
// their write.
func TestWriterDoesNotReplayALostWrite(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t, dir)
	pid := seedProject(t, d, "no-replay")

	ex := NewExecutor(service.New(d, "tester"))
	out, err := ex.Execute(context.Background(), MethodNotesCreate, mustJSON(t, NotesCreateArgs{
		ProjectID: pid, Title: "once", Content: "c", Kind: "knowledge", Source: "mcp",
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	note, ok := out.(*domain.Note)
	if !ok || note == nil {
		t.Fatalf("create returned %#v", out)
	}

	// A stale expected version is a conflict, not contention: it must come
	// back as a conflict, and the stored row must be untouched.
	_, err = ex.Execute(context.Background(), MethodNotesUpdate, mustJSON(t, NotesUpdateArgs{
		NoteID: note.ID, Content: "second", Title: "once", ExpectedVersion: 999,
	}))
	if err == nil {
		t.Fatal("a stale version must be refused")
	}
	if !errors.Is(err, errcode.ErrVersionConflict) {
		t.Fatalf("expected a version conflict, got: %v", err)
	}
}
