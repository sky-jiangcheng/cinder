package writerapi

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"repotory/internal/db"
	"repotory/internal/service"
)

// Gate ② of ADR-0058 / TODO 13.7: a process that is not the writer must not
// write the shared database, and the writer's absence must not change that.
//
// The shape the production entry points use (TODO 13.7): the election comes
// BEFORE the database is opened, so the role decides how the file is opened.
// The tests below reproduce that order instead of the manager's internal
// New(), because the open path is where the write used to happen.

// electAndOpen runs the production order: elect, open the database for the
// role, then adopt the election.
//
// It returns the handle, the manager, and whether the election was won.
func electAndOpen(t *testing.T, dbPath, process string) (*sql.DB, *Manager, bool) {
	t.Helper()

	election, err := Elect(Options{
		DBPath:           dbPath,
		Process:          process,
		Surface:          service.AISurfaceMCP,
		SelfElectAllowed: true,
	})
	if err != nil {
		t.Fatalf("Elect(%s): %v", process, err)
	}

	var d *sql.DB
	if election.Won() {
		d, err = db.OpenWithRetry(dbPath)
	} else {
		d, err = db.OpenReader(dbPath)
	}
	if err != nil {
		t.Fatalf("open the database for %s: %v", process, err)
	}
	t.Cleanup(func() { _ = d.Close() })

	m, err := NewWithElection(service.New(d, process), Options{
		DBPath:           dbPath,
		DB:               d,
		SchemaReady:      election.Won(),
		Process:          process,
		Surface:          service.AISurfaceMCP,
		SelfElectAllowed: true,
	}, election)
	if err != nil {
		t.Fatalf("NewWithElection(%s): %v", process, err)
	}
	t.Cleanup(m.Shutdown)
	return d, m, election.Won()
}

// The first half of gate ②: while the writer is alive, the client writes
// nothing to the shared file — its business writes go through the forward
// path, and its own handle refuses writes outright.
func TestNonWriterWritesNothingToTheSharedDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")

	writerDB, writerMgr, won := electAndOpen(t, dbPath, "cinder-mcp-writer")
	if !won {
		t.Fatal("the first process did not win the election")
	}
	if !writerMgr.IsWriter() {
		t.Fatal("the winner does not hold the writer role")
	}

	clientDB, clientMgr, won := electAndOpen(t, dbPath, "cinder-mcp-client")
	if won {
		t.Fatal("the second process won the election while the writer holds the lock")
	}
	if clientMgr.IsWriter() {
		t.Fatal("the second process claims the writer role")
	}

	// The client's handle is read-only: it cannot write even if it tried.
	if !db.ReadOnly(clientDB) {
		t.Fatal("the client's handle is writable: a non-writer process can write to the shared database")
	}
	if _, err := clientDB.Exec("INSERT INTO app_config(key, value) VALUES('client_direct','1')"); err == nil {
		t.Fatal("the client's handle accepted a direct write")
	}

	// ...and its business write still lands, through the writer.
	pid := seedProject(t, writerDB, "forwarded")
	var note serviceNote
	if err := clientMgr.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "forwarded", Content: "written by the writer on our behalf",
		Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatalf("forwarded notes.create: %v", err)
	}
	if note.ID == 0 {
		t.Fatal("the forwarded write did not land")
	}
	var count int
	if err := writerDB.QueryRow(`SELECT COUNT(*) FROM project_notes WHERE id = ?`, note.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("read the forwarded row back: count=%d err=%v", count, err)
	}
}

// The second half of gate ②: the schema is the writer's job. A client that
// opens the shared database must not migrate it, and a writer that takes over
// must finish what the previous writer left undone.
func TestTheSchemaIsTheWritersWork(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")

	writerDB, _, won := electAndOpen(t, dbPath, "cinder-mcp-writer")
	if !won {
		t.Fatal("the first process did not win the election")
	}

	// Break the derived wiki layer, the way a writer that died mid-migration
	// leaves it behind.
	if err := db.DropWikiSchema(writerDB); err != nil {
		t.Fatalf("drop the wiki layer: %v", err)
	}

	// The client opens the same file and must leave the broken schema alone.
	clientDB, _, _ := electAndOpen(t, dbPath, "cinder-mcp-client")
	if !db.ReadOnly(clientDB) {
		t.Fatal("the client's handle is writable")
	}
	var n int
	if err := clientDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wiki_pages'").Scan(&n); err != nil {
		t.Fatalf("check the wiki layer: %v", err)
	}
	if n != 0 {
		t.Fatal("the client did the writer's schema work on open")
	}
}

