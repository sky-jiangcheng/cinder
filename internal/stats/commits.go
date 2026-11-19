package stats

import (
	"context"
	"log"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RecentCommit holds information about the most recent commit.
type RecentCommit struct {
	// Hash is the full commit SHA. Empty only if git output was unparseable.
	// Kept because a commit row is actionable: it is what you copy to cherry-pick
	// or paste into a search, and what identifies the commit on the forge.
	Hash    string `json:"hash"`
	Time    string `json:"time"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
}

// recentCommitConcurrency bounds how many `git log -1` processes run at once. The
// status bar used to fork one strictly sequentially per repository, so a machine
// with 140 known repos paid ~2.5s of cold-start latency before the service-side TTL
// cache took over (measured: 2.58s first call, 0.4ms after).
const recentCommitConcurrency = 16

// recentCommitBudget caps the whole fan-out. Each repo already carries QueryTimeout
// (30s) on its own git call, so with no overall budget a single wedged repository —
// an unresponsive mount is the realistic case — could hold the status bar for 30s,
// and several of them stack linearly. Trading completeness for bounded latency is
// the right call for a status strip, and stopping early is logged rather than
// silent.
const recentCommitBudget = 5 * time.Second

// recentCommitFor reads the newest commit of one repository. It returns the commit
// and its unix timestamp, or nil when git fails, the output is unparseable, or the
// repo has no commit matching the filter — all of which are ordinary for a scanned
// folder that turned out not to be a git repository.
func recentCommitFor(ctx context.Context, repoPath, filterAuthor string) (*RecentCommit, int64) {
	args := []string{
		"log", "-1",
		"--pretty=format:%H%n%an%n%at%n%s%n%D",
	}
	if filterAuthor != "" {
		args = append(args, "--author="+filterAuthor)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, 0
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 4 {
		return nil, 0
	}
	ts, _ := strconv.ParseInt(lines[2], 10, 64)
	if ts == 0 {
		return nil, 0
	}
	branch := ""
	if len(lines) >= 5 {
		branch = extractBranch(lines[4])
	}
	return &RecentCommit{
		Hash:    lines[0],
		Time:    time.Unix(ts, 0).Format("2006-01-02 15:04:05"),
		Author:  lines[1],
		Message: lines[3],
		Repo:    repoPath,
		Branch:  branch,
	}, ts
}

// GetRecentCommit queries the most recent commit across all repositories.
func GetRecentCommit(repoPaths []string, filterAuthor string) (*RecentCommit, error) {
	if err := ValidateAuthor(filterAuthor); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), recentCommitBudget)
	defer cancel()

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		best   *RecentCommit
		bestTS int64
		sem    = make(chan struct{}, recentCommitConcurrency)
		ran    int
	)

	for _, repoPath := range repoPaths {
		// Stop launching once the budget is spent; anything already in flight is
		// bounded by the same context.
		if ctx.Err() != nil {
			break
		}
		ran++
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }()
			commit, ts := recentCommitFor(ctx, path, filterAuthor)
			if commit == nil {
				return
			}
			mu.Lock()
			if best == nil || ts > bestTS {
				best, bestTS = commit, ts
			}
			mu.Unlock()
		}(repoPath)
	}
	wg.Wait()

	if ran < len(repoPaths) {
		log.Printf("stats: recent-commit scan stopped after %d/%d repos (budget %s reached)",
			ran, len(repoPaths), recentCommitBudget)
	}
	return best, nil
}

// maxCommitScan bounds a full-history search. Pagination needs the true total,
// so a search cannot stop at the requested page size; 20k commits per repo is
// far beyond any checkout this product has seen while keeping a runaway
// history from turning one keystroke into an unbounded git read.
const maxCommitScan = 20000

// readCommits reads at most maxPerRepo newest commits from each repository.
// Kept separate from the sorting/pagination so both the fixed-size "recent"
// feed and the searchable, paginated log parse git output exactly once.
func readCommits(repoPaths []string, filterAuthor string, maxPerRepo int) []RecentCommit {
	var all []RecentCommit
	// Field separator: NUL byte, which cannot appear inside git's text fields.
	const sep = "\x00"

	for _, repoPath := range repoPaths {
		args := []string{
			"log", "-" + strconv.Itoa(maxPerRepo),
			// The NUL separator must reach git as the FORMAT SPECIFIER %x00 —
			// a literal NUL byte inside an argv string fails at execve (POSIX
			// argv is NUL-terminated), so every command errored and the commit
			// list was silently empty for every project. %n (as GetLastCommit
			// uses) cannot be used here: commit subjects may contain newlines.
			"--pretty=format:%H%x00%an%x00%at%x00%s%x00%D",
		}
		if filterAuthor != "" {
			args = append(args, "--author="+filterAuthor)
		}

		ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout)
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repoPath
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, sep, 5)
			if len(parts) < 4 {
				continue
			}
			ts, _ := strconv.ParseInt(parts[2], 10, 64)
			if ts == 0 {
				continue
			}
			branch := ""
			if len(parts) >= 5 {
				branch = extractBranch(parts[4])
			}
			all = append(all, RecentCommit{
				Hash:    parts[0],
				Time:    time.Unix(ts, 0).Format("2006-01-02 15:04:05"),
				Author:  parts[1],
				Message: parts[3],
				Repo:    repoPath,
				Branch:  branch,
			})
		}
	}
	return all
}

// GetRecentCommits returns the most recent commits across the given repositories,
// merged and sorted by time descending, capped at limit. The author filter is
// optional; an empty filter returns commits from all authors.
func GetRecentCommits(repoPaths []string, filterAuthor string, limit int) ([]RecentCommit, error) {
	if err := ValidateAuthor(filterAuthor); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	all := readCommits(repoPaths, filterAuthor, limit)
	sort.Slice(all, func(i, j int) bool {
		return parseTimestamp(all[i].Time) > parseTimestamp(all[j].Time)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// SearchCommits filters commit subjects, authors, hashes and repository paths
// by a case-insensitive substring, then returns one page and the full match
// count. Filtering happens here rather than with `git log --grep` because --grep
// only sees the subject: an author name or a 7-character hash must be searchable
// too, and OR-ing --grep/--author in git is not possible.
func SearchCommits(repoPaths []string, query string, offset, limit int) ([]RecentCommit, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxCommitScan {
		limit = maxCommitScan
	}
	if offset < 0 {
		offset = 0
	}
	q := strings.ToLower(strings.TrimSpace(query))
	all := readCommits(repoPaths, "", maxCommitScan)
	if q != "" {
		matched := make([]RecentCommit, 0, len(all))
		for _, c := range all {
			if strings.Contains(strings.ToLower(c.Message), q) ||
				strings.Contains(strings.ToLower(c.Author), q) ||
				strings.Contains(strings.ToLower(c.Hash), q) ||
				strings.Contains(strings.ToLower(c.Repo), q) ||
				strings.Contains(strings.ToLower(c.Branch), q) {
				matched = append(matched, c)
			}
		}
		all = matched
	}
	sort.Slice(all, func(i, j int) bool {
		return parseTimestamp(all[i].Time) > parseTimestamp(all[j].Time)
	})
	total := len(all)
	if offset >= total {
		return []RecentCommit{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}
