package review

import (
	"strconv"
	"strings"
)

// Diff parsing lives here as a pure function so that the interesting part of
// the review pipeline — "which lines did this commit add" — is testable with a
// string literal instead of a fixture repository.

// Status values for FileChange.Status. They mirror `git diff`'s vocabulary
// rather than a private enum, because they are also shown in the report.
const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
	StatusRenamed  = "renamed"
)

// DiffLine is one line inside a hunk. Kind is the patch prefix: ' ' context,
// '+' addition, '-' removal. Text is the line with the prefix stripped.
type DiffLine struct {
	Kind byte
	Text string
}

// Hunk is one '@@' block.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	// Header is the text after the closing '@@', which git fills with the
	// enclosing declaration ("@@ -40,7 +40,9 @@ public class Foo"). Rules do
	// not depend on it, but the report shows it because it saves opening the
	// file to know where the hunk is.
	Header string
	Lines  []DiffLine
}

// FileChange is one file's worth of patch.
type FileChange struct {
	// Path is the new path (the old one for a deletion), repo-relative.
	Path string
	// OldPath differs from Path only for renames.
	OldPath string
	Status  string
	Hunks   []Hunk
	// Binary is set for images, jars etc. Binary files are skipped: no rule
	// can say anything about them and the line numbers do not exist.
	Binary bool
}

// Added returns the added lines as a 1-based new-file line number → text map.
// This is the incremental boundary every rule keys off.
func (fc FileChange) Added() map[int]string {
	out := map[int]string{}
	for _, h := range fc.Hunks {
		n := h.NewStart
		for _, l := range h.Lines {
			switch l.Kind {
			case '+':
				out[n] = l.Text
				n++
			case ' ':
				n++
			}
		}
	}
	return out
}

// Removed returns the deleted lines in order.
func (fc FileChange) Removed() []string {
	var out []string
	for _, h := range fc.Hunks {
		for _, l := range h.Lines {
			if l.Kind == '-' {
				out = append(out, l.Text)
			}
		}
	}
	return out
}

// ChangedLines counts additions plus removals, which is what the
// `review.max_diff_lines` budget is expressed in (ADR-0053 D5).
func (fc FileChange) ChangedLines() int {
	n := 0
	for _, h := range fc.Hunks {
		for _, l := range h.Lines {
			if l.Kind == '+' || l.Kind == '-' {
				n++
			}
		}
	}
	return n
}

// ParseUnifiedDiff parses `git diff` / `git show` output into per-file
// changes. It accepts the subset git actually emits for text files; anything
// it does not understand is ignored rather than treated as an error, because
// a patch that fails to parse in one hunk should cost that hunk, not the whole
// day's review.
func ParseUnifiedDiff(text string) []FileChange {
	var (
		out  []FileChange
		cur  *FileChange
		hunk *Hunk
	)
	lines := strings.Split(text, "\n")
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			if cur != nil {
				out = append(out, *cur)
			}
			a, b := splitGitHeader(strings.TrimPrefix(line, "diff --git "))
			cur = &FileChange{Path: stripPrefix(b), OldPath: stripPrefix(a), Status: StatusModified}
			hunk = nil

		case cur == nil:
			// Preamble before the first `diff --git` (commit headers when
			// --format is empty) carries nothing we need.

		case strings.HasPrefix(line, "new file mode"):
			cur.Status = StatusAdded

		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = StatusDeleted

		case strings.HasPrefix(line, "rename from "):
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
			cur.Status = StatusRenamed

		case strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
			cur.Status = StatusRenamed

		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			cur.Binary = true

		case strings.HasPrefix(line, "--- "):
			// `--- /dev/null` marks an addition; the path itself is already
			// known from the `diff --git` header, which is more reliable for
			// renames (it names both sides).
			if strings.HasPrefix(line, "--- /dev/null") {
				cur.Status = StatusAdded
			}

		case strings.HasPrefix(line, "+++ "):
			if strings.HasPrefix(line, "+++ /dev/null") {
				cur.Status = StatusDeleted
			}

		case strings.HasPrefix(line, "@@"):
			h := parseHunkHeader(line)
			if h == nil {
				hunk = nil
				continue
			}
			cur.Hunks = append(cur.Hunks, *h)
			hunk = &cur.Hunks[len(cur.Hunks)-1]

		case hunk != nil && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") ||
			strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\\")):
			if strings.HasPrefix(line, "\\") {
				continue // "\ No newline at end of file"
			}
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: line[0], Text: line[1:]})
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	// A rename with no content change produces no hunks; keep it anyway, the
	// rules that care about contract paths need to see the move.
	return out
}

// splitGitHeader splits `a/x b/y`, tolerating paths with spaces by splitting
// on the ` b/` boundary that git itself guarantees for the common case.
func splitGitHeader(s string) (string, string) {
	if i := strings.Index(s, " b/"); i > 0 {
		return s[:i], s[i+1:]
	}
	fields := strings.Fields(s)
	if len(fields) == 2 {
		return fields[0], fields[1]
	}
	return s, s
}

// stripPrefix removes git's a/ and b/ prefixes. It deliberately does not
// unquote C-style escaped paths ("a/caf\303\251.txt"): such a file gets an
// unreadable Path, the rule that reads it finds nothing, and the review is
// quiet rather than wrong.
func stripPrefix(p string) string {
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// parseHunkHeader reads `@@ -oldStart,oldCount +newStart,newCount @@ header`.
// Counts default to 1 when omitted, which is what git does.
func parseHunkHeader(line string) *Hunk {
	body := strings.TrimPrefix(line, "@@")
	end := strings.Index(body, "@@")
	if end < 0 {
		return nil
	}
	header := strings.TrimSpace(body[end+2:])
	fields := strings.Fields(strings.TrimSpace(body[:end]))
	if len(fields) < 2 {
		return nil
	}
	h := &Hunk{Header: header}
	var ok bool
	if h.OldStart, h.OldCount, ok = parseRange(strings.TrimPrefix(fields[0], "-")); !ok {
		return nil
	}
	if h.NewStart, h.NewCount, ok = parseRange(strings.TrimPrefix(fields[1], "+")); !ok {
		return nil
	}
	return h
}

// parseRange parses "start,count" or "start".
func parseRange(s string) (int, int, bool) {
	start, count := s, "1"
	if i := strings.IndexByte(s, ','); i >= 0 {
		start, count = s[:i], s[i+1:]
	}
	a, err := strconv.Atoi(start)
	if err != nil {
		return 0, 0, false
	}
	b, err := strconv.Atoi(count)
	if err != nil {
		return 0, 0, false
	}
	return a, b, true
}
