package service

import (
	"context"
	"fmt"
	"time"

	"repotory/internal/review"
	"repotory/internal/service/reviewrun"
)

// The model pass, from the service's side. Everything about which candidates
// become verdicts — chunk size, index bounds, "a reply may not invent a
// finding" — lives in reviewrun/refine.go. What lives here is the part the
// review package must not know: how to reach a model, and where the call is
// audited (ADR-0030).

// reviewAITimeout is per call. Review calls are small and structured; a slow
// answer is worth abandoning rather than holding the run open.
const reviewAITimeout = 90 * time.Second

// reviewRefiner is the bridge reviewrun.Refiner expects: one chunk in, verdicts
// out. Its failures carry through unchanged so the run can count them, except
// "no endpoint configured" — that is tagged ErrUnavailable, which makes the
// pipeline report a rule-only run instead of N failed calls.
type reviewRefiner struct{ s *Service }

func (r reviewRefiner) Verdicts(ctx context.Context, path, content string, chunk []review.Finding) ([]reviewrun.Verdict, error) {
	cfg, err := r.s.aiChatConfig()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", reviewrun.ErrUnavailable, err)
	}
	system, user := reviewrun.Prompt(path, content, chunk)
	res, err := r.s.chatWithAudit(cfg, []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, reviewAITimeout)
	if err != nil {
		return nil, err
	}
	return reviewrun.ParseVerdicts(res.Reply), nil
}
