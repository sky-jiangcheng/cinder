package db

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// The page queue's whole claim is the same one the note queue makes: wiki_pages is
// the chokepoint every page writer converges on (compiler, approve, merge, CLI), so
// trigger-maintained dirtying covers them all. These tests write through db on
// purpose — that IS the writer path.

// A page created already-approved must queue its first embed; a page created
// pending must NOT (only approved content is ever embedded, ADR-0015).
func TestPageTriggersMarkDirtyByStatus(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "pstat")

	appr, err := CreateWikiPage(db, WikiKindConcept, "appr", "Approved", pid, "body")
	if err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, appr.ID, true, "approved page INSERT")

	pend, err := CreateWikiPageAs(db, WikiKindConcept, "pend", "Pending", pid, "body", WikiStatusPending, "wiki-compile")
	if err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, pend.ID, false, "pending page INSERT must not queue")
}

// Approving a pending page dirties it (its content now enters the approved set);
// moving an approved page back to pending also dirties it, so the drainer sees it
// and removes the now-stale vector.
func TestPageTriggersMarkDirtyOnStatusTransitions(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "ptrans")

	pend, err := CreateWikiPageAs(db, WikiKindConcept, "p", "P", pid, "body", WikiStatusPending, "wiki-compile")
	if err != nil {
		t.Fatal(err)
	}
	if err := TruncateDirtyPageIDs(db); err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, pend.ID, false, "pending, before any transition")

	// pending -> approved (approve).
	if err := SetWikiPageStatus(db, pend.ID, WikiStatusApproved); err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, pend.ID, true, "after approve")

	if err := TruncateDirtyPageIDs(db); err != nil {
		t.Fatal(err)
	}
	// approved -> pending (un-approve): must re-dirty so the vector is removed.
	if err := SetWikiPageStatus(db, pend.ID, WikiStatusPending); err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, pend.ID, true, "after un-approve")
}

// A content edit dirties an approved page (its vector must be recomputed). The
// value-comparison guard means an update that resends the SAME title/content does
// not queue a wasted embedding request — mirroring the note queue's lesson.
func TestPageTriggersMarkDirtyOnContentChange(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "pcontent")

	page, err := CreateWikiPage(db, WikiKindConcept, "c", "Title", pid, "body v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := TruncateDirtyPageIDs(db); err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, page.ID, false, "approved, before edit")

	if _, err := db.Exec("UPDATE wiki_pages SET content='body v2' WHERE id=?", page.ID); err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, page.ID, true, "after a real content change")

	// Resending the identical content is a no-op for the queue.
	if err := TruncateDirtyPageIDs(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE wiki_pages SET content='body v2' WHERE id=?", page.ID); err != nil {
		t.Fatal(err)
	}
	assertPageDirty(t, db, page.ID, false, "after re-writing identical content")
}

