package wikipage

import (
	"database/sql"

	"repotory/internal/db"
)

// The review feed projection. It is here, on the read side, because three
// surfaces consume it — the review queue, the scope page and the compile job's
// "what did I just produce" — and only one of them writes anything. Keeping it
// with the compiler would have made the read side depend on the write side for
// a struct shape.
//
// Pending, rejected and approved rows must look identical in the UI, so there
// is exactly one projection: if "approve" and "promote" rendered different row
// shapes, the same page would appear to change when nothing about it did.

// WikiPendingPage is one row of the review feed: the page plus what it is
// connected to.
type WikiPendingPage struct {
	WikiPage *db.WikiPage  `json:"page"`
	Sources  []int64       `json:"source_note_ids"`
	OutLinks []db.PageEdge `json:"out_links"`
	InBacks  []db.PageEdge `json:"in_links"`
}

// ReviewFeed returns the pages in one status with their provenance and edges.
//
// A project id narrows to that project's rows; 0 means every page, global ones
// included. The review queue passes its project through unchanged, which keeps
// its "project_id = ? exactly" semantics (ADR-0031 决策 5) — the browse path is
// the one that also shows global pages, and it uses a different query.
func ReviewFeed(database *sql.DB, status string, projectID int64) ([]WikiPendingPage, error) {
	pages, err := db.ListWikiPagesByStatus(database, status, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]WikiPendingPage, 0, len(pages))
	for i := range pages {
		p := pages[i]
		item := WikiPendingPage{WikiPage: &p}
		if ids, err := db.NotesForPage(database, p.ID); err == nil {
			item.Sources = ids
		}
		if e, err := db.WikiEdgesFrom(database, p.ID); err == nil {
			item.OutLinks = e
		}
		if e, err := db.WikiEdgesTo(database, p.ID); err == nil {
			item.InBacks = e
		}
		out = append(out, item)
	}
	return out, nil
}
