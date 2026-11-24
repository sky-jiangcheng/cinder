package service

import (
	"log"
	"strings"

	"repotory/internal/db"
)

// HandoffTimelineEntry is one card of the handoff timeline: the reader view
// over every handoff note in the knowledge base, newest first.
//
// It is deliberately NOT the memory ring's HandoffEntry. The ring (D25) is an
// entry point and stays content-free by design; the timeline is a READER —
// the user came here because handoff notes piled up across projects and
// projects' note tabs stopped answering "what happened, when". So the card
// carries an excerpt of the body, and clicking one lands on the full note.
type HandoffTimelineEntry struct {
	NoteID       int64  `json:"note_id"`
	ProjectID    int64  `json:"project_id"`
	ProjectName  string `json:"project_name"`
	Title        string `json:"title"`
	Excerpt      string `json:"excerpt"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	AutoCaptured bool   `json:"auto_captured"`
}

// The timeline is a scrolling view, not a query result: the frontend asks for
// progressively larger windows ("load more"), so the cap exists to keep one
// careless limit=999999 from dragging every handoff body out of SQLite in a
// single call. 200 covers months of daily captures.
const maxHandoffTimelineLimit = 200

// excerptRunes is how much of a handoff body the timeline preview shows. Long
// enough to recognise a session by its first line ("## Summary" plus the first
// sentence), short enough that a hundred cards render without scrolling a
// novel.
const excerptRunes = 180

// excerpt collapses a note body into a single preview line. Markdown headers
// and blank lines are structural, not content — the preview is about what the
// handoff SAYS, so runs of whitespace fold into one space and the result is
// clamped on a rune boundary.
func excerpt(content string) string {
	fields := strings.Fields(content)
	line := strings.Join(fields, " ")
	runes := []rune(line)
	if len(runes) <= excerptRunes {
		return line
	}
	return string(runes[:excerptRunes]) + "…"
}

// ListHandoffTimeline returns handoff notes across ALL projects, newest first,
// as the timeline's cards. limit<=0 takes the default; larger asks are clamped
// to the timeline cap. Like the memory ring it degrades on a database error —
// a log line and an empty timeline instead of failing the page the timeline
// sits inside.
func (s *Service) ListHandoffTimeline(limit int) []HandoffTimelineEntry {
	if limit <= 0 {
		limit = defaultRecentHandoffLimit
	}
	if limit > maxHandoffTimelineLimit {
		limit = maxHandoffTimelineLimit
	}
	notes, err := db.ListRecentHandoffNotes(s.db, 0, limit)
	if err != nil {
		log.Printf("ListHandoffTimeline: %v", err)
		return nil
	}
	out := make([]HandoffTimelineEntry, 0, len(notes))
	for _, n := range notes {
		out = append(out, HandoffTimelineEntry{
			NoteID:       n.ID,
			ProjectID:    n.ProjectID,
			ProjectName:  n.ProjectName,
			Title:        n.Title,
			Excerpt:      excerpt(n.Content),
			CreatedAt:    n.CreatedAt,
			UpdatedAt:    n.UpdatedAt,
			AutoCaptured: strings.Contains(strings.ReplaceAll(strings.ToLower(n.Tags), " ", ""), "auto-captured"),
		})
	}
	return out
}
