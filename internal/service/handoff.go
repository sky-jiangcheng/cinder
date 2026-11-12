package service

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"repotory/internal/db"
)

// handoffTag is the reserved tag marking a note as a structured session
// handoff. BuildProjectContext sorts these notes first, so the next session
// reads the previous one's exit record before anything else.
const handoffTag = "handoff"

// milestoneTag marks a handoff as a rolling checkpoint (ADR-0054): it is
// written every time the agent completes a meaningful work unit, not just at
// session end. The tag lets readers distinguish "current state" from "session
// summary" without a new DB column or kind.
const milestoneTag = "milestone"

// Handoff write bounds. A handoff is agent-authored JSON arriving over MCP,
// so it is untrusted input: without caps a runaway agent could write a
// multi-megabyte summary or thousands of bullets in one call, and the next
// repotory_context would embed all of it. The note-level bounds still apply
// on top of these (the rendered Markdown is written through CreateNoteWithMeta).
const (
	maxHandoffSummaryLen = 4_000
	maxHandoffItems      = 20    // items per section
	maxHandoffItemLen    = 1_000 // bytes per bullet
)

// HandoffInput is the structured record of what an AI session accomplished.
// Sections are optional except Summary, but an empty record carries no value
// for the next session, so at least one section must be non-empty.
type HandoffInput struct {
	ProjectID int64
	// Agent names the tool that produced the handoff, e.g. "claude-code" or
	// "cursor". Optional; recorded in the note header for provenance.
	Agent     string
	Summary   string
	Changes   []string
	Decisions []string
	Gotchas   []string
	NextSteps []string
	// Tags appends user-supplied tags to the reserved "handoff" tag.
	Tags string
	// Source records who wrote the handoff. Empty means "an agent pushed it
	// over MCP" — the path the session protocol defines — so it only has to be
	// set when the writer is something else (a person in the desktop app).
	// Filing a hand-written handoff as "mcp" would make the note claim a
	// provenance it does not have.
	Source string
	// WorkspacePath is the directory the session was working in (an IDE workspace
	// folder or an agent cwd). Optional. When it resolves inside a scanned
	// repository, the note is attributed to that repository in note_repositories —
	// which is what lets a later session opened in the SAME folder lead with these
	// notes instead of the whole project's.
	//
	// It is not stored verbatim on the note: the repository row is the durable
	// identity, so renaming or moving the folder does not orphan the attribution
	// silently, and the note never leaks an absolute path into its content.
	WorkspacePath string
	// Milestone marks this handoff as a rolling checkpoint (ADR-0054), not a
	// session-end summary. A milestone is written every time the agent completes
	// a meaningful work unit (a feature, a fix, a test run), so that a session
	// killed mid-work — by credit exhaustion, timeout, crash — still leaves the
	// last checkpoint as "current state" for the next session.
	//
	// Milestone=true → title "Milestone Handoff", tag "milestone", Next Steps
	// means "what is currently unfinished". Milestone=false (default) → title
	// "Session Handoff", tag unchanged, Next Steps means "what the next session
	// should do" — the full plan.
	Milestone bool
	// SessionID identifies the session this record describes, when the writer
	// knows it (the capture path always does; an agent over MCP may). It is
	// not stored on the note — it only seeds the idempotency key, so a retry of
	// the same session collapses onto the same note instead of appending a
	// second near-identical handoff (ADR-0057 D1).
	SessionID string
}

// CreateHandoffNote persists a session handoff as a knowledge note rendered
// from a fixed Markdown template. A stable template matters: the note is
// consumed by both humans (desktop UI) and agents (repotory_context), and the
// section headings are the contract between the writer and every future
// reader.
func (s *Service) CreateHandoffNote(in HandoffInput) (*HandoffResult, error) {
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		return nil, fmt.Errorf("summary is required")
	}
	if !handoffHasContent(in) {
		return nil, fmt.Errorf("at least one of changes, decisions, gotchas, next_steps must be non-empty")
	}
	if err := validateHandoffBounds(in, summary); err != nil {
		return nil, err
	}

	title := handoffTitle(in.Milestone)
	tags := handoffTag
	if in.Milestone {
		tags = tags + ", " + milestoneTag
	}
	if extra := strings.TrimSpace(in.Tags); extra != "" {
		tags = tags + ", " + extra
	}

	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "mcp"
	}
	body := renderHandoffMarkdown(in, summary)
	note, deduped, err := s.createNoteWithKey(in.ProjectID, title, body, tags, "knowledge", source, handoffIdempotencyKey(in, body))
	if err != nil {
		return nil, err
	}
	res := &HandoffResult{NoteID: note.ID, Title: note.Title, Tags: tags, Milestone: in.Milestone, Deduped: deduped}

	// A deduped write already carries its repository attribution (the first
	// write did it), and re-running the attribution would be pure overhead on
	// the exact path this exists to make cheap: the retry.
	if deduped {
		return res, nil
	}

	// Attribute the note to the repository the session was standing in. This is the
	// writer note_repositories never had; without it the repository scope in
	// BuildProjectContext can never find anything to lead with.
	//
	// Deliberately non-fatal, and reported: the handoff already exists and is
	// useful at project scope, so failing the whole call because the finer
	// attribution did not land would destroy a session record over a ranking hint.
	if strings.TrimSpace(in.WorkspacePath) != "" {
		if r := s.ResolveProjectByPath(in.WorkspacePath); r != nil && r.Repository != nil &&
			r.Project != nil && r.Project.ID == in.ProjectID {
			if err := s.attachNoteToRepository(note.ID, r.Repository.ID); err != nil {
				res.RepositoryNote = fmt.Sprintf("note not attributed to repository %d: %v",
					r.Repository.ID, err)
			} else {
				res.RepositoryID = r.Repository.ID
				res.RepositoryPath = r.Repository.Path
			}
		}
	}
	return res, nil
}

