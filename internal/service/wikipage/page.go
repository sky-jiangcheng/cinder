package wikipage

import (
	"database/sql"
	"fmt"
	"strings"

	"repotory/internal/db"
)

// Reading the approved knowledge layer (ADR-0032).
//
// Why this file exists. ADR-0015's gate is on the WRITE side: compiled pages
// land pending, a human approves them, and only then may they be retrieved.
// The gate was right, but it defined "published" as "may be cited" and never as
// "may be read" — so the 27 approved pages of a real library had exactly one
// consumer, the AI evidence block, and no human reader anywhere. The web app
// rendered page bodies in two places and both were on the review page.
//
// This file is the other half of that promise: the read side a person uses.
//
// What it deliberately does NOT do:
//
//   - It does not edit. Changing an approved page is ADR-0021's revision flow
//     (snapshots, who changed what), and this ADR stays out of it.
//   - It does not touch access_count. That column means "how often a page was
//     surfaced into an ANSWER's evidence" (schema v26); a human opening a page
//     is a different visit, and mixing the two would make one column carry two
//     meanings that a future ranking policy would read as one.
//   - It does not show pending or rejected pages. Those live on the review
//     page, which is where the gate is; showing them here too would blur
//     "to be judged" and "published".
//
// Scope semantics are the evidence path's, not the review path's: a project
// scope means "that project's pages PLUS the global ones" (ADR-0031 事实三), so
// a global page is visible from every project while another project's page is
// not. The review queue filters project_id = ? exactly and stays that way —
// the two coexist on purpose (ADR-0031 决策 5).

// WikiPageSummary is one row of the browse feed: enough to render a card,
// without shipping every page body to a list that may hold a hundred of them.
type WikiPageSummary struct {
	ID    int64  `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	// ProjectID is 0 for a global page (ADR-0031): the page belongs to no
	// project, which is a fact the reader must be able to SEE, not infer.
	ProjectID int64  `json:"project_id"`
	UpdatedAt string `json:"updated_at"`
	Snippet   string `json:"snippet"`
}

// WikiPageDetail is one approved page in full, plus the graph around it.
type WikiPageDetail struct {
	WikiPageSummary
	Content string `json:"content"`
	// Source is the provenance of the write (manual | ai | wiki-compile | …),
	// Status is always "approved" — the detail endpoint refuses anything else.
	Status   string        `json:"status"`
	Source   string        `json:"source"`
	OutLinks []db.PageEdge `json:"out_links"`
	InLinks  []db.PageEdge `json:"in_links"`
	// SourceNoteIDs are the notes the page was compiled from.
	SourceNoteIDs []int64 `json:"source_note_ids"`
}

// BrowseWikiPages returns the approved pages a reader may see, newest first.
// An empty query is not involved: the browse feed has no query, so it uses the
// dedicated list query rather than the search path (which treats an empty
// query as "no results" — see db.SearchWikiPages).
func BrowseWikiPages(database *sql.DB, projectID int64, limit int) ([]WikiPageSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	pages, err := db.ListVisibleApprovedPages(database, projectID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]WikiPageSummary, 0, len(pages))
	for _, p := range pages {
		out = append(out, pageSummary(p, ""))
	}
	return out, nil
}

// SearchWikiPages searches the approved pages a reader may see.
//
// It is the same retrieval the evidence path uses (approved-only, bm25, CJK
// relaxation) with the same project scope, so "what the answer cited" and
// "what the reader found" cannot disagree about which pages exist.
func SearchWikiPages(database *sql.DB, projectID int64, query string, limit int) ([]WikiPageSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	pages, err := db.SearchWikiPages(database, query, projectID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]WikiPageSummary, 0, len(pages))
	for _, p := range pages {
		out = append(out, pageSummary(p, query))
	}
	return out, nil
}

// GetWikiPage returns one approved page with its body, its edges and the notes
// it was compiled from.
//
// It refuses a page that is not approved: the approval gate decides what may
// be READ as much as what may be cited, and a detail endpoint that served a
// pending page would turn the browse surface into a second review surface.
func GetWikiPage(database *sql.DB, pageID int64) (*WikiPageDetail, error) {
	p, err := db.GetWikiPageByID(database, pageID)
	if err != nil {
		return nil, fmt.Errorf("page %d not found: %w", pageID, err)
	}
	if p.Status != db.WikiStatusApproved {
		return nil, fmt.Errorf("page %d is %q: only approved pages are readable here", pageID, p.Status)
	}
	out, err := db.WikiEdgesFrom(database, pageID)
	if err != nil {
		return nil, err
	}
	in, err := db.WikiEdgesTo(database, pageID)
	if err != nil {
		return nil, err
	}
	notes, err := db.NotesForPage(database, pageID)
	if err != nil {
		return nil, err
	}
	detail := &WikiPageDetail{
		WikiPageSummary: pageSummary(*p, ""),
		Content:         p.Content,
		Status:          p.Status,
		Source:          p.Source,
		OutLinks:        out,
		InLinks:         in,
		SourceNoteIDs:   notes,
	}
	if detail.OutLinks == nil {
		detail.OutLinks = []db.PageEdge{}
	}
	if detail.InLinks == nil {
		detail.InLinks = []db.PageEdge{}
	}
	if detail.SourceNoteIDs == nil {
		detail.SourceNoteIDs = []int64{}
	}
	return detail, nil
}

// pageSummary projects a page onto the feed shape, with a plain-text snippet.
//
// The snippet is deliberately NOT the notes path's HTML snippet: that one is
// built for the web list (it carries <mark> and is sanitized at render time),
// while this one is also the payload of an MCP tool and of two IDE plugins,
// where markup is noise and plain text is safer by construction.
func pageSummary(p db.WikiPage, query string) WikiPageSummary {
	return WikiPageSummary{
		ID:        p.ID,
		Slug:      p.Slug,
		Title:     p.Title,
		Kind:      p.Kind,
		ProjectID: p.ProjectID,
		UpdatedAt: p.UpdatedAt,
		Snippet:   pageSnippet(p.Content, query),
	}
}

// pageSnippet windows the content around the first match of the query.
//
// Rune-based, not byte-based: a CJK page cut mid-character renders as a
// replacement glyph, which looks like corrupted knowledge.
func pageSnippet(content, query string) string {
	const window = 120
	runes := []rune(content)
	if len(runes) == 0 {
		return ""
	}
	start := 0
	if q := strings.TrimSpace(query); q != "" {
		if idx := strings.Index(strings.ToLower(content), strings.ToLower(q)); idx >= 0 {
			// The index is a BYTE offset; convert it to a rune offset.
			pos := len([]rune(content[:idx]))
			start = pos - window/2
			if start < 0 {
				start = 0
			}
		}
	}
	end := start + window
	if end > len(runes) {
		end = len(runes)
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out = out + "…"
	}
	return out
}
