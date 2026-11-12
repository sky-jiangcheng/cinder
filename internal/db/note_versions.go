package db

import (
	"database/sql"
	"fmt"

	"repotory/internal/diff"
)

// ListNoteVersions returns the recent version history for a note, ordered by
// created_at descending. At most 50 versions are kept by the cleanup trigger.
func ListNoteVersions(db *sql.DB, noteID int64) ([]NoteVersion, error) {
	rows, err := db.Query(
		"SELECT id, note_id, version, title, content, tags, kind, created_at FROM note_versions WHERE note_id = ? ORDER BY created_at DESC, id DESC LIMIT 50",
		noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []NoteVersion
	for rows.Next() {
		var v NoteVersion
		if err := rows.Scan(&v.ID, &v.NoteID, &v.Version, &v.Title, &v.Content, &v.Tags, &v.Kind, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// GetNoteVersion returns a single version by ID.
func GetNoteVersion(db *sql.DB, versionID int64) (*NoteVersion, error) {
	v := &NoteVersion{}
	err := db.QueryRow(
		"SELECT id, note_id, version, title, content, tags, kind, created_at FROM note_versions WHERE id = ?", versionID).
		Scan(&v.ID, &v.NoteID, &v.Version, &v.Title, &v.Content, &v.Tags, &v.Kind, &v.CreatedAt)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// RestoreNoteVersion restores a note to the content of a previous version.
// It updates the note's content and metadata from the version snapshot.
//
// Since schema v31 the snapshot a restore reads holds the state the note had
// BEFORE an edit (ADR-0057), so "restore v3" means "put back what was there
// before the edit that produced version 4". Restoring is itself a write: the
// version counter moves FORWARD (n → n+1) rather than back to the snapshot's
// number. Rewinding the counter would make every client holding the newer
// version look up-to-date and turn the next write into a silent overwrite,
// which is the failure the counter exists to prevent.
func RestoreNoteVersion(db *sql.DB, noteID, versionID int64) error {
	v, err := GetNoteVersion(db, versionID)
	if err != nil {
		return err
	}
	if v.NoteID != noteID {
		return fmt.Errorf("version %d does not belong to note %d", versionID, noteID)
	}
	_, err = db.Exec(
		"UPDATE project_notes SET content = ?, title = ?, tags = ?, kind = ?, version = version + 1, updated_at = strftime('%Y-%m-%d %H:%M:%f','now') WHERE id = ?",
		v.Content, v.Title, v.Tags, v.Kind, noteID)
	return err
}

// DiffNoteVersions produces a line-based diff between a historical version
// and the current note content. Returns the unified diff string; if either
// side is not found the error is propagated.
func DiffNoteVersions(db *sql.DB, noteID int64, versionID int64) (string, error) {
	current, err := GetNoteByID(db, noteID)
	if err != nil {
		return "", err
	}
	v, err := GetNoteVersion(db, versionID)
	if err != nil {
		return "", err
	}
	if v.NoteID != noteID {
		return "", fmt.Errorf("version %d does not belong to note %d", versionID, noteID)
	}
	return diff.Lines(v.Content, current.Content), nil
}
