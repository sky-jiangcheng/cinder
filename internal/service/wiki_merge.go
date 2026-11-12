package service

import (
	"context"
	"fmt"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
)

// Concept merge (ADR-0019). Both halves moved to internal/service/wikigen
// (ADR-0060 step three): structural candidate discovery, which is free and
// model-less but is called by the model half and so travels with it, and the
// model-backed suggestion pass. What stays here is the facade — the review page
// and lint call the same methods as before — and the one human-only path,
// MergeWikiPagesInto, which is a click a person makes, not a model call.

// MergeCandidate and MergeCandidateReport are aliases, not copies: their json
// tags cross the RPC bridge to the review page, so an alias keeps every caller
// referring to the same type.
type (
	MergeCandidate       = wikigen.MergeCandidate
	MergeCandidateReport = wikigen.MergeCandidateReport
)

// Compat aliases for the vocabulary the write side moved to wikigen: the
// config key, the surface's top-K and the two scoring bounds, still named by
// this package's tests. Const aliases cannot drift from the definitions.
const (
	wikiMergeSuggestKey = wikigen.KeyMergeSuggest
	mergeDefaultTopK    = wikigen.MergeDefaultTopK
	mergeMinPairScore   = wikigen.MergeMinPairScore
	mergeMaxBucket      = wikigen.MergeMaxBucket
)

// FindMergeCandidates runs the four structural blocking signals over the page
// layer and returns the top-K pairs worth a human's attention. No model, no
// vector, no writes — which is why it can be called freely, including from the
// review page on every load.
func (s *Service) FindMergeCandidates(projectID int64, topK int) (*MergeCandidateReport, error) {
	return wikigen.FindMergeCandidates(s.db, projectID, topK)
}

// suggestMerges asks the model about the structural candidates and turns only
// the confirmed ones into findings. RunWikiLint calls it as one of its two model
// halves; the kernel and its budgets live in wikigen.
func (s *Service) suggestMerges(pages []db.WikiPage) ([]WikiFinding, string) {
	return wikigen.SuggestMerges(context.Background(), s.db, wikigenChat{s}, pages)
}

// MergeWikiPagesInto is the ONLY path to the merge transaction, reachable only from
// a binding the review page invokes on a click (ADR-0019 决策 1). The tombstone copy
// lives here so the wiki's user-facing wording stays in one place and the db layer
// keeps taking a string rather than owning a language.
func (s *Service) MergeWikiPagesInto(loserID, winnerID int64) (*db.MergeResult, error) {
	winner, err := db.GetWikiPageByID(s.db, winnerID)
	if err != nil {
		return nil, fmt.Errorf("merge: winner page: %w", err)
	}
	return db.MergeWikiPages(s.db, loserID, winnerID,
		fmt.Sprintf("已并入 [[%s]]（人工合并：原页面作为墓碑保留，以维持入链与审计记录）。", winner.Slug))
}