// handoffIdempotencyKey names what a record is ABOUT, which is what makes a
// repeat write collapse instead of appending a second near-identical handoff
// (ADR-0057 D1). Three shapes, because "the same session" and "the same
// checkpoint" are different things:
//
//   - session-scoped (a session-end record): handoff:<project>:<session>. The
//     SessionEnd hook, a retried repotory_handoff and the auto-capture of one
//     session all land on one row.
//   - milestone: handoff:<project>[:<session>]:milestone:<digest>. ADR-0054
//     writes several checkpoints INTO one session, so a session-scoped key
//     would collapse every checkpoint after the first into the first and
//     silently lose the work it recorded. The digest of the rendered record
//     is the identity of a checkpoint: the same one re-sent dedupes, a new one
//     lands.
//   - no session id (the MCP tool does not carry one): the digest of the
//     rendered record. The retry that matters there is the same agent sending
//     the same payload twice, and that digest survives it.
//
// Project-scoped in every shape: the same summary text in two projects is two
// records, not a duplicate.
func handoffIdempotencyKey(in HandoffInput, body string) string {
	session := strings.TrimSpace(in.SessionID)
	digest := sha256.Sum256([]byte(body))
	switch {
	case in.Milestone && session != "":
		return fmt.Sprintf("handoff:%d:%s:milestone:%x", in.ProjectID, session, digest[:16])
	case in.Milestone:
		return fmt.Sprintf("handoff:%d:milestone:%x", in.ProjectID, digest[:16])
	case session != "":
		return fmt.Sprintf("handoff:%d:%s", in.ProjectID, session)
	default:
		return fmt.Sprintf("handoff:%d:sha256:%x", in.ProjectID, digest[:16])
	}
}

// validateHandoffBounds caps the structured sections before rendering, so
// the failure message names the section and the limit the agent hit.
func validateHandoffBounds(in HandoffInput, summary string) error {
	if len(summary) > maxHandoffSummaryLen {
		return fmt.Errorf("summary too long: %d bytes (max %d)", len(summary), maxHandoffSummaryLen)
	}
	for name, section := range map[string][]string{
		"changes": in.Changes, "decisions": in.Decisions,
		"gotchas": in.Gotchas, "next_steps": in.NextSteps,
	} {
		items := nonEmptyLines(section)
		if len(items) > maxHandoffItems {
			return fmt.Errorf("%s has too many items: %d (max %d)", name, len(items), maxHandoffItems)
		}
		for _, item := range items {
			if len(item) > maxHandoffItemLen {
				return fmt.Errorf("%s item too long: %d bytes (max %d)", name, len(item), maxHandoffItemLen)
			}
		}
	}
	return nil
}

// HandoffResult reports what was persisted so the calling agent can confirm
// the handoff landed instead of assuming.
type HandoffResult struct {
	NoteID int64  `json:"note_id"`
	Title  string `json:"title"`
	Tags   string `json:"tags"`
	// Milestone reports whether this handoff is a rolling checkpoint (ADR-0054)
	// or a session-end summary, so the calling agent can confirm the write
	// landed as the right type.
	Milestone bool `json:"milestone"`
	// RepositoryID / RepositoryPath echo the attribution when it succeeded, so the
	// caller can tell "recorded against this module" from "recorded against the
	// project" instead of assuming the finer scope happened.
	RepositoryID   int64  `json:"repository_id,omitempty"`
	RepositoryPath string `json:"repository_path,omitempty"`
	// RepositoryNote carries why attribution did NOT happen. It is a note, not an
	// error, precisely because the handoff itself succeeded.
	RepositoryNote string `json:"repository_note,omitempty"`
	// Deduped reports that this write described a session that already has a
	// handoff, so nothing new was written and NoteID points at the existing
	// note (ADR-0057 D1). It is a success, and the caller should treat the
	// record as updated-in-place rather than assume a second handoff exists.
	Deduped bool `json:"deduped,omitempty"`
}