// The writer's absence must not turn the client into a writer of the shared
// file behind the contract's back: the client takes the ROLE (and inherits the
// schema duty), which is the only way it is allowed to write.
func TestTheWriterAbsenceMovesTheRoleAndTheSchemaWork(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")

	writerDB, writerMgr, won := electAndOpen(t, dbPath, "cinder-mcp-writer")
	if !won {
		t.Fatal("the first process did not win the election")
	}
	clientDB, clientMgr, _ := electAndOpen(t, dbPath, "cinder-mcp-client")
	if clientMgr.IsWriter() {
		t.Fatal("the second process claims the writer role")
	}
	if !db.ReadOnly(clientDB) {
		t.Fatal("the client's handle is writable before the takeover")
	}
	pid := seedProject(t, writerDB, "takeover")

	// Leave the schema half-migrated, so the takeover can be seen to finish it.
	if err := db.DropWikiSchema(writerDB); err != nil {
		t.Fatalf("drop the wiki layer: %v", err)
	}

	// The writer dies.
	writerMgr.Shutdown()

	var note serviceNote
	if err := clientMgr.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "after-takeover", Content: "x", Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatalf("write after the writer died: %v", err)
	}
	if note.ID == 0 {
		t.Fatal("the takeover write did not land")
	}
	if !clientMgr.IsWriter() {
		t.Fatal("the surviving client did not take over the writer role")
	}
	if db.ReadOnly(clientDB) {
		t.Fatal("the takeover did not make the client's handle writable")
	}

	// The new writer inherits the schema duty: the layer the dead writer left
	// half-migrated is back.
	var n int
	if err := clientDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='wiki_pages'").Scan(&n); err != nil {
		t.Fatalf("check the restored wiki layer: %v", err)
	}
	if n != 1 {
		t.Fatal("the new writer did not finish the migration it inherited")
	}
}

// A client that cannot take the role must not fall back to writing the shared
// file: the handle stays read-only, and the write is refused by the contract
// once nobody can serve it.
func TestAClientThatCannotTakeOverStillDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")

	writerDB, writerMgr, won := electAndOpen(t, dbPath, "cinder-mcp-writer")
	if !won {
		t.Fatal("the first process did not win the election")
	}
	pid := seedProject(t, writerDB, "server-write")

	// A process that may not self-elect: the server's policy.
	election, err := Elect(Options{DBPath: dbPath, Process: "cinder-server", Surface: service.AISurfaceMCP})
	if err != nil {
		t.Fatalf("Elect(cinder-server): %v", err)
	}
	if election.Won() {
		t.Fatal("a process that may not self-elect took the writer role")
	}
	clientDB, err := db.OpenReader(dbPath)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer clientDB.Close() //nolint:errcheck // test teardown

	m, err := NewWithElection(service.New(clientDB, "cinder-server"), Options{
		DBPath:           dbPath,
		DB:               clientDB,
		SchemaReady:      false,
		Process:          "cinder-server",
		Surface:          service.AISurfaceMCP,
		SelfElectAllowed: false,
	}, election)
	if err != nil {
		t.Fatalf("NewWithElection(cinder-server): %v", err)
	}
	defer m.Shutdown() //nolint:errcheck // test teardown

	if m.IsWriter() {
		t.Fatal("the server claims the writer role")
	}
	if !db.ReadOnly(clientDB) {
		t.Fatal("the server's handle is writable")
	}
	if _, err := clientDB.Exec("INSERT INTO app_config(key, value) VALUES('server_direct','1')"); err == nil {
		t.Fatal("the server's handle accepted a direct write")
	}

	// The writer is alive, so the write goes forward and lands — on the
	// writer's handle, not on the server's.
	var note serviceNote
	if err := m.ExecuteInto(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "forwarded-by-server", Content: "x", Kind: "knowledge", Source: "mcp",
	}, &note); err != nil {
		t.Fatalf("forwarded notes.create: %v", err)
	}
	if note.ID == 0 {
		t.Fatal("the forwarded write did not land")
	}

	// Now nobody can serve it: the server may not take the role, so the write
	// must be refused — not answered by writing to the shared file itself.
	writerMgr.Shutdown()
	if _, err := m.Execute(context.Background(), MethodNotesCreate, NotesCreateArgs{
		ProjectID: pid, Title: "no-writer", Content: "x", Kind: "knowledge", Source: "mcp",
	}); err == nil {
		t.Fatal("the write was accepted while no process holds the writer role")
	}
	if !db.ReadOnly(clientDB) {
		t.Fatal("the server's handle became writable without the writer role")
	}
}

// The election files live beside the database, so "same database" and "same
// writer" are one statement. A database in another directory has its own
// writer.
func TestTheElectionIsPerDatabase(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	_, mgrA, wonA := electAndOpen(t, filepath.Join(dirA, "repotory.db"), "cinder-mcp-a")
	if !wonA {
		t.Fatal("the first database did not elect a writer")
	}
	_, mgrB, wonB := electAndOpen(t, filepath.Join(dirB, "repotory.db"), "cinder-mcp-b")
	if !wonB {
		t.Fatal("the second database did not elect its own writer")
	}
	if !mgrA.IsWriter() || !mgrB.IsWriter() {
		t.Fatalf("two databases cannot both hold the role: a=%s b=%s",
			RoleName(mgrA.Role()), RoleName(mgrB.Role()))
	}

	// The election files are beside the database they belong to.
	if _, err := os.Stat(filepath.Join(dirA, "repotory.writer.lock")); err != nil {
		t.Fatalf("the election lock is not beside the database: %v", err)
	}
}
