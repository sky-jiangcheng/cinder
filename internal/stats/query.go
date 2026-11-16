package stats

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// QueryStats runs git log --shortstat for the given repository, date, and optional author.
// Returns aggregated statistics. All user-supplied parameters are validated.
func QueryStats(repoPath, date, author string) (*Result, error) {
	if err := ValidateDate(date); err != nil {
		return nil, err
	}
	if err := ValidateAuthor(author); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), QueryTimeout)
	defer cancel()

	args := []string{
		"log",
		"--since=" + date + " 00:00:00",
		"--until=" + date + " 23:59:59",
		// No --first-parent on purpose (2026-11-16 fix): with it, git renders
		// each MERGE commit's diff against its first parent, and a routine
		// "merge the feature branch into release" day reported the whole
		// branch's line count (measured: 146 files / +5430 lines) as if the
		// person who pressed merge had written it. Plain `git log --shortstat`
		// omits merge diffs entirely — the same semantics QueryStatsRange
		// (the backfill path) has always used, and the two paths must agree
		// or the same day reads differently depending on which one wrote it.
		"--pretty=tformat:",
		"--shortstat",
	}

	if author != "" {
		args = append(args, "--author="+author)
	}

	//nolint:gosec // date and author are validated by ValidateDate/ValidateAuthor above
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("query timed out after %v", QueryTimeout)
		}
		return &Result{}, nil
	}

	result, perr := parseShortStat(string(out))
	if perr != nil {
		return nil, perr
	}

	// Count commits in the same window. The shortstat query above has no
	// per-commit output, so a separate rev-list count is required.
	// Two hard lessons from the 2026-11-16 fix:
	//   - `rev-list` REQUIRES a revision. Without one it exits 129 (usage),
	//     the error was swallowed by `countErr == nil`, and the commits column
	//     was silently 0 for every row this path wrote (69 of 4,975 in the
	//     live library — exactly the single-day refresh's footprint).
	//   - no --first-parent here either: `git log` (the range path, and the
	//     semantics this count must match) walks all parents and counts merge
	//     commits; --first-parent would count only the tip.
	countArgs := []string{
		"rev-list", "--count",
		"--since=" + date + " 00:00:00",
		"--until=" + date + " 23:59:59",
		"HEAD",
	}
	if author != "" {
		countArgs = append(countArgs, "--author="+author)
	}
	//nolint:gosec // same validated inputs as the query above
	countCmd := exec.CommandContext(ctx, "git", countArgs...)
	countCmd.Dir = repoPath
	countOut, countErr := countCmd.Output()
	if countErr != nil {
		// A repo with no commits at all is the legitimate failure (fresh
		// clone, unborn HEAD); everything else is a bug we must be able to
		// see. Log it — the swallowed 129 above hid for months precisely
		// because this branch was silent.
		log.Printf("stats commit count failed repo=%q date=%s: %v", repoPath, date, countErr)
	} else if n, nerr := strconv.Atoi(strings.TrimSpace(string(countOut))); nerr == nil {
		result.Commits = n
	}

	return result, nil
}

// parseShortStat parses git log --shortstat output into a Result.
func parseShortStat(output string) (*Result, error) {
	result := &Result{}
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if len(line) == 40 && isHex(line) {
			continue
		}

		files, added, deleted := parseStatLine(line)
		result.FilesChanged += files
		result.LinesAdded += added
		result.LinesDeleted += deleted
	}

	return result, nil
}

// parseStatLine parses a single shortstat line.
func parseStatLine(line string) (files, added, deleted int) {
	parts := strings.Split(line, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		fields := strings.Fields(part)

		if len(fields) < 2 {
			continue
		}

		val, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		keyword := fields[1]
		switch {
		case strings.HasPrefix(keyword, "file"):
			files = val
		case strings.HasPrefix(keyword, "insertion"):
			added = val
		case strings.HasPrefix(keyword, "deletion"):
			deleted = val
		}
	}
	return
}
