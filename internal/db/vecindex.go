package db

// The pure-Go sqlite-vec extension is imported for its side effect, making the
// `vec0` virtual table and `vec_*` functions available on every connection
// (verified zero-CGO in internal/vecprobe). Its only global side effect is that
// sqlite3_auto_extension initialises SQLite, which rules out
// sqlite.RegisterPageCache — Cinder never uses a custom page cache, so this is
// safe (ADR-0012 决策 1).
import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite/vec"
)

// Vector embeddings are stored in a `vec0` virtual table keyed by note rowid.
// The table is a DERIVED cache (ADR-0012 决策 5): SQLite `project_notes` remains
// the single source of truth, and note_embeddings can always be dropped and
// rebuilt from it. It exists only when semantic search is enabled.

const maxEmbeddingDim = 8192

// vecTableName is the derived vector index. Kept unquoted so vec0's auxiliary
// `<table>_info`/`<table>_chunks` shadow tables follow the same name.
const vecTableName = "note_embeddings"

// vecMetaTable records the dimension of the current vec index. We track it
// ourselves rather than introspecting sqlite-vec's shadow tables (whose column
// names are not a stable public contract across versions), so a model/dim swap
// reliably triggers a rebuild.
const vecMetaTable = "note_embeddings_meta"

// VectorIndexReady reports whether the note_embeddings vec0 table exists.
func VectorIndexReady(db *sql.DB) bool {
	var n int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", vecTableName).Scan(&n)
	return err == nil && n > 0
}

// EnsureVectorIndex creates the vec0 table sized to dim, dropping it first if a
// table with a DIFFERENT dim already exists (a model swap changes dim; the cache
// is then rebuilt from scratch). dim must be 1..maxEmbeddingDim.
func EnsureVectorIndex(db *sql.DB, dim int) error {
	if dim < 1 || dim > maxEmbeddingDim {
		return fmt.Errorf("db: invalid embedding dim %d (want 1..%d)", dim, maxEmbeddingDim)
	}
	if VectorIndexReady(db) {
		if cur, ok := storedVectorDim(db); ok && cur == dim {
			return nil // already the right shape
		}
		if err := DropVectorIndex(db); err != nil {
			return err
		}
	}
	// dim is a validated int, so Sprintf injection is not a concern here.
	ddl := fmt.Sprintf("CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(embedding float[%d])", vecTableName, dim)
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("db: create vec index: %w", err)
	}
	if _, err := db.Exec(
		"CREATE TABLE IF NOT EXISTS " + vecMetaTable + " (dim INTEGER NOT NULL)"); err != nil {
		return fmt.Errorf("db: create vec meta: %w", err)
	}
	if _, err := db.Exec("DELETE FROM " + vecMetaTable); err != nil {
		return err
	}
	_, err := db.Exec("INSERT INTO "+vecMetaTable+"(dim) VALUES(?)", dim)
	return err
}

// storedVectorDim reads the tracked dimension from the meta table.
func storedVectorDim(db *sql.DB) (dim int, ok bool) {
	err := db.QueryRow("SELECT dim FROM " + vecMetaTable + " LIMIT 1").Scan(&dim)
	if err != nil {
		return 0, false
	}
	return dim, true
}

// StoredVectorDim is the exported view of the local index's dimension, used by
// the incremental drainer when `embedding_dim` is unset: an index built before
// that key was persisted still knows its own width, and guessing wrong would
// write vectors that KNN silently mis-ranks.
func StoredVectorDim(db *sql.DB) (int, bool) { return storedVectorDim(db) }

// DropVectorIndex removes the derived vector table + its meta row.
func DropVectorIndex(db *sql.DB) error {
	if _, err := db.Exec("DROP TABLE IF EXISTS " + vecTableName); err != nil {
		return fmt.Errorf("db: drop vec index: %w", err)
	}
	if _, err := db.Exec("DROP TABLE IF EXISTS " + vecMetaTable); err != nil {
		return fmt.Errorf("db: drop vec meta: %w", err)
	}
	return nil
}

// ClearVectorIndex empties the vector table without dropping it (used before a
// full re-embed).
func ClearVectorIndex(db *sql.DB) error {
	if _, err := db.Exec("DELETE FROM " + vecTableName); err != nil {
		return fmt.Errorf("db: clear vec index: %w", err)
	}
	return nil
}

// PutNoteEmbedding upserts a note's vector, keyed by note id (the vec0 rowid).
// vec0 does NOT honour INSERT OR REPLACE for an existing rowid (it raises
// "UNIQUE constraint failed ... primary key"), so the upsert is an explicit
// delete-then-insert: the incremental drainer re-embeds edited notes through
// this path.
func PutNoteEmbedding(db *sql.DB, noteID int64, vec []float32) error {
	blob, err := encodeVector(vec)
	if err != nil {
		return err
	}
	if _, err := db.Exec("DELETE FROM "+vecTableName+" WHERE rowid = ?", noteID); err != nil {
		return fmt.Errorf("db: replace embedding note %d: %w", noteID, err)
	}
	if _, err := db.Exec(
		fmt.Sprintf("INSERT INTO %s(rowid, embedding) VALUES(?, ?)", vecTableName),
		noteID, blob); err != nil {
		return fmt.Errorf("db: put embedding note %d: %w", noteID, err)
	}
	return nil
}

