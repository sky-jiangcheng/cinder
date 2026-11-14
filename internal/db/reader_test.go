package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Gate ① of ADR-0058 / TODO 13.7, extended by TODO 13.8: a process that is
// not the writer must not write to the shared database — not the main file,
// not the WAL, and not even create it.
//
// The old open path wrote on every process start (WAL pragma, eight Ensure*
// re-assertions, the migration stamp, a full FTS rebuild). That is what made
// `database is locked` reach an agent tool call: a fresh cinder-mcp opened the
// database while the writer was inside a write transaction.
//
// TODO 13.8 closes the last hole of that gate: the reader's read-only state
// used to be a per-connection pragma, and a pragma does not stop the reader
// from CREATING the database when it was missing. Now the file itself is
// opened read-only.
//
// The measurement below is deliberately made against a database that already
// has WAL content, so "the WAL did not grow" is a real observation and not the
// vacuous truth that an empty WAL cannot grow.

// fileState is the observable state of a database file: its size and its
// last-write time.
func fileState(path string) (size int64, mtime int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return fi.Size(), fi.ModTime().UnixNano()
}

// walFrames counts the frames in the WAL sidecar.
//
// The WAL header is 32 bytes; every frame is a 24-byte header plus one page.
func walFrames(path string, pageSize int) int {
	fi, err := os.Stat(path + "-wal")
	if err != nil {
		return 0
	}
	const walHeaderSize = 32
	const frameHeaderSize = 24
	size := fi.Size()
	if size <= walHeaderSize {
		return 0
	}
	return int((size - walHeaderSize) / (int64(pageSize) + frameHeaderSize))
}

func TestReaderOpenWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	// Give the WAL something to hold, so the frame count below is a
	// measurement rather than a tautology.
	if _, err := writer.Exec("INSERT INTO app_config(key, value) VALUES('probe_seed','1')"); err != nil {
		t.Fatalf("seed the writer database: %v", err)
	}

	var pageSize int
	if err := writer.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatalf("read the page size: %v", err)
	}
	if frames := walFrames(path, pageSize); frames == 0 {
		t.Fatalf("the WAL holds no frames — the test would prove nothing")
	}

	sizeBefore, mtimeBefore := fileState(path)
	framesBefore := walFrames(path, pageSize)

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer reader.Close() //nolint:errcheck // test teardown

	if !ReadOnly(reader) {
		t.Fatal("the reader handle is not read-only: a non-writer process can write to the shared database")
	}

	// Read the way the MCP tools do, so the read path is exercised too.
	var n int
	if err := reader.QueryRow("SELECT count(*) FROM app_config").Scan(&n); err != nil {
		t.Fatalf("read through the reader handle: %v", err)
	}

	sizeAfter, mtimeAfter := fileState(path)
	framesAfter := walFrames(path, pageSize)

	if sizeAfter != sizeBefore {
		t.Errorf("the shared database file changed size while the reader was running: %d -> %d", sizeBefore, sizeAfter)
	}
	if mtimeAfter != mtimeBefore {
		t.Errorf("the shared database file was rewritten while the reader was running")
	}
	if framesAfter != framesBefore {
		t.Errorf("the WAL grew while the reader was running: %d -> %d frames", framesBefore, framesAfter)
	}
}

// A read-only handle that merely claims to be read-only would be no gate at
// all: the contract has to fail loudly at the SQLite level.
func TestReaderRefusesWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer reader.Close() //nolint:errcheck // test teardown

	if _, err := reader.Exec("INSERT INTO app_config(key, value) VALUES('reader_write','1')"); err == nil {
		t.Fatal("the reader handle accepted a write: a non-writer process can corrupt the shared database")
	}

	// The refused write must have left nothing behind.
	var n int
	if err := reader.QueryRow("SELECT count(*) FROM app_config WHERE key='reader_write'").Scan(&n); err != nil {
		t.Fatalf("count the refused rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused write landed anyway: %d rows", n)
	}
}

