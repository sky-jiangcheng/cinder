package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	pluginruntime "repotory/internal/core/plugin/runtime"
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
func (s *Service) SearchNotes(ctx context.Context, query string) ([]domain.SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return []domain.SearchHit{}, nil
	}
	results, err := db.SearchNotes(ctx, s.db, query)
	if err != nil {
		return nil, fmt.Errorf("search notes: %w", err)
	}
	if results == nil {
		results = []domain.SearchHit{}
	}
	markLexicalRecall(results)
	// M3-A: optionally fuse vector recall (default off; a no-op bypass when off).
	// ADR-0070: the temporal arm applies regardless of the semantic switch.
	fused := s.fuseTemporal(ctx, s.fuseSemantic(results, query), query)
	s.annotateSourceTypes(fused)
	return fused, nil
}

// SearchNotesStrict is SearchNotes without the lexical OR relaxation, for
// callers whose output is a CITATION rather than a ranked hint — the AI
// evidence channel is the only one today (see wiki_evidence.go's call site).
//
// The semantic fusion still applies: a vector hit is a genuine near-match in
// embedding space, not a shared trigram, so it does not carry the false-positive
// risk the relaxation does. The temporal arm (ADR-0070) deliberately does NOT
// apply here: a date window pulls in notes by creation time, not by content
// match, which is exactly the coincidence-as-citation risk this path avoids.
func (s *Service) SearchNotesStrict(ctx context.Context, query string) ([]domain.SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return []domain.SearchHit{}, nil
	}
	results, err := db.SearchNotesStrict(ctx, s.db, query)
	if err != nil {
		return nil, fmt.Errorf("search notes: %w", err)
	}
	if results == nil {
		results = []domain.SearchHit{}
	}
	markLexicalRecall(results)
	fused := s.fuseSemantic(results, query)
	s.annotateSourceTypes(fused)
	return fused, nil
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
func (s *Service) SearchAll(ctx context.Context, query string) ([]domain.SearchHit, error) {
	notes, todos, err := s.searchLexical(ctx, query)
	if err != nil {
		return nil, err
	}
	return s.fuseHits(ctx, query, notes, todos), nil
}

// searchLexical is stage one: the local FTS/LIKE pass plus provenance stamps.
//
// It is split out from SearchAll because the two stages have wildly different
// costs. Measured on a real library (162 notes): this stage is ~3ms, while the
// semantic arm's query embedding is a NETWORK round trip — 4–12s against a
// remote embedding endpoint. Before the split every keystroke-triggered search
// paid that round trip before showing anything at all (ADR-0077).
//
// The note/todo split lives here rather than in the caller: note IDs come from
// project_notes and todo IDs from project_todos, both AUTOINCREMENT — id 1
// exists in both tables and means different rows, so the two ID spaces must
// never be fused together.
func (s *Service) searchLexical(ctx context.Context, query string) (notes, todos []domain.SearchHit, err error) {
	if strings.TrimSpace(query) == "" {
		return []domain.SearchHit{}, []domain.SearchHit{}, nil
	}
	results, err := db.SearchAll(ctx, s.db, query)
	if err != nil {
		return nil, nil, fmt.Errorf("search all: %w", err)
	}
	markLexicalRecall(results)
	notes = make([]domain.SearchHit, 0, len(results))
	todos = make([]domain.SearchHit, 0, len(results))
	for _, h := range results {
		if h.Type == "todo" {
			todos = append(todos, h)
			continue
		}
		notes = append(notes, h)
	}
	// Only note hits carry a source type; annotating the note slice is the
	// whole job, and it mutates in place.
	s.annotateSourceTypes(notes)
	return notes, todos, nil
}

// fuseHits is stage two: the temporal and semantic recall arms over the note
// subset, with the todos appended unchanged.
//
// Notes first (fused), then todos — the same shape db.SearchAll returns, so a
// lexical-only run keeps byte-identical ordering. Both arms degrade to a no-op
// on their own (semantic off, no embedder, unreachable endpoint, no vector
// index), which is why this never returns an error: the worst case is stage
// one's answer.
func (s *Service) fuseHits(ctx context.Context, query string, notes, todos []domain.SearchHit) []domain.SearchHit {
	fused := s.fuseTemporal(ctx, s.fuseSemantic(notes, query), query)
	combined := append(fused, todos...)
	s.annotateSourceTypes(combined)
	return combined
}

// markLexicalRecall stamps the base (FTS/LIKE) channel on every hit before
// fusion. The fusion arms append their own channels, so a hit's Recall ends up
// telling exactly which arms surfaced it (ADR-0072 search transparency).
func markLexicalRecall(hits []domain.SearchHit) {
	for i := range hits {
		hits[i].Recall = []string{"lexical"}
	}
}

// appendRecallChannel adds a fusion channel to a hit's provenance, deduped,
// preserving stage order (lexical first, then semantic, then temporal).
func appendRecallChannel(h domain.SearchHit, channel string) domain.SearchHit {
	for _, c := range h.Recall {
		if c == channel {
			return h
		}
	}
	h.Recall = append(h.Recall, channel)
	return h
}

// annotateSourceTypes fills the derived source bucket on note hits so the
// search surfaces can filter by provenance (ADR-0022 pending item 2). It is
// presentation-only: a lookup failure leaves the field empty and never fails
// the search.
func (s *Service) annotateSourceTypes(hits []domain.SearchHit) {
	ids := make([]int64, 0, len(hits))
	for _, h := range hits {
		if h.Type == "note" {
			ids = append(ids, h.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	sources, err := db.NoteSourcesByIDs(s.db, ids)
	if err != nil {
		log.Printf("search: source-type annotation failed: %v", err)
		return
	}
	for i := range hits {
		if hits[i].Type != "note" {
			continue
		}
		if src, ok := sources[hits[i].ID]; ok {
			hits[i].SourceType = string(pluginruntime.SourceTypeFor(src))
		}
	}
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