// HandoffStatus is the minimal status surface for UI chrome (currently the VS
// Code status bar). It intentionally excludes content: status updates may poll
// periodically, and the last handoff's body belongs to the knowledge reader.
type HandoffStatus struct {
	NoteID    int64  `json:"note_id"`
	ProjectID int64  `json:"project_id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

// HandoffEntry is one row of the first-screen memory ring (D25). Like
// HandoffStatus it is deliberately content-free: the ring is an entry point,
// not a reader — NoteID is the deep-link target, so opening an entry lands on
// that exact note in the project's notes tab rather than on the project
// overview. The body still lives on the project's notes surface.
type HandoffEntry struct {
	NoteID       int64  `json:"note_id"`
	ProjectID    int64  `json:"project_id"`
	ProjectName  string `json:"project_name"`
	Title        string `json:"title"`
	UpdatedAt    string `json:"updated_at"`
	AutoCaptured bool   `json:"auto_captured"`
}

// The memory-ring window is a glanceable list, not a query result: callers
// without an explicit limit get the default, and larger asks are clamped.
const (
	defaultRecentHandoffLimit = 10
	maxRecentHandoffLimit     = 50
)

// ListRecentHandoffs returns the newest handoff notes for the first-screen
// memory ring, newest first. limit<=0 takes the default; larger asks are
// clamped. A database error logs and returns what was read (nil on total
// failure): the ring degrades to its empty state instead of erroring the
// whole first screen.
func (s *Service) ListRecentHandoffs(limit int) []HandoffEntry {
	if limit <= 0 {
		limit = defaultRecentHandoffLimit
	}
	if limit > maxRecentHandoffLimit {
		limit = maxRecentHandoffLimit
	}
	notes, err := db.ListRecentHandoffNotes(s.db, 0, limit)
	if err != nil {
		log.Printf("ListRecentHandoffs: %v", err)
		return nil
	}
	out := make([]HandoffEntry, 0, len(notes))
	for _, n := range notes {
		out = append(out, HandoffEntry{
			NoteID:      n.ID,
			ProjectID:   n.ProjectID,
			ProjectName: n.ProjectName,
			Title:       n.Title,
			UpdatedAt:   n.UpdatedAt,
			// The auto-capture tag is written as "handoff, auto-captured"
			// (handoff_capture.go); normalize the same way the SQL predicate
			// does so the badge matches what actually got imported.
			AutoCaptured: strings.Contains(strings.ReplaceAll(strings.ToLower(n.Tags), " ", ""), "auto-captured"),
		})
	}
	return out
}

// LatestHandoff returns the newest handoff note's status. projectID>0 scopes
// to one project; zero means the newest across the knowledge base. A missing
// handoff is (nil, nil), not an error.
func (s *Service) LatestHandoff(projectID int64) (*HandoffStatus, error) {
	note, err := db.LatestHandoffNote(s.db, projectID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &HandoffStatus{
		NoteID:    note.ID,
		ProjectID: note.ProjectID,
		Title:     note.Title,
		UpdatedAt: note.UpdatedAt,
	}, nil
}

// handoffHasContent checks whether any structured section carries information.
func handoffHasContent(in HandoffInput) bool {
	for _, section := range [][]string{in.Changes, in.Decisions, in.Gotchas, in.NextSteps} {
		for _, item := range section {
			if strings.TrimSpace(item) != "" {
				return true
			}
		}
	}
	return false
}

// handoffTitle returns the note title for a handoff, distinguishing a rolling
// checkpoint (ADR-0054) from a session-end summary. The timestamp is the same
// for both — the distinction is in the word, because the next session reads the
// title to know whether this is "where we are now" or "how the last session
// ended".
func handoffTitle(milestone bool) string {
	word := "Session"
	if milestone {
		word = "Milestone"
	}
	return fmt.Sprintf("%s Handoff %s", word, time.Now().Format("2006-01-02 15:04"))
}

// renderHandoffMarkdown renders the canonical handoff template. Sections with
// no content are omitted entirely: an absent heading tells the reader "the
// previous session recorded nothing here" without a wall of empty bullets.
//
// A milestone handoff is labelled in the header so the reader knows it is a
// checkpoint, not a summary: the Next Steps section in a milestone means
// "what is currently unfinished", not "what the next session should do".
func renderHandoffMarkdown(in HandoffInput, summary string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Session Handoff\n\n"))
	if in.Milestone {
		b.WriteString("**[Rolling checkpoint — not a session summary]**\n\n")
	}
	if agent := strings.TrimSpace(in.Agent); agent != "" {
		b.WriteString(fmt.Sprintf("> Written by `%s`\n\n", agent))
	}
	b.WriteString("## Summary\n\n")
	b.WriteString(summary)
	b.WriteString("\n\n")

	writeSection := func(heading string, items []string) {
		lines := nonEmptyLines(items)
		if len(lines) == 0 {
			return
		}
		b.WriteString(fmt.Sprintf("## %s\n\n", heading))
		for _, item := range lines {
			b.WriteString(fmt.Sprintf("- %s\n", item))
		}
		b.WriteString("\n")
	}
	writeSection("Changes", in.Changes)
	writeSection("Decisions", in.Decisions)
	writeSection("Gotchas", in.Gotchas)
	writeSection("Next Steps", in.NextSteps)

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// nonEmptyLines drops blank items and trims whitespace so template output
// stays tidy regardless of how the caller formatted the sections.
func nonEmptyLines(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if line := strings.TrimSpace(item); line != "" {
			out = append(out, line)
		}
	}
	return out
}
