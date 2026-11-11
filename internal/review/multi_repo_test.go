package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newGitRepo builds a throwaway git repo with one fixed-date commit.
func newGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2020-01-01T12:00:00", "GIT_COMMITTER_DATE=2020-01-01T12:00:00",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q")
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestPathKeyRoundTrip(t *testing.T) {
	root, rel := "/repos/a", "src/main/java/X.java"
	gotRoot, gotRel := SplitKey(PathKey(root, rel))
	if gotRoot != root || gotRel != rel {
		t.Fatalf("round trip = (%q,%q), want (%q,%q)", gotRoot, gotRel, root, rel)
	}
	// A plain relative path has no separator and stays readable.
	gotRoot, gotRel = SplitKey("src/main/java/X.java")
	if gotRoot != "" || gotRel != "src/main/java/X.java" {
		t.Fatalf("plain key = (%q,%q)", gotRoot, gotRel)
	}
}

func TestMultiRepoGrepAcrossRoots(t *testing.T) {
	dirA := newGitRepo(t, map[string]string{
		"src/A.java":      "class A { String marker = \"x\"; }",
		"src/Shared.java": "class Shared { String marker = \"y\"; }",
	})
	dirB := newGitRepo(t, map[string]string{
		"src/B.java": "class B { String marker = \"z\"; }",
	})
	m := NewMultiRepo(context.Background(), []string{dirA, dirB})

	hits := m.GrepJava("marker")
	if len(hits) != 3 {
		t.Fatalf("grep hits = %d, want 3 across both repos: %v", len(hits), hits)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i-1] > hits[i] {
			t.Fatalf("grep results must be sorted: %v", hits)
		}
	}
	// Each hit is an encoded key that decodes to one of the two roots.
	roots := map[string]bool{}
	for _, h := range hits {
		root, rel := SplitKey(h)
		roots[root] = true
		if !strings.HasSuffix(rel, ".java") {
			t.Errorf("bad relative path %q", rel)
		}
		if _, ok := m.Read(h); !ok {
			t.Errorf("Read failed for %q", h)
		}
	}
	if !roots[dirA] || !roots[dirB] {
		t.Errorf("both repositories must be represented: %v", roots)
	}
	// A second identical grep is served from the cache.
	if _, ok := m.cache["marker"]; !ok {
		t.Error("grep results should be cached per literal")
	}
	again := m.GrepJava("marker")
	if strings.Join(hits, ",") != strings.Join(again, ",") {
		t.Error("cached grep must return the same hits")
	}
}