// The drainer splits "re-embed this approved page" from "remove the vector (gone
// or no longer approved)": one LEFT JOIN with the status guard collapses both
// non-approved cases to Gone.
func TestNextDirtyPageEmbedWorkSplitsLiveAndGone(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "psplit")

	live, err := CreateWikiPage(db, WikiKindConcept, "live", "Live", pid, "keep me")
	if err != nil {
		t.Fatal(err)
	}
	doomed, err := CreateWikiPage(db, WikiKindConcept, "doomed", "Doomed", pid, "gone soon")
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteWikiPage(db, doomed.ID); err != nil {
		t.Fatal(err)
	}
	// A page that WAS approved (so it is queued) and is then un-approved: its
	// stale vector must be removed, so it lands in Gone. A page that was only ever
	// pending is never queued at all — it has no vector to remove.
	unappr, err := CreateWikiPage(db, WikiKindConcept, "unappr", "Unapproved", pid, "was approved")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetWikiPageStatus(db, unappr.ID, WikiStatusPending); err != nil {
		t.Fatal(err)
	}

	work, err := NextDirtyPageEmbedWork(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(work.ToEmbed) != 1 || work.ToEmbed[0].ID != live.ID {
		t.Fatalf("ToEmbed = %+v, want exactly the approved page %d", work.ToEmbed, live.ID)
	}
	if want := PageEmbeddingText("Live", "keep me"); work.ToEmbed[0].Text != want {
		t.Errorf("Text = %q, want %q (shared builder)", work.ToEmbed[0].Text, want)
	}
	// Both the deleted page and the un-approved page land in Gone.
	goneSet := map[int64]bool{}
	for _, id := range work.Gone {
		goneSet[id] = true
	}
	if !goneSet[doomed.ID] {
		t.Errorf("Gone = %v, want the deleted page %d", work.Gone, doomed.ID)
	}
	if !goneSet[unappr.ID] {
		t.Errorf("Gone = %v, want the un-approved page %d (remove stale vector)", work.Gone, unappr.ID)
	}

	// Reading the queue must not consume it: a failed embed is retried, not lost.
	again, err := NextDirtyPageEmbedWork(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.ToEmbed)+len(again.Gone) != len(work.ToEmbed)+len(work.Gone) {
		t.Fatalf("queue shrank by being read: %+v", again)
	}

	resolved := append([]int64{}, work.Gone...)
	for _, w := range work.ToEmbed {
		resolved = append(resolved, w.ID)
	}
	if err := ClearDirtyPageIDs(db, resolved); err != nil {
		t.Fatal(err)
	}
	empty, err := NextDirtyPageEmbedWork(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.ToEmbed) != 0 || len(empty.Gone) != 0 {
		t.Fatalf("ClearDirtyPageIDs left %+v", empty)
	}
}

// v23 must be replay-safe: EnsurePageEmbedDirty runs from the migration AND from
// InitDB on every open, and the DDL uses DROP-before-CREATE, so a re-assert cannot
// fail or lose the table.
func TestEnsurePageEmbedDirtyIsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	pid := createTestProject(t, db, "pidemp")
	page, err := CreateWikiPage(db, WikiKindConcept, "i", "I", pid, "body")
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsurePageEmbedDirty(db); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	assertPageDirty(t, db, page.ID, true, "after re-applying the DDL (queue must survive)")
}

// Multiple processes open the same database: the desktop app, one cinder-mcp per
// IDE, cinder-server. InitDB re-asserts this DDL on EVERY open, so two of them
// starting together both run DROP TRIGGER IF EXISTS and then both run
// CREATE TRIGGER. The second CREATE can land after the first committed and used
// to take the whole process down with "trigger wiki_pages_embed_au already
// exists" before it could even answer `initialize`. The sibling
// EnsureNoteEmbedDirty always tolerated that error; this side did not.
//
// Reproduced with real handles on one file — each goroutine opens its own
// *sql.DB, which is what a separate process is from SQLite's point of view. A
// single handle cannot race with itself, and an in-memory database is private
// to its handle, so neither would catch this.
//
// This failure is user-visible: the client sees the server exit before the
// handshake ("init failed: EOF") with no explanation, and only the server's own
// log names the trigger.
//
// Each goroutine opens the way a real process does — OpenWithRetry, which is
// what cmd/mcp and cmd/server call — rather than bare InitDB. Sixteen openers
// racing on a -race runner can exhaust busy_timeout inside the migration and
// come back with SQLITE_BUSY; retrying that is the shipped behaviour, and the
// already-exists race above still surfaces because it is not a lock error.
func TestEnsurePageEmbedDirtySurvivesConcurrentOpeners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	first, err := InitDB(path)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	first.Close() //nolint:errcheck

	const openers = 16
	const rounds = 6
	var wg sync.WaitGroup
	failures := make(chan error, openers*rounds)
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Its own handle: the peer process.
			d, err := OpenWithRetry(path)
			if err != nil {
				failures <- err
				return
			}
			defer d.Close() //nolint:errcheck
			for j := 0; j < rounds; j++ {
				if err := EnsurePageEmbedDirty(d); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("concurrent ensure must not fail a process: %v", err)
	}
}

func assertPageDirty(t *testing.T, db *sql.DB, id int64, want bool, when string) {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM page_embed_dirty WHERE page_id = ?", id).Scan(&n); err != nil {
		t.Fatalf("%s: query page dirty queue: %v", when, err)
	}
	if got := n > 0; got != want {
		t.Errorf("%s: page %d dirty = %v, want %v", when, id, got, want)
	}
}