// DeleteNoteEmbedding removes a note's vector (note deleted).
func DeleteNoteEmbedding(db *sql.DB, noteID int64) error {
	_, err := db.Exec("DELETE FROM "+vecTableName+" WHERE rowid = ?", noteID)
	return err
}

// KnnNoteIDs returns the note ids of the k vectors nearest to `query`, ordered
// by ascending distance (most similar first).
func KnnNoteIDs(db *sql.DB, query []float32, k int) ([]int64, error) {
	if k <= 0 {
		return nil, nil
	}
	blob, err := encodeVector(query)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		fmt.Sprintf("SELECT rowid FROM %s WHERE embedding MATCH ? AND k = ? ORDER BY distance", vecTableName),
		blob, k)
	if err != nil {
		return nil, fmt.Errorf("db: knn: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// VectorStoreHealthCheck verifies the vec0 store works end to end: ensure the
// index at `dim`, write a probe vector at a sentinel rowid, confirm KNN returns
// it, then delete the probe. Returns nil only if the round-trip succeeds. Leaves
// a correctly-sized (probe-clean) index behind. Used by cmd/vector-init.
func VectorStoreHealthCheck(db *sql.DB, dim int) error {
	if dim < 1 || dim > maxEmbeddingDim {
		return fmt.Errorf("db: health check: invalid dim %d", dim)
	}
	if err := EnsureVectorIndex(db, dim); err != nil {
		return err
	}
	const probeRowid = int64(-1) // note ids are positive; -1 is a safe sentinel
	probe := make([]float32, dim)
	probe[0] = 1
	if err := PutNoteEmbedding(db, probeRowid, probe); err != nil {
		return err
	}
	defer func() { _ = DeleteNoteEmbedding(db, probeRowid) }()
	ids, err := KnnNoteIDs(db, probe, 1)
	if err != nil {
		return err
	}
	if len(ids) == 0 || ids[0] != probeRowid {
		return fmt.Errorf("db: health check KNN returned %v, want probe %d", ids, probeRowid)
	}
	return nil
}

// NoteEmbeddingInput is one note's id + embeddable text for a rebuild pass.
type NoteEmbeddingInput struct {
	ID   int64
	Text string
}

// ListNoteEmbeddingInputs returns id + text (title + content) for every note, in
// id order, for a full re-embed. Content is capped so a huge note does not blow
// the embedding request.
func ListNoteEmbeddingInputs(db *sql.DB) ([]NoteEmbeddingInput, error) {
	rows, err := db.Query("SELECT id, title, content FROM project_notes ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("db: list note inputs: %w", err)
	}
	defer rows.Close()
	var out []NoteEmbeddingInput
	for rows.Next() {
		var id int64
		var title, content string
		if err := rows.Scan(&id, &title, &content); err != nil {
			return nil, err
		}
		// Shared with the incremental drainer (NoteEmbeddingText) so a note's
		// vector means the same thing whichever path wrote it.
		out = append(out, NoteEmbeddingInput{ID: id, Text: NoteEmbeddingText(title, content)})
	}
	return out, rows.Err()
}

// NoteHitsByIDs loads note search-hits for the given ids (to materialise
// vector-recall ids the lexical FTS pass did not return). Order not guaranteed.
func NoteHitsByIDs(db *sql.DB, ids []int64, query string) ([]SearchHit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(
		"SELECT id, project_id, title, content FROM project_notes WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return nil, fmt.Errorf("db: note hits by ids: %w", err)
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		var content string
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.Title, &content); err != nil {
			return nil, err
		}
		h.Type = "note"
		h.Snippet = makeSnippet(content, query)
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// encodeVector serialises a float32 vector to the JSON-array text form sqlite-vec
// accepts for both insertion and MATCH queries (e.g. "[0.1,0.2]").
func encodeVector(vec []float32) (string, error) {
	if len(vec) == 0 {
		return "", fmt.Errorf("db: empty embedding vector")
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	// Sanity: ensure it is valid JSON (catches NaN/Inf, which sqlite-vec rejects).
	if !json.Valid([]byte(b.String())) {
		return "", fmt.Errorf("db: embedding vector is not finite")
	}
	return b.String(), nil
}

// PruneNoteEmbeddings removes every embedding whose note no longer exists.
// Notes deleted through the project cascade (CleanupStaleDataTx) have no
// single note id to delete, so callers with a derived index present run this
// after the deleting transaction commits. Returns how many orphans were
// removed.
func PruneNoteEmbeddings(db *sql.DB) (int64, error) {
	res, err := db.Exec("DELETE FROM " + vecTableName + " WHERE rowid NOT IN (SELECT id FROM project_notes)")
	if err != nil {
		return 0, fmt.Errorf("db: prune embeddings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}
