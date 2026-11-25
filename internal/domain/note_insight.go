package domain

// NoteRef identifies one note on a cross-reference surface — a related-note
// chip, a duplicate pair. It carries only what a link needs, so the payload of
// the insight grid stays small even when every card carries three references.
type NoteRef struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	ProjectName string `json:"project_name,omitempty"`
	Title       string `json:"title"`
}

// RelatedNote is another note judged relevant to one note.
//
// The reason is deliberately STRUCTURED rather than a sentence: the same
// insight is rendered in the user's UI language, so the server ships the
// evidence (which terms overlapped, whether they share a topic or project) and
// lets the client phrase it. A server-side English sentence would leak into the
// Chinese UI, and a server-side Chinese one into the English UI.
type RelatedNote struct {
	NoteRef
	Score       float64  `json:"score"`
	Shared      []string `json:"shared,omitempty"`
	SameTopic   bool     `json:"same_topic,omitempty"`
	SameProject bool     `json:"same_project,omitempty"`
}

// NoteInsight is the AI layer of one note: its cached summary, the keywords it
// was summarized with, and the notes it is related to. Summary is empty when no
// summary has been generated yet — a card then shows its own content, which is
// the pre-AI behaviour, not an error state.
type NoteInsight struct {
	NoteID   int64         `json:"note_id"`
	Summary  string        `json:"summary,omitempty"`
	Keywords []string      `json:"keywords,omitempty"`
	Related  []RelatedNote `json:"related,omitempty"`
	// Cached is true when the summary came from the store (no model call).
	Cached bool `json:"cached"`
	// Stale is true when the note changed after its summary was written.
	Stale bool `json:"stale,omitempty"`
}

// NoteInsightGeneration is one batch summary response. Failed counts the notes
// the model returned nothing usable for, and Warning carries a non-fatal
// caveat (a truncated reply, a model that ignored the output protocol).
type NoteInsightGeneration struct {
	Insights []NoteInsight `json:"insights"`
	Failed   int           `json:"failed"`
	Warning  string        `json:"warning,omitempty"`
}

// NoteDuplicate is one suspected duplicate pair. Score is the same content
// overlap the related-note layer uses, just at a much higher threshold; Shared
// names the terms the two notes have in common so the user can judge the pair
// instead of trusting a number.
type NoteDuplicate struct {
	A      NoteRef  `json:"a"`
	B      NoteRef  `json:"b"`
	Score  float64  `json:"score"`
	Shared []string `json:"shared,omitempty"`
}