// Gate ② of TODO 13.8: the reader's read-only state is a FILE-level
// restriction, not a pragma.
//
// The old reader asked for query_only=1, which is a per-connection pragma:
// any later statement could turn it off again, and it had nothing to say
// about a connection opened from another DSN. The handle is now opened with
// SQLite's mode=ro, so the FILE is read-only — turning the pragma off does
// not help, because the write fails at the file, not at the pragma.
func TestReaderIsReadOnlyAtTheFileLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer reader.Close() //nolint:errcheck // test teardown

	// Take the pragma off — the old reader became writable right here.
	if _, err := reader.Exec("PRAGMA query_only=OFF"); err != nil {
		t.Fatalf("the pragma could not be turned off: %v", err)
	}
	if _, err := reader.Exec("INSERT INTO app_config(key, value) VALUES('pragma_off_write','1')"); err == nil {
		t.Fatal("the reader wrote to the shared database with the pragma turned off: the restriction is only a pragma")
	}

	// The refused write must have left nothing behind.
	var n int
	if err := reader.QueryRow("SELECT count(*) FROM app_config WHERE key='pragma_off_write'").Scan(&n); err != nil {
		t.Fatalf("count the refused rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused write landed anyway: %d rows", n)
	}
}

// Gate ① of TODO 13.8: the reader does not create the database. The writer
// creates it; a reader that arrives first waits for the writer and then
// fails with an error that says what to do.
func TestReaderDoesNotCreateTheMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	reader, err := openReader(path, 300*time.Millisecond)
	if err == nil {
		reader.Close() //nolint:errcheck // test teardown
		t.Fatal("the reader opened a database that does not exist")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("the reader created the database: the file belongs to the writer")
	}
	if !strings.Contains(err.Error(), "no writer has created it") {
		t.Fatalf("the error does not say what to do about it: %v", err)
	}
}

// The desktop's client role is a writer by necessity (the human-gate writes
// have no forward path), but it is still not the creator of the database.
func TestClientDoesNotCreateTheMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	client, err := openExisting(path, 300*time.Millisecond)
	if err == nil {
		client.Close() //nolint:errcheck // test teardown
		t.Fatal("the client opened a database that does not exist")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("the client created the database: the file belongs to the writer")
	}
}

// The reader handle is not a dead end: when the writer dies, this process takes
// the writer role, and the handle must be able to write.
func TestPromoteToWriterMakesTheReaderWritable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer reader.Close() //nolint:errcheck // test teardown

	// Read first, so the pool holds a connection that was opened read-only.
	var n int
	if err := reader.QueryRow("SELECT count(*) FROM app_config").Scan(&n); err != nil {
		t.Fatalf("read through the reader handle: %v", err)
	}

	promoted, err := PromoteToWriter(reader)
	if err != nil {
		t.Fatalf("promote the reader: %v", err)
	}
	if !promoted {
		t.Fatal("PromoteToWriter reported no change — the handle was not read-only to begin with")
	}
	if ReadOnly(reader) {
		t.Fatal("the handle is still read-only after being promoted to the writer role")
	}
	if _, err := reader.Exec("INSERT INTO app_config(key, value) VALUES('writer_write','1')"); err != nil {
		t.Fatalf("write through the promoted handle: %v", err)
	}

	// The write has to be in the database, not just accepted.
	if err := writer.QueryRow("SELECT count(*) FROM app_config WHERE key='writer_write'").Scan(&n); err != nil {
		t.Fatalf("count the promoted rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("the promoted write did not land: %d rows", n)
	}

	// A handle that is already writable needs no promotion.
	promoted, err = PromoteToWriter(reader)
	if err != nil {
		t.Fatalf("promote an already-writable handle: %v", err)
	}
	if promoted {
		t.Fatal("PromoteToWriter reported a change on a handle that was already writable")
	}
}

// The promotion has to survive the pool replacing the connection.
//
// The file was opened read-only, and SQLite cannot upgrade an open file
// handle — so the promotion hands out connections built from the writable
// DSN instead, and the read-only connections the pool still holds are
// dropped. Every later connection has to be writable too.
func TestPromotedReaderStaysWritableAfterAConnectionIsReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer reader.Close() //nolint:errcheck // test teardown

	// Read, so the pool holds a connection that was opened read-only.
	var n int
	if err := reader.QueryRow("SELECT count(*) FROM app_config").Scan(&n); err != nil {
		t.Fatalf("read through the reader handle: %v", err)
	}

	if _, err := PromoteToWriter(reader); err != nil {
		t.Fatalf("promote the reader: %v", err)
	}

	// The write goes through the pool, which still holds the read-only
	// connection: the pool has to drop it and hand out a writable one.
	if _, err := reader.Exec("INSERT INTO app_config(key, value) VALUES('replaced_write','1')"); err != nil {
		t.Fatalf("write through the promoted handle: %v", err)
	}

	// Now make the pool throw its connection away, so the next statement
	// opens a brand new one.
	reader.SetMaxIdleConns(0)
	if _, err := reader.Exec("INSERT INTO app_config(key, value) VALUES('replacement_write','1')"); err != nil {
		t.Fatalf("the replacement connection is read-only: the promotion did not survive it: %v", err)
	}

	if err := writer.QueryRow("SELECT count(*) FROM app_config WHERE key IN ('replaced_write','replacement_write')").Scan(&n); err != nil {
		t.Fatalf("count the promoted rows: %v", err)
	}
	if n != 2 {
		t.Fatalf("the promoted writes did not land: %d rows", n)
	}
}

