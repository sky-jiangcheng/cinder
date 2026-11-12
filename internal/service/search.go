package service

import (
	"fmt"
	"strings"

	"repotory/internal/db"
	"repotory/internal/domain"
)

// SearchNotes searches note content/title/tags across all projects,
// returning ranked hits with context snippets.
//
// ADR-0055 D2: a storage fault is an error, never a nil slice. Returning nil
// used to serialize as "null" (MCP) and {"hits": null} (HTTP), which clients
// read as "nothing matched" — a failure must not masquerade as zero hits.
// An empty query is a legitimate zero-hit answer, so it returns an explicit
// empty slice with a nil error.
func (s *Service) SearchNotes(query string) ([]domain.SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return []domain.SearchHit{}, nil
	}
	results, err := db.SearchNotes(s.db, query)
	if err != nil {
		return nil, fmt.Errorf("search notes: %w", err)
	}
	if results == nil {
		results = []domain.SearchHit{}
	}
	// M3-A: optionally fuse vector recall (default off; a no-op bypass when off).
	return s.fuseSemantic(results, query), nil
}

// SearchAll searches notes and todos together, returning ranked unified hits.
//
// It applies the same optional vector recall as SearchNotes — this is the path
// the product actually uses (all five frontend search entry points, the MCP
// repotory_notes_search / repotory_ask tools, and the HTTP /api/search?all=1
// endpoint), so leaving the fusion out here meant the ADR-0012 / ADR-0018
// evaluation gains never reached a single real query.
//
// The fusion runs on the NOTES SUBSET ONLY, and that is not a detail: note
// IDs come from project_notes and todo IDs from project_todos, both
// AUTOINCREMENT — id 1 exists in both tables and means different rows. The
// fusion's byID map is keyed by bare int64, so fusing the mixed list would
// let a todo and a note collide and silently overwrite each other. Splitting
// first keeps the two ID spaces apart; the vector index only ever holds note
// embeddings, so notes are the only subset it can extend.
func (s *Service) SearchAll(query string) ([]domain.SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return []domain.SearchHit{}, nil
	}
	results, err := db.SearchAll(s.db, query)
	if err != nil {
		return nil, fmt.Errorf("search all: %w", err)
	}
	if results == nil {
		results = []domain.SearchHit{}
	}
	notes := make([]domain.SearchHit, 0, len(results))
	todos := make([]domain.SearchHit, 0, len(results))
	for _, h := range results {
		if h.Type == "todo" {
			todos = append(todos, h)
			continue
		}
		notes = append(notes, h)
	}
	// Notes first (fused), then todos — the same shape db.SearchAll returns,
	// so a lexical-only run keeps byte-identical ordering.
	fused := s.fuseSemantic(notes, query)
	return append(fused, todos...), nil
}

// FormatSearchAnswer renders up to limit search hits in the shared
// "[type] title\nsnippet" text format used by the CLI ask command and the MCP
// ask tool.
func FormatSearchAnswer(hits []domain.SearchHit, limit int) []string {
	parts := make([]string, 0, limit)
	for i, h := range hits {
		if i >= limit {
			break
		}
		parts = append(parts, fmt.Sprintf("[%s] %s\n%s", h.Type, h.Title, h.Snippet))
	}
	return parts
}
