package conformance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/conformance"
)

// findFixture locates scripts/shared-chat-conformance.fixture.json from any
// package directory: the file is the ONE contract, and every runner walks up
// to it rather than carrying its own copy.
func findFixture(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "scripts", "shared-chat-conformance.fixture.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("shared-chat-conformance.fixture.json not found above")
		}
		dir = parent
	}
}

func loadFixture(t *testing.T) *conformance.Fixture {
	t.Helper()
	f, err := conformance.Load(findFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestFixtureIsSelfConsistent runs the validator over the real fixture: a
// malformed rule must fail HERE, before three runners each learn about it at
// their own layer.
func TestFixtureIsSelfConsistent(t *testing.T) {
	f := loadFixture(t)
	if len(f.SettledRules()) == 0 {
		t.Fatal("a fixture with no settled rules gates nothing")
	}
	if len(f.SkippedRules()) == 0 {
		t.Fatal("the open decisions (11.1-11.3) must stay recorded as skips, not deleted")
	}
	if f.ContractVersion.Core != "2.0.0" {
		t.Fatalf("fixture pins core %q; if the tool face changed, that change goes through the fixture/CoreVersion gates first", f.ContractVersion.Core)
	}
	// The message-field list is the wire shape the runners convert FROM; it
	// must name exactly the columns the server reads (db chatMessageColumns).
	want := []string{"id", "role", "content", "source", "reply_to", "created_at"}
	if len(f.Input.MessageFields) != len(want) {
		t.Fatalf("input.message_fields = %v, want %v", f.Input.MessageFields, want)
	}
	for i, name := range want {
		if f.Input.MessageFields[i] != name {
			t.Fatalf("input.message_fields[%d] = %q, want %q", i, f.Input.MessageFields[i], name)
		}
	}
}

// TestSkippedRulesNameTheirTodo keeps the skips honest: a skip that no longer
// names the open decision it waits on is either settled (promote it) or lost.
func TestSkippedRulesNameTheirTodo(t *testing.T) {
	f := loadFixture(t)
	for _, r := range f.SkippedRules() {
		if !strings.Contains(r.SkipReason, "11.") {
			t.Errorf("skipped rule %q does not name the TODO it waits on: %q", r.ID, r.SkipReason)
		}
	}
}
