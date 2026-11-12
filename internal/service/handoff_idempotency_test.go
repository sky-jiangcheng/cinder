package service

import (
	"testing"

	"repotory/internal/db"
)

// One session has at most one session-end handoff. Before schema v31 the
// SessionEnd hook, a retried repotory_handoff and the capture path each wrote
// their own row, and BuildProjectContext showed the near-duplicates side by
// side with no way to tell which was final (ADR-0057 D1).
func TestCreateHandoffNote_SameSessionWritesOneNote(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "idem-handoff", "/code/idem-handoff")

	in := HandoffInput{
		ProjectID: pid,
		Summary:   "wired the exporter",
		Changes:   []string{"added exporter.go"},
		SessionID: "sess-42",
	}
	first, err := s.CreateHandoffNote(in)
	if err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	if first.Deduped {
		t.Fatal("the first record is not a duplicate")
	}

	second, err := s.CreateHandoffNote(in)
	if err != nil {
		t.Fatalf("repeat handoff: %v", err)
	}
	if !second.Deduped {
		t.Error("the repeat must report deduped=true so the caller knows nothing new was written")
	}
	if second.NoteID != first.NoteID {
		t.Errorf("repeat landed on note %d, want the existing %d", second.NoteID, first.NoteID)
	}

	notes, err := db.ListNotes(s.db, pid)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	handoffs := 0
	for _, n := range notes {
		if IsHandoffNote(n) {
			handoffs++
		}
	}
	if handoffs != 1 {
		t.Fatalf("got %d handoff notes, want 1", handoffs)
	}

	// A different session is a different record: the key names the session,
	// not the project.
	other := in
	other.SessionID = "sess-43"
	third, err := s.CreateHandoffNote(other)
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	if third.Deduped || third.NoteID == first.NoteID {
		t.Errorf("a different session must write its own note, got deduped=%v id=%d", third.Deduped, third.NoteID)
	}
}

// The MCP tool carries no session id, so the key falls back to the payload
// digest. The retry that matters there is the same agent sending the same
// record twice — that must collapse, and a genuinely different record must
// not.
func TestCreateHandoffNote_WithoutSessionIDDedupesByPayload(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "idem-handoff-digest", "/code/idem-handoff-digest")

	in := HandoffInput{
		ProjectID: pid,
		Summary:   "same summary",
		Decisions: []string{"keep FTS5"},
	}
	first, err := s.CreateHandoffNote(in)
	if err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	repeat, err := s.CreateHandoffNote(in)
	if err != nil {
		t.Fatalf("repeat handoff: %v", err)
	}
	if !repeat.Deduped || repeat.NoteID != first.NoteID {
		t.Errorf("identical payload must collapse: deduped=%v id=%d (first %d)", repeat.Deduped, repeat.NoteID, first.NoteID)
	}

	in.Summary = "a different summary"
	changed, err := s.CreateHandoffNote(in)
	if err != nil {
		t.Fatalf("changed handoff: %v", err)
	}
	if changed.Deduped {
		t.Error("a record with different content must land as a new note")
	}
}

// Milestones are a series of states INSIDE one session (ADR-0054). A
// session-scoped key would silently absorb every checkpoint after the first,
// which is the opposite of what the rolling checkpoint exists for — so the key
// is per checkpoint when Milestone is set.
func TestCreateHandoffNote_MilestonesInOneSessionAllLand(t *testing.T) {
	s, _ := setupService(t)
	pid := seedProject(t, s.db, "idem-milestones", "/code/idem-milestones")

	base := HandoffInput{ProjectID: pid, SessionID: "sess-m", Milestone: true}

	first := base
	first.Summary = "checkpoint one"
	first.NextSteps = []string{"write the migration"}
	if _, err := s.CreateHandoffNote(first); err != nil {
		t.Fatalf("first milestone: %v", err)
	}

	second := base
	second.Summary = "checkpoint two"
	second.NextSteps = []string{"run the gates"}
	res, err := s.CreateHandoffNote(second)
	if err != nil {
		t.Fatalf("second milestone: %v", err)
	}
	if res.Deduped {
		t.Error("a new checkpoint must not be absorbed by the previous one")
	}

	// The same checkpoint re-sent is still a duplicate.
	repeat, err := s.CreateHandoffNote(second)
	if err != nil {
		t.Fatalf("repeat milestone: %v", err)
	}
	if !repeat.Deduped || repeat.NoteID != res.NoteID {
		t.Errorf("re-sent checkpoint must collapse: deduped=%v id=%d (want %d)", repeat.Deduped, repeat.NoteID, res.NoteID)
	}
}
