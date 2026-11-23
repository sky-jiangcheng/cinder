package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// NoteIDsInWindow recalls note IDs created within [from, to) — the temporal
// arm of ADR-0070. Two stages: FTS-ranked inside the window when the query
// has matchable terms, then every note in the window by recency. The second
// stage is the point: a date question can share zero trigrams with the note
// it targets ("11月17号那天在解决什么问题" vs a handoff titled "Session Handoff
// 2026-11-17 16:46"), so content ranking alone would return nothing.
//
// from/to are UTC instants; created_at is SQLite CURRENT_TIMESTAMP (UTC) in
// "2006-01-02 15:04:05" text form, so the comparison is a plain string range.
func NoteIDsInWindow(ctx context.Context, database *sql.DB, query string, from, to time.Time, limit int) ([]int64, error) {
	fromStr := from.UTC().Format("2006-01-02 15:04:05")
	toStr := to.UTC().Format("2006-01-02 15:04:05")
	if ftsUsable(query) {
		for _, expr := range []string{escapeFTS(query), escapeFTSOR(query)} {
			if expr == "" {
				continue
			}
			ids, err := noteIDsFTSInWindow(ctx, database, expr, fromStr, toStr, limit)
			if err == nil && len(ids) > 0 {
				return ids, nil
			}
		}
	}
	rows, err := database.QueryContext(ctx,
		"SELECT id FROM project_notes WHERE created_at >= ? AND created_at < ? ORDER BY updated_at DESC LIMIT ?",
		fromStr, toStr, limit)
	if err != nil {
		return nil, fmt.Errorf("db: note ids in window: %w", err)
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

func noteIDsFTSInWindow(ctx context.Context, database *sql.DB, expr, from, to string, limit int) ([]int64, error) {
	rows, err := database.QueryContext(ctx,
		"SELECT n.id FROM project_notes_fts f JOIN project_notes n ON n.id = f.rowid "+
			"WHERE project_notes_fts MATCH ? AND n.created_at >= ? AND n.created_at < ? "+
			"ORDER BY bm25(project_notes_fts), n.updated_at DESC LIMIT ?",
		expr, from, to, limit)
	if err != nil {
		return nil, err
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
