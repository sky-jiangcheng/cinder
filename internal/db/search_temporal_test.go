package db

import (
	"context"
	"testing"
	"time"
)

// Local day 2026-11-17 in UTC+8 expressed as the UTC instant range the column
// actually stores: [2026-11-16 16:00Z, 2026-11-17 16:00Z).
var temporalTestFrom = time.Date(2026, 11, 16, 16, 0, 0, 0, time.UTC)
var temporalTestTo = time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)

func TestNoteIDsInWindowFTSStage(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	pid := createTestProject(t, database, "temporal-fts")
	inWindow, err := CreateNoteEx(database, pid, "流水线配置", "部署流水线配置完成", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	outside, err := CreateNoteEx(database, pid, "流水线配置旧", "部署流水线配置完成", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-17 02:00:00", inWindow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-18 02:00:00", outside.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := NoteIDsInWindow(context.Background(), database, "部署流水线", temporalTestFrom, temporalTestTo, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != inWindow.ID {
		t.Fatalf("ids = %v, want [%d] (the out-of-window twin must not leak)", ids, inWindow.ID)
	}
}

// The recency fallback is the arm's reason to exist: a date question can share
// zero trigrams with its target, so FTS finds nothing and the window itself
// must supply the recall.
func TestNoteIDsInWindowRecencyFallback(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	pid := createTestProject(t, database, "temporal-fallback")
	target, err := CreateNoteEx(database, pid, "Session Handoff 2026-11-17 16:46", "MiddlePlatform 联调记录", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-17 08:46:38", target.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := NoteIDsInWindow(context.Background(), database, "11月17号那天在解决什么问题？", temporalTestFrom, temporalTestTo, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != target.ID {
		t.Fatalf("ids = %v, want [%d]", ids, target.ID)
	}
}

// A note created at 2026-11-16 17:00 UTC is 2026-11-17 01:00 in UTC+8: it
// belongs to the LOCAL day even though its UTC date is the previous day.
func TestNoteIDsInWindowLocalDayBoundary(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	pid := createTestProject(t, database, "temporal-boundary")
	early, err := CreateNoteEx(database, pid, "凌晨笔记", "本地时间十七号凌晨写的", "", "other", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE project_notes SET created_at=? WHERE id=?", "2026-11-16 17:00:00", early.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := NoteIDsInWindow(context.Background(), database, "随便什么查询词", temporalTestFrom, temporalTestTo, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != early.ID {
		t.Fatalf("ids = %v, want [%d] (local-day boundary must include it)", ids, early.ID)
	}
}

func TestNoteIDsInWindowEmpty(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	pid := createTestProject(t, database, "temporal-empty")
	if _, err := CreateNoteEx(database, pid, "窗口外", "不在窗口里", "", "other", "manual"); err != nil {
		t.Fatal(err)
	}
	ids, err := NoteIDsInWindow(context.Background(), database, "窗口外", temporalTestFrom, temporalTestTo, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}