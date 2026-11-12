package service

import (
	"repotory/internal/db"
	"repotory/internal/service/wikipage"
)

// The read side of the knowledge layer — browse, search, page detail, version
// history, link suggestions, scope and Markdown export — lives in
// internal/service/wikipage as of ADR-0060 step two.
//
// Why this half first: these files touch nothing but the database. They never
// call a model, never start a goroutine and never ask another domain for an
// answer, so they could be moved without inventing an interface. The compile,
// merge, lint, community and evidence halves all do call a model, so each of
// them will need its own seam — that is a separate change.
//
// The types are aliases, not copies: the Wails binding, the RPC bridge and the
// MCP `repotory_wiki_*` tools keep seeing one shape, including the json tags.

// WikiPageSummary is one row of the browse feed.
type WikiPageSummary = wikipage.WikiPageSummary

// WikiPageDetail is one approved page in full, plus the graph around it.
type WikiPageDetail = wikipage.WikiPageDetail

// WikiPendingPage is one row of the review feed.
type WikiPendingPage = wikipage.WikiPendingPage

// WikiLinkSuggestion is one "this text looks like it should link there" row.
type WikiLinkSuggestion = wikipage.WikiLinkSuggestion

// WikiExportReport counts what an export wrote.
type WikiExportReport = wikipage.WikiExportReport

// ErrWikiExportNotEmpty is the sentinel a non-forced export returns when the
// target already holds files. It is the same value the sub-package returns, so
// errors.Is keeps working for callers that hold either name.
var ErrWikiExportNotEmpty = wikipage.ErrWikiExportNotEmpty

// BrowseWikiPages returns the approved pages a reader may see, newest first.
func (s *Service) BrowseWikiPages(projectID int64, limit int) ([]WikiPageSummary, error) {
	return wikipage.BrowseWikiPages(s.db, projectID, limit)
}

// SearchWikiPages searches the approved pages a reader may see, with the same
// retrieval and scope the evidence path uses.
func (s *Service) SearchWikiPages(projectID int64, query string, limit int) ([]WikiPageSummary, error) {
	return wikipage.SearchWikiPages(s.db, projectID, query, limit)
}

// GetWikiPage returns one approved page with its body, its edges and the notes
// it was compiled from. Anything not approved is refused.
func (s *Service) GetWikiPage(pageID int64) (*WikiPageDetail, error) {
	return wikipage.GetWikiPage(s.db, pageID)
}

// ListWikiPageVersions returns a page's snapshots, newest first.
func (s *Service) ListWikiPageVersions(pageID int64) ([]db.PageVersion, error) {
	return wikipage.ListWikiPageVersions(s.db, pageID)
}

// DiffWikiPageVersion compares one snapshot against the page's live content.
func (s *Service) DiffWikiPageVersion(pageID, versionID int64) (string, error) {
	return wikipage.DiffWikiPageVersion(s.db, pageID, versionID)
}

// SuggestWikiLinks proposes links for content that is not a page yet.
func (s *Service) SuggestWikiLinks(projectID int64, content string) []WikiLinkSuggestion {
	return wikipage.SuggestWikiLinks(s.db, projectID, content)
}

// ListApprovedWikiPages is the scope surface's feed.
func (s *Service) ListApprovedWikiPages(projectID int64) ([]WikiPendingPage, error) {
	return wikipage.ListApprovedWikiPages(s.db, projectID)
}

// PromoteWikiPageToGlobal moves an approved page into the global tier. It is
// the only path there, and nothing automated may call it (ADR-0015).
func (s *Service) PromoteWikiPageToGlobal(pageID int64) error {
	return wikipage.PromoteWikiPageToGlobal(s.db, pageID)
}

// AssignWikiPageProject moves a page back into a project tier.
func (s *Service) AssignWikiPageProject(pageID int64, projectID int64) error {
	return wikipage.AssignWikiPageProject(s.db, pageID, projectID)
}

// ExportWikiTree writes the approved pages as a Markdown tree on disk.
func (s *Service) ExportWikiTree(rootDir string, projectID int64, force bool) (*WikiExportReport, error) {
	return wikipage.ExportWikiTree(s.db, rootDir, projectID, force)
}
