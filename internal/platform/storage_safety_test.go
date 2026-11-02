package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckStorageSafety_Clean(t *testing.T) {
	tmp := t.TempDir()
	rep := CheckStorageSafety(tmp)
	if !rep.IsSafe {
		t.Fatalf("expected clean tempdir to be safe, got warning: %s", rep.Warning)
	}
}

func TestCheckStorageSafety_CloudSync(t *testing.T) {
	fakeICloud := "/Users/test/Library/Mobile Documents/com~apple~CloudDocs/repotory"
	rep := CheckStorageSafety(fakeICloud)
	if rep.IsSafe {
		t.Fatal("expected iCloud path to be detected as unsafe")
	}
	if rep.RiskType != "cloud_sync" {
		t.Fatalf("expected risk_type cloud_sync, got %s", rep.RiskType)
	}
}

func TestCheckStorageSafety_FuseHidden(t *testing.T) {
	tmp := t.TempDir()
	hidden := filepath.Join(tmp, ".fuse_hidden12345")
	if err := os.WriteFile(hidden, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	rep := CheckStorageSafety(tmp)
	if rep.IsSafe {
		t.Fatal("expected directory with .fuse_hidden to be marked unsafe")
	}
	if rep.RiskType != "fuse_network" {
		t.Fatalf("expected risk_type fuse_network, got %s", rep.RiskType)
	}
}

// TestCheckStorageSafety_MissingDbFileInExistingDir covers the fresh-install
// shape: the data directory exists (GetDbPath creates it before the check
// runs) but the database file does not exist yet.
//
// The target used to be resolved to the database path itself in that case, so
// the writability probe wrote "<db file>/.safety_probe_<pid>" and could never
// succeed. Every fresh install — and every launch right after the data
// directory was removed — therefore reported a bogus "unwritable" storage risk.
func TestCheckStorageSafety_MissingDbFileInExistingDir(t *testing.T) {
	dir := t.TempDir()
	rep := CheckStorageSafety(filepath.Join(dir, "repotory.db"))
	if !rep.IsSafe {
		t.Fatalf("expected a missing db file in an existing dir to be safe, got %s: %s", rep.RiskType, rep.Warning)
	}
	if rep.Path != dir {
		t.Fatalf("expected report path %s, got %s", dir, rep.Path)
	}
}

// TestCheckStorageSafety_ExistingDbFile pins the other resolution branch: an
// existing database file reports its parent directory.
func TestCheckStorageSafety_ExistingDbFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "repotory.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	rep := CheckStorageSafety(dbPath)
	if !rep.IsSafe {
		t.Fatalf("expected an existing db file to be safe, got %s: %s", rep.RiskType, rep.Warning)
	}
	if rep.Path != dir {
		t.Fatalf("expected report path %s, got %s", dir, rep.Path)
	}
}

// TestCheckStorageSafety_ProbeLeavesNoFile guards the probe itself: the
// temporary file it creates must not survive the check.
func TestCheckStorageSafety_ProbeLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	CheckStorageSafety(filepath.Join(dir, "repotory.db"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".safety_probe_") {
			t.Fatalf("probe file %s was left behind", e.Name())
		}
	}
}
