// Package review implements the incremental commit review designed in
// ADR-0053: deterministic rules screen candidates, and only candidates are
// ever sent to a model for a second opinion.
//
// The split matters because of what the rules are NOT: they are heuristics
// over text, not a compiler. Measured on one real repository, 38 of 113
// Feign methods looked wrong to a regex while only some of them are actually
// wrong. So a rule never decides — it produces a candidate with an honest
// verdict, and the verdict is what the report and the notifier trust.
package review

import (
	"fmt"
	"sort"
	"strings"
)

// Severity is how much attention a finding deserves. It is a string, not an
// int, because it is persisted verbatim and read by a notification threshold
// ("only high"), so the stored value should be greppable in the database.
type Severity string

const (
	// SeverityHigh means "this breaks at runtime and no compiler says so".
	SeverityHigh Severity = "high"
	// SeverityMedium means "this is plausibly wrong; a human should look".
	SeverityMedium Severity = "medium"
	// SeverityLow is reserved for rules that are informational by nature.
	SeverityLow Severity = "low"
)

// Verdict records how much of the finding is established.
type Verdict string

const (
	// VerdictConfirmed is only used by rules with no known false-positive
	// boundary — a compiler would catch these if it could. It is the only
	// verdict the notifier is allowed to push (ADR-0053 D7).
	VerdictConfirmed Verdict = "confirmed"
	// VerdictUnverified means a deterministic rule fired but the pattern has
	// a legitimate reading (a Feign method whose provider takes @RequestParam,
	// a swallowed exception in idempotent cleanup). A model may later promote
	// it to confirmed or demote it to dismissed.
	VerdictUnverified Verdict = "unverified"
	// VerdictDismissed is written by the model review pass (P2) when the
	// candidate does not hold up. Rule-only runs never emit it.
	VerdictDismissed Verdict = "dismissed"
)

// Finding is one candidate produced by one rule against one file.
type Finding struct {
	// RepoPath, FilePath and CommitSHA locate the finding. The rule layer
	// leaves them empty — a rule sees one file and cannot know which
	// repository it arrived from — and the service fills them in while it
	// walks the repositories. They are here because every renderer needs
	// the location, including the ones that never touch the database: a
	// dry-run preview without file paths is not a preview (the CLI and the
	// MCP both returned empty ones until 2026-11-09).
	RepoPath  string
	FilePath  string
	CommitSHA string
	// Rule is the rule id, e.g. "feign_missing_body". It is also the key the
	// per-rule on/off config uses, so it must stay stable.
	Rule string
	// Severity is already adjusted for the hit (a rule may return medium for
	// a single-parameter method and high for a multi-parameter one).
	Severity Severity
	// Verdict is the rule's own confidence, before any model review.
	Verdict Verdict
	// Line is the 1-based line in the NEW file. Zero means "file-level".
	Line int
	// Evidence is the changed line(s) plus the smallest context that makes
	// the finding readable without opening the file. Truncated.
	Evidence string
	// Message is one sentence: what is wrong.
	Message string
	// Suggestion is one sentence: what to do about it. May be empty — an
	// empty suggestion is better than a wrong one.
	Suggestion string
}

// File is everything a rule is allowed to look at, plus the boundary that
// makes the review incremental: Added is the set of line numbers this commit
// introduced.
//
// A rule must only report on lines in Added (or on a declaration that spans
// one of them). Reporting pre-existing code would turn the daily report into
// a backlog dump, which is exactly what ADR-0053 D1 rejects.
type File struct {
	// Path is repo-relative and slash-separated.
	Path string
	// Key is the encoded cross-repository key used to exclude the current file from lookups.
	Key string
	// Content is the whole file as of that commit ("" for deletions).
	Content string
	// Added maps a 1-based new-file line number to its text (without the
	// leading '+'), for every line the commit added.
	Added map[int]string
	// Removed holds the deleted lines, without their leading '-'. Rules that
	// compare an old contract to a new one need them.
	Removed []string
	// Status is "added", "modified", "deleted" or "renamed".
	Status string
	// Lines is Content split once, so every rule does not re-split it.
	lines []string
}

