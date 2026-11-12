package db

import (
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{",,", ""},
		// The exact sample that shipped the bug: stored verbatim, it became
		// one tag row and the frontend filter could never match it.
		{"cbipay，样例测试,  支付 ,cbipay", "cbipay, 样例测试, 支付"},
		{"a，b，c", "a, b, c"},        // full-width commas split too
		{" a , b ,, c ", "a, b, c"}, // trim + drop empties
		{"a,a,b", "a, b"},           // dedupe keeps first occurrence
		{"single", "single"},
		{"  spaced  ", "spaced"},
	}
	for _, c := range cases {
		if got := NormalizeTags(c.in); got != c.want {
			t.Errorf("NormalizeTags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The db layer is the chokepoint every writer converges on (desktop UI, MCP,
// plugin runtime), so all three tag-writing entry points must store the
// canonical form — a note created with a comma-joined string must come back
// split, and ListAllTags must offer individual tags, not the joined string.
func TestNoteTagWritePathsNormalize(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	pid := createTestProject(t, db, "proj-tags")

	raw := "cbipay，样例测试,  支付 ,cbipay"
	want := "cbipay, 样例测试, 支付"

	note, err := CreateNoteEx(db, pid, "title", "body", raw, "knowledge", "manual")
	if err != nil {
		t.Fatalf("CreateNoteEx: %v", err)
	}
	if note.Tags != want {
		t.Errorf("CreateNoteEx stored tags %q, want %q", note.Tags, want)
	}

	if err := UpdateNoteMeta(db, note.ID, "title", " x , x , y ", "knowledge", false); err != nil {
		t.Fatalf("UpdateNoteMeta: %v", err)
	}
	updated, err := GetNoteByID(db, note.ID)
	if err != nil {
		t.Fatalf("GetNoteByID: %v", err)
	}
	if updated.Tags != "x, y" {
		t.Errorf("UpdateNoteMeta stored tags %q, want %q", updated.Tags, "x, y")
	}

	if err := UpdateNoteFull(db, note.ID, "body2", "title2", "m，n", "other", true, 0); err != nil {
		t.Fatalf("UpdateNoteFull: %v", err)
	}
	updated, err = GetNoteByID(db, note.ID)
	if err != nil {
		t.Fatalf("GetNoteByID after full update: %v", err)
	}
	if updated.Tags != "m, n" {
		t.Errorf("UpdateNoteFull stored tags %q, want %q", updated.Tags, "m, n")
	}

	tags, err := ListAllTags(db)
	if err != nil {
		t.Fatalf("ListAllTags: %v", err)
	}
	if len(tags) != 2 || tags[0] != "m" || tags[1] != "n" {
		t.Errorf("ListAllTags = %v, want [m n] (split, distinct)", tags)
	}
}
