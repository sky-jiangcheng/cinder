package service

import (
	"encoding/json"
	"errors"
	"testing"

	"repotory/internal/db"
)

// A mixed run is still status=done, but its note must say partial:true and
// carry the failure count so a poller can tell it from a clean success.
func TestCompileJobNoteMarksPartial(t *testing.T) {
	clean := compileJobNote("3 note(s) processed", 0)
	if clean != "3 note(s) processed" {
		t.Fatalf("clean note changed: %q", clean)
	}
	partial := compileJobNote("3 note(s) processed", 2)
	var payload map[string]any
	if err := json.Unmarshal([]byte(partial), &payload); err != nil {
		t.Fatalf("partial note is not a JSON payload: %v", err)
	}
	if payload["partial"] != true {
		t.Errorf("partial flag = %v, want true", payload["partial"])
	}
	if payload["failed"] != float64(2) {
		t.Errorf("failed count = %v, want 2", payload["failed"])
	}
	if payload["message"] == "" {
		t.Error("partial payload lost the human-readable message")
	}
}

// The partial payload must survive the finish write and be readable by a poller.
func TestPartialCompileJobNoteIsVisibleToPoller(t *testing.T) {
	svc, _ := setupService(t)
	pid := jobCorpus(t, svc)
	id, err := db.CreateCompileJob(svc.db, pid, 3)
	if err != nil {
		t.Fatal(err)
	}
	svc.finishJob(id, db.CompileJobDone, compileJobNote("3 note(s) processed", 2), "")
	j, err := svc.GetCompileJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != db.CompileJobDone {
		t.Fatalf("status = %s, want %s", j.Status, db.CompileJobDone)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(j.Note), &payload); err != nil {
		t.Fatalf("poller cannot parse the note payload: %v", err)
	}
	if payload["partial"] != true || payload["failed"] != float64(2) {
		t.Fatalf("poller lost the partial marker: %v", payload)
	}
}

// A filed model answer must land as a pending, ai-sourced page, not as an
// approved manual page (ADR-0015 "output lands ONLY as pending").
func TestFileAnswerAsPageLandsAsPendingAIPage(t *testing.T) {
	svc, _ := setupService(t)
	projectID, _, _ := buildGateCorpus(t, svc)
	ev := svc.GatherEvidence(projectID, "release checklist migration dry run rollback", EvidenceBudget{})
	id, err := svc.FileAnswerAsPage(projectID, "what is the rollback plan", "answer body", ev, nil)
	if err != nil {
		t.Fatalf("FileAnswerAsPage: %v", err)
	}
	page, err := db.GetWikiPageByID(svc.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if page.Status != db.WikiStatusPending {
		t.Errorf("filed page status = %q, want %q", page.Status, db.WikiStatusPending)
	}
	if page.Source != "ai" {
		t.Errorf("filed page source = %q, want %q", page.Source, "ai")
	}
}

// Consecutive failed rounds must back off exponentially (capped), and only a
// no-progress round with queued work counts as a failure.
func TestDrainerBackoffCapsAndRoundDetection(t *testing.T) {
	if got := drainerBackoff(0); got != embedDrainInterval {
		t.Fatalf("zero failures = %v, want %v", got, embedDrainInterval)
	}
	prev := drainerBackoff(1)
	for failures := 2; failures <= 20; failures++ {
		got := drainerBackoff(failures)
		if got < prev {
			t.Fatalf("backoff decreased at %d failures: %v < %v", failures, got, prev)
		}
		if got > embedDrainMaxBackoff {
			t.Fatalf("backoff %v exceeds cap %v", got, embedDrainMaxBackoff)
		}
		prev = got
	}
	if got := drainerBackoff(20); got != embedDrainMaxBackoff {
		t.Fatalf("backoff did not reach the cap: %v", got)
	}
	if !drainRoundFailed(errors.New("down"), true, 0) {
		t.Error("a returned error must count as a failure")
	}
	if !drainRoundFailed(nil, true, 0) {
		t.Error("queued work with zero progress must count as a failure")
	}
	if drainRoundFailed(nil, false, 0) {
		t.Error("an empty queue must not be treated as a failure")
	}
	if drainRoundFailed(nil, true, 1) {
		t.Error("a round that resolved work must not be a failure")
	}
}
