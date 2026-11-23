package service

import (
	"context"
	"log"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/search/temporal"
)

// ADR-0070: the temporal recall arm. A date expression in the query is a
// structural signal neither trigrams nor embeddings carry, so it gets its own
// arm fused by the same RRF as the vector arm. Unlike semantic search it is
// always on: pure local computation, no endpoint, no data leaving the machine,
// and a query without a date expression is byte-identical to before.

const temporalRecallK = 20 // window ids fused with the existing hits

// fuseTemporal merges notes created inside the query's date window into the
// existing hits. Any failure (no date expression, window query error, empty
// window) returns the input unchanged — it can never reduce results.
func (s *Service) fuseTemporal(ctx context.Context, base []domain.SearchHit, query string) []domain.SearchHit {
	win, ok := temporal.Parse(query, temporal.Anchor(ctx))
	if !ok {
		return base
	}
	ids, err := db.NoteIDsInWindow(ctx, s.db, query, win.From, win.To, temporalRecallK)
	if err != nil {
		log.Printf("temporal recall failed; using existing results: %v", err)
		return base
	}
	if len(ids) == 0 {
		return base
	}
	return s.fuseRankList(base, ids, query, 1.0, "temporal")
}
