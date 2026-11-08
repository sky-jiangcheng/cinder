package review

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The review reads git through the CLI rather than a library: the process is
// short-lived, git is already a hard requirement of the product (the scanner
// and stats layer shell out to it too), and `git show` composes patch,
// rename detection and binary detection in one place where a library would
// need four calls and its own version-skew story.

// Commit is one commit in the reviewed window.
type Commit struct {
	SHA     string
	Author  string
	Message string
	At      time.Time
}

// LogCommits lists commits touching the repository between since and until.
// Merge commits are excluded: their patch is the union of both sides, so
// reporting on them would double every finding.
func LogCommits(ctx context.Context, repoPath string, since, until time.Time) ([]Commit, error) {
	args := []string{
		"log", "--no-merges", "--no-color",
		"--pretty=format:%H%x1f%an%x1f%at%x1f%s",
		"--since=@" + strconv.FormatInt(since.Unix(), 10),
		"--until=@" + strconv.FormatInt(until.Unix(), 10),
	}
	out, err := runGit(ctx, repoPath, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\x1f", 4)
		if len(f) < 4 {
			continue
		}
		ts, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			continue
		}
		commits = append(commits, Commit{SHA: f[0], Author: f[1], Message: f[3], At: time.Unix(ts, 0)})
	}
	return commits, nil
}

// CommitPatch returns one commit's changes. `git show --format=` prints the
// patch alone; the diff-filter keeps deletions out of the rule pass only if
// the caller asks (deletions still matter for route renames, so we do not).
func CommitPatch(ctx context.Context, repoPath, sha string) ([]FileChange, error) {
	out, err := runGit(ctx, repoPath,
		"show", "--no-color", "--no-ext-diff", "--unified=3", "--format=", "--find-renames", sha)
	if err != nil {
		return nil, err
	}
	return ParseUnifiedDiff(out), nil
}

// FileAtCommit reads one file's content as of the commit. Content comes from
// the commit, not the working tree: the review is about what was committed,
// and a dirty checkout must not make a finding appear or disappear.
func FileAtCommit(ctx context.Context, repoPath, sha, path string) (string, error) {
	out, err := runGit(ctx, repoPath, "show", sha+":"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

// runGit runs git in repoPath and returns stdout. stderr is folded into the
// error: a failing git command that does not say why is the most expensive
// kind of bug to chase in a background job.
func runGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

// FSRepo is the filesystem-backed Repo used by the rules that must look
// outside the changed file. It caches both the file list and the contents, so
// a day with 300 changed files in one repository reads the tree once.
type FSRepo struct {
	root     string
	javaOnce bool
	java     []string
	cache    map[string]string
	ctx      context.Context
}

// NewFSRepo returns a Repo rooted at a working tree.
func NewFSRepo(ctx context.Context, root string) *FSRepo {
	return &FSRepo{root: root, cache: map[string]string{}, ctx: ctx}
}

// Read returns a repo-relative file's content.
func (r *FSRepo) Read(path string) (string, bool) {
	if c, ok := r.cache[path]; ok {
		return c, true
	}
	content, err := FileAtCommit(r.ctx, r.root, "HEAD", path)
	if err != nil {
		return "", false
	}
	r.cache[path] = content
	return content, true
}

// maxGrepFiles bounds a full-tree scan. A route rename in a repository with
// tens of thousands of Java files is not worth minutes of the user's morning;
// past the bound the rule reports what it managed to read (a shorter list of
// callers is still actionable) rather than hanging.
const maxGrepFiles = 4000

// GrepJava returns the repo-relative paths of .java files under the tree that
// contain the literal string.
func (r *FSRepo) GrepJava(literal string) []string {
	if !r.javaOnce {
		r.javaOnce = true
		r.java = javaFiles(r.ctx, r.root)
	}
	var hits []string
	for _, p := range r.java {
		content, ok := r.Read(p)
		if !ok {
			continue
		}
		if strings.Contains(content, literal) {
			hits = append(hits, p)
		}
	}
	sort.Strings(hits)
	return hits
}

// javaFiles lists tracked and untracked .java files. It uses `git ls-files`
// (not a walk) so build output, vendored jars and node_modules do not get
// scanned, and so .gitignore is respected for free.
func javaFiles(ctx context.Context, root string) []string {
	out, err := runGit(ctx, root, "ls-files", "--cached", "--others", "--exclude-standard", "*.java")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] || !strings.HasSuffix(line, ".java") {
			continue
		}
		// Glob expansion can return absolute paths on some git builds; keep
		// the tree consistent by making everything repo-relative.
		if filepath.IsAbs(line) {
			continue
		}
		seen[line] = true
		paths = append(paths, line)
		if len(paths) >= maxGrepFiles {
			break
		}
	}
	sort.Strings(paths)
	return paths
}
