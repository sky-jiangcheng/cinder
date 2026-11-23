package service

import (
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
)

// Entry-side redaction (ADR-0068). The promise pinned here is the same one
// ADR-0030 makes for the exit side: with memory_redact off (the default),
// note bytes are stored exactly as given; with it on, structured credentials
// never reach the database through any of the service write paths.

func newRedactTestService(t *testing.T) *Service {
	t.Helper()
	database, err := db.InitDB(filepath.Join(t.TempDir(), "redact.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, "tester")
}

func TestNoteWritesUnchangedByDefault(t *testing.T) {
	svc := newRedactTestService(t)
	secret := "deploy key AKIAIOSFODNN7EXAMPLE"
	note, err := svc.CreateNoteWithMeta(1, "release", secret, "ops", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if note.Content != secret {
		t.Errorf("default state altered the content: %q", note.Content)
	}
}

func TestNoteWritesRedactedWhenEnabled(t *testing.T) {
	svc := newRedactTestService(t)
	if err := svc.UpdateConfig("memory_redact", "1"); err != nil {
		t.Fatal(err)
	}
	secret := "deploy key AKIAIOSFODNN7EXAMPLE"

	note, err := svc.CreateNoteWithMeta(1, "key AKIAIOSFODNN7EXAMPLE", secret, "", "knowledge", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note.Content, "AKIA") || !strings.Contains(note.Content, "[redacted:aws-access-key-id]") {
		t.Errorf("CreateNoteWithMeta content not masked: %q", note.Content)
	}
	if strings.Contains(note.Title, "AKIA") {
		t.Errorf("CreateNoteWithMeta title not masked: %q", note.Title)
	}

	plain, err := svc.CreateNote(1, secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.Content, "AKIA") {
		t.Errorf("CreateNote content not masked: %q", plain.Content)
	}

	keyed, _, err := svc.createNoteWithKey(1, "handoff", secret, "", "log", "claude-hook", "test-key-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(keyed.Content, "AKIA") {
		t.Errorf("createNoteWithKey content not masked: %q", keyed.Content)
	}
}

func TestAIAuditStatusReportsPresetGates(t *testing.T) {
	svc := newRedactTestService(t)
	status, err := svc.GetAIAuditStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.PresetsEnabled || status.RedactEnabled || status.PresetCount != 0 {
		t.Errorf("gates not default-off: %+v", status)
	}

	for k, v := range map[string]string{
		"ai_review_input":   "1",
		"ai_review_presets": "1",
		"memory_redact":     "1",
	} {
		if err := svc.UpdateConfig(k, v); err != nil {
			t.Fatal(err)
		}
	}
	status, err = svc.GetAIAuditStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !status.PresetsEnabled || !status.RedactEnabled {
		t.Errorf("gates not reported on: %+v", status)
	}
	if status.PresetCount == 0 || status.RuleCount != status.PresetCount {
		t.Errorf("preset rules not counted (rule=%d preset=%d)", status.RuleCount, status.PresetCount)
	}
}
