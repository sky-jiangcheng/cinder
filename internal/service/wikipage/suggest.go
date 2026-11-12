package wikipage

import (
	"database/sql"
	"strings"

	"repotory/internal/db"
)

// WikiLinkSuggestion represents a candidate entity/concept from the library that
// appears in the given text but is not yet formatted as a [[wikilink]].
type WikiLinkSuggestion struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	PageID  int64  `json:"page_id"`
	Matched string `json:"matched"` // the exact word/phrase matched in text
}

// SuggestWikiLinks scans content for mentions of existing approved wiki page titles
// or slugs, returning candidate concepts that could be turned into [[wikilinks]].
// It excludes concepts that are already linked in the content (e.g. [[slug]] or [[slug|title]]).
// Only concepts with titles >= minLen runes are suggested to avoid noise.
func SuggestWikiLinks(database *sql.DB, projectID int64, content string) []WikiLinkSuggestion {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	pages, err := db.ListWikiPagesByStatus(database, db.WikiStatusApproved, projectID)
	if err != nil || len(pages) == 0 {
		return nil
	}

	// 1. Gather already linked tokens to avoid redundant suggestions
	alreadyLinked := make(map[string]bool)
	for _, m := range WikiLinkRe.FindAllStringSubmatch(content, -1) {
		token := strings.TrimSpace(m[1])
		if token != "" {
			alreadyLinked[strings.ToLower(token)] = true
		}
	}

	contentLower := strings.ToLower(content)
	const minRuneLen = 3

	var suggestions []WikiLinkSuggestion
	seenPages := make(map[int64]bool)

	for _, p := range pages {
		if seenPages[p.ID] {
			continue
		}

		// Check by Title
		title := strings.TrimSpace(p.Title)
		titleLower := strings.ToLower(title)
		if len([]rune(title)) >= minRuneLen && !alreadyLinked[titleLower] && !alreadyLinked[strings.ToLower(p.Slug)] {
			if strings.Contains(contentLower, titleLower) {
				suggestions = append(suggestions, WikiLinkSuggestion{
					Slug:    p.Slug,
					Title:   p.Title,
					Kind:    p.Kind,
					PageID:  p.ID,
					Matched: title,
				})
				seenPages[p.ID] = true
				continue
			}
		}

		// Check by Slug (e.g. if slug is a recognized identifier with >= minRuneLen)
		slug := strings.TrimSpace(p.Slug)
		slugLower := strings.ToLower(slug)
		if len([]rune(slug)) >= minRuneLen && !alreadyLinked[slugLower] && !alreadyLinked[titleLower] {
			if strings.Contains(contentLower, slugLower) {
				suggestions = append(suggestions, WikiLinkSuggestion{
					Slug:    p.Slug,
					Title:   p.Title,
					Kind:    p.Kind,
					PageID:  p.ID,
					Matched: slug,
				})
				seenPages[p.ID] = true
			}
		}
	}

	return suggestions
}
