package service

import (
	"context"
	"testing"
	"time"

	"repotory/internal/db"
	"repotory/internal/search/temporal"
)

var temporalSvcAnchor = time.Date(2026, 11, 23, 15, 0, 0, 0, time.FixedZone("CST", 8*3600))

// The temporal arm must work with semantic search OFF (its default): the
// target shares zero trigrams with the query, so only the date window can
// recall it.
func TestSearchAllTemporalArmRecallsDateOnlyTarget(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "temporal-svc", "/tmp/temporal-svc")
	target, err := db.CreateNoteEx(svc.db, pid, "Session Handoff 2026-11-17 16:46", "联调记录与问题清单", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-17 08:46:38", target.ID); err != nil {
		t.Fatal(err)
	}
	decoy, err := db.CreateNoteEx(svc.db, pid, "Session Handoff 2026-11-18 21:04", "另一个项目的联调记录", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-18 13:04:00", decoy.ID); err != nil {
		t.Fatal(err)
	}

	ctx := temporal.WithAnchor(context.Background(), temporalSvcAnchor)
	hits, err := svc.SearchAll(ctx, "11月17号那天 MiddlePlatform 项目在解决什么问题？")
	if err != nil {
		t.Fatal(err)
	}
	var foundTarget, foundDecoy bool
	for _, h := range hits {
		if h.ID == target.ID && h.Type == "note" {
			foundTarget = true
		}
		if h.ID == decoy.ID && h.Type == "note" {
			foundDecoy = true
		}
	}
	if !foundTarget {
		t.Fatalf("date-window target not recalled; hits=%+v", hits)
	}
	if foundDecoy {
		t.Fatal("a note outside the date window leaked into the results")
	}
}

// A query without a date expression must be untouched by the arm.
func TestSearchAllNoDateExpressionUnchanged(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "temporal-nodate", "/tmp/temporal-nodate")
	note, err := db.CreateNoteEx(svc.db, pid, "部署流水线", "部署流水线配置完成", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	ctx := temporal.WithAnchor(context.Background(), temporalSvcAnchor)
	hits, err := svc.SearchAll(ctx, "部署流水线")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != note.ID {
		t.Fatalf("lexical ordering changed for a date-free query: %+v", hits)
	}
}

// The citation path (SearchNotesStrict) deliberately skips the temporal arm:
// a window hit is a creation-time coincidence, not a content match.
func TestSearchNotesStrictSkipsTemporalArm(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "temporal-strict", "/tmp/temporal-strict")
	target, err := db.CreateNoteEx(svc.db, pid, "Session Handoff 2026-11-17 16:46", "MiddlePlatform 联调记录", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-17 08:46:38", target.ID); err != nil {
		t.Fatal(err)
	}
	ctx := temporal.WithAnchor(context.Background(), temporalSvcAnchor)
	hits, err := svc.SearchNotesStrict(ctx, "11月17号那天 MiddlePlatform 项目在解决什么问题？")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.ID == target.ID {
			t.Fatal("strict citation path pulled in a date-window hit with no content match")
		}
	}
}

// SearchNotes (the continuity surface) gets the same arm as SearchAll.
func TestSearchNotesTemporalArm(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "temporal-notes", "/tmp/temporal-notes")
	target, err := db.CreateNoteEx(svc.db, pid, "Session Handoff 2026-11-20 11:03", "上周五的交接内容", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-20 03:03:00", target.ID); err != nil {
		t.Fatal(err)
	}
	ctx := temporal.WithAnchor(context.Background(), temporalSvcAnchor)
	hits, err := svc.SearchNotes(ctx, "上周五的会话交接记录了什么？")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		if h.ID == target.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("relative-date target not recalled via SearchNotes; hits=%+v", hits)
	}
}