// Gate ③ of TODO 13.8: the schema is the writer's work, and only the writer
// does it.
//
// A reader that repaired the schema would write to the shared database — the
// very thing the reader path exists to prevent. So a reader that opens a
// database with a broken derived layer leaves it broken.
func TestReaderDoesNotRepairTheSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	// Break the derived wiki layer the way a half-finished migration would.
	if err := DropWikiSchema(writer); err != nil {
		t.Fatalf("drop the wiki layer: %v", err)
	}

	reader, err := OpenReader(path)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer reader.Close() //nolint:errcheck // test teardown

	var n int
	if err := reader.QueryRow("SELECT count(*) FROM app_config").Scan(&n); err != nil {
		t.Fatalf("read through the reader handle: %v", err)
	}
	if err := reader.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wiki_pages'").Scan(&n); err != nil {
		t.Fatalf("check the dropped layer through the reader: %v", err)
	}
	if n != 0 {
		t.Fatal("the reader repaired the schema: a non-writer process wrote to the shared database")
	}
}

// The writer repairs the schema it inherits: a writer that took over inherits
// the duty, so the self-healing layers come back with the role.
func TestEnsureSchemaRestoresTheDerivedLayers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	// Break the derived wiki layer the way a half-finished migration would.
	if err := DropWikiSchema(writer); err != nil {
		t.Fatalf("drop the wiki layer: %v", err)
	}
	var n int
	if err := writer.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wiki_pages'").Scan(&n); err != nil {
		t.Fatalf("check the dropped layer: %v", err)
	}
	if n != 0 {
		t.Fatal("the wiki layer was not dropped — the test would prove nothing")
	}

	if err := EnsureSchema(writer); err != nil {
		t.Fatalf("re-assert the schema: %v", err)
	}
	if err := writer.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wiki_pages'").Scan(&n); err != nil {
		t.Fatalf("check the restored layer: %v", err)
	}
	if n != 1 {
		t.Fatal("the schema work did not restore the wiki layer it inherited")
	}
}

// The desktop's client role: the handle stays writable (the human-gate writes
// have no forward path), but opening it must still not do the writer's schema
// work — that is what used to collide with the writer's lock.
func TestOpenExistingDoesNotTouchTheSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repotory.db")

	writer, err := InitDB(path)
	if err != nil {
		t.Fatalf("init the writer database: %v", err)
	}
	defer writer.Close() //nolint:errcheck // test teardown

	if err := DropWikiSchema(writer); err != nil {
		t.Fatalf("drop the wiki layer: %v", err)
	}

	var pageSize int
	if err := writer.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatalf("read the page size: %v", err)
	}
	sizeBefore, mtimeBefore := fileState(path)
	framesBefore := walFrames(path, pageSize)

	client, err := OpenExisting(path)
	if err != nil {
		t.Fatalf("open the client handle: %v", err)
	}
	defer client.Close() //nolint:errcheck // test teardown

	if ReadOnly(client) {
		t.Fatal("the client handle is read-only, but the desktop's human-gate writes have no forward path")
	}

	// The schema is the writer's: the client must not have re-created the
	// layer it dropped.
	var n int
	if err := client.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wiki_pages'").Scan(&n); err != nil {
		t.Fatalf("check the wiki layer: %v", err)
	}
	if n != 0 {
		t.Fatal("the client handle did the writer's schema work on open")
	}

	sizeAfter, mtimeAfter := fileState(path)
	framesAfter := walFrames(path, pageSize)

	if sizeAfter != sizeBefore {
		t.Errorf("the shared database file changed size while the client was running: %d -> %d", sizeBefore, sizeAfter)
	}
	if mtimeAfter != mtimeBefore {
		t.Errorf("the shared database file was rewritten while the client was running")
	}
	if framesAfter != framesBefore {
		t.Errorf("the WAL grew while the client was running: %d -> %d frames", framesBefore, framesAfter)
	}
}
