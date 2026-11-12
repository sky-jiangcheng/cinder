package service

import (
	"context"

	"repotory/internal/service/wikigen"
)

// Community synthesis (ADR-0020). The kernel — structural detection, the
// consent gate placed after the free half, and the per-topic model call — moved
// to internal/service/wikigen (ADR-0060 step three). What stays here is the
// facade the review page calls, plus the const/wrapper aliases whose names the
// thresholds and slug tests still use.

// WikiCommunityReport is an alias, not a copy: its json tags cross the RPC
// bridge to the review page.
type WikiCommunityReport = wikigen.WikiCommunityReport

// Compat aliases for the vocabulary the write side moved to wikigen: the
// consent key, the source tag and the two threshold keys, still named by this
// package's tests. Const aliases cannot drift from the definitions.
const (
	wikiCommunityKey    = wikigen.KeyCommunity
	communitySourceTag  = wikigen.CommunitySourceTag
	communityMinSizeKey = wikigen.KeyCommunityMinSize
	communityMaxSizeKey = wikigen.KeyCommunityMaxSize
)

// RunCommunitySummary detects topics among projectID's APPROVED pages and files one
// pending synthesis page per topic.
//
// projectID 0 means the GLOBAL tier (pages belonging to no project), NOT "every
// project": a community spanning two projects would summarise something neither
// user asked about, and "all projects" has no representation in the schema because
// project_id is a real foreign key. The review surface therefore requires an
// explicit project to run against — the same rule StartCompile enforces.
func (s *Service) RunCommunitySummary(projectID int64) (*WikiCommunityReport, error) {
	return wikigen.RunCommunitySummary(context.Background(), s.db, wikigenChat{s}, projectID)
}

// communitySizeThresholds delegates to the kernel's reader. Kept as a method
// because the threshold test drives it through the service, and because the
// overrides are a property of the community feature, not of one function.
func (s *Service) communitySizeThresholds() (int, int) {
	return wikigen.CommunitySizeThresholds(s.db)
}

// CommunitySynthesisSlug is 决策 7's stable identity: `synthesis-` plus the first
// eight hex characters of sha256 over the sorted member slug list. Same community,
// same slug forever — which is the only reason a re-run updates a page instead of
// filing a second one.
func CommunitySynthesisSlug(members []string) string {
	return wikigen.CommunitySynthesisSlug(members)
}

// CommunitySynthesisTitle names the page for a human, leading with the alphabetically
// first member so the title cannot drift between runs either.
func CommunitySynthesisTitle(members []string) string {
	return wikigen.CommunitySynthesisTitle(members)
}