// Lines returns the file content split into lines, memoised. Index i is
// line i+1 — callers use 1-based line numbers throughout.
func (f *File) Lines() []string {
	if f.lines == nil {
		f.lines = strings.Split(f.Content, "\n")
	}
	return f.lines
}

// Line returns the text of a 1-based line, or "" when out of range.
func (f *File) Line(n int) string {
	ls := f.Lines()
	if n < 1 || n > len(ls) {
		return ""
	}
	return ls[n-1]
}

// AddedLines returns the added line numbers in ascending order. Rules iterate
// it instead of the map so output is deterministic — the same commit must
// produce the same findings in the same order on every run, otherwise the
// report is not reviewable.
func (f *File) AddedLines() []int {
	out := make([]int, 0, len(f.Added))
	for n := range f.Added {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// Touches reports whether any added line falls in [from, to]. It is the
// incremental gate for declaration-level rules: a Feign method is reported
// only when the commit actually changed it.
func (f *File) Touches(from, to int) bool {
	if to < from {
		return false
	}
	for n := range f.Added {
		if n >= from && n <= to {
			return true
		}
	}
	return false
}

// Repo is the sliver of the repository a rule may consult beyond the changed
// file. Two rules (route renames, arity changes) need it because the breakage
// is on the OTHER side of a contract, not in the changed file.
//
// It is an interface, and the tests pass a map-backed fake, so rules stay
// pure functions of (file, repo) with fixtures instead of fixture repos.
type Repo interface {
	// GrepJava returns the repo-relative paths of .java files containing the
	// literal string. Results must be deterministic (sorted).
	GrepJava(literal string) []string
	// Read returns a repo-relative file's content, and whether it exists.
	Read(path string) (string, bool)
}

// Rule is one deterministic check.
type Rule interface {
	// ID is stable: it is stored in review_findings.rule and used as the
	// config key that disables the rule.
	ID() string
	// Check returns findings for one file. Implementations must not panic on
	// malformed input (a source file is not a parse tree): recover-worthy
	// assumptions belong in tests, not in production paths.
	Check(f *File, repo Repo) []Finding
}

// evidence renders a finding's evidence block from a line range: the added
// line itself, marked, with at most context lines around it. Line-based and
// truncated so a minified file cannot blow up the database row.
func evidence(f *File, line, context int) string {
	if line < 1 {
		return clip(strings.Join(f.Removed, "\n"), 400)
	}
	var b strings.Builder
	from, to := line-context, line+context
	if from < 1 {
		from = 1
	}
	for n := from; n <= to; n++ {
		text := f.Line(n)
		if n > len(f.Lines()) {
			break
		}
		switch {
		case n == line:
			b.WriteString("> ")
		case f.Added[n] != "":
			b.WriteString("+ ")
		default:
			b.WriteString("  ")
		}
		b.WriteString(strings.TrimRight(text, "\r"))
		b.WriteByte('\n')
	}
	return clip(strings.TrimRight(b.String(), "\n"), 400)
}

// clip shortens s to at most n bytes on a rune boundary. Evidence lands in a
// TEXT column and is rendered in a table cell; there is no value in storing
// a whole 4000-line file.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !isRuneStart(cut[len(cut)-1]) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

// isRuneStart reports whether b can be the last byte of a faithful cut: a
// byte that is not a UTF-8 continuation byte (0b10xxxxxx).
func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// oneLine collapses a message/suggestion to a single line — they are rendered
// in tables and notifications, where a newline silently eats the rest.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// finding is the constructor every rule uses, so the invariants (one-line
// message, clipped evidence, non-empty rule id) hold in one place.
func finding(f *File, rule string, sev Severity, v Verdict, line int, msg, suggestion string) Finding {
	return Finding{
		Rule:       rule,
		Severity:   sev,
		Verdict:    v,
		Line:       line,
		Evidence:   evidence(f, line, 2),
		Message:    oneLine(msg),
		Suggestion: oneLine(suggestion),
	}
}

// String is for logs and test failure messages.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d %s/%s %s", f.Rule, f.Line, f.Severity, f.Verdict, f.Message)
}
