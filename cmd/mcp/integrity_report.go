package main

import (
	"fmt"
	"sort"
	"strings"

	"repotory/internal/integrity"
	"repotory/internal/service"
	"repotory/internal/version"
)

// runIntegrityReport renders internal/integrity's Report as the plain-text
// receipt the other MCP tools return.
//
// Two rules govern the wording, because this output is read by an agent that
// will take it at face value:
//
//   - A failing check must never be softened. "warn" and "fail" stay distinct,
//     and the check's own Detail is passed through verbatim rather than
//     re-summarised, so the receipt cannot end up describing something the
//     metrics contradict.
//   - A check that errored is reported as errored. It is neither a pass nor a
//     failure, and the aggregate score already accounts for it as a lower
//     bound rather than pretending the run was clean.
func runIntegrityReport(svc *service.Service) string {
	// Deliberately via the service rather than reaching for its *sql.DB: ADR-0005
	// makes service the only layer allowed to touch the database, and handing out
	// the handle would turn every caller into an eleventh unchecked consumer.
	report := svc.RunIntegrityChecks()

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Cinder Data Integrity (v%s) ===\n\n", version.Version)
	fmt.Fprintf(&sb, "Trust score: %.0f/100 — %s\n\n", report.Score, report.Verdict)

	for _, c := range report.Checks {
		fmt.Fprintf(&sb, "%s  %s\n", statusMark(c.Status), c.Title)
		if c.Detail != "" {
			fmt.Fprintf(&sb, "      %s\n", c.Detail)
		}
		if c.Error != "" {
			fmt.Fprintf(&sb, "      error: %s\n", c.Error)
		}
		if len(c.Offenders) > 0 {
			fmt.Fprintf(&sb, "      offending row ids: %s\n", joinIDs(c.Offenders))
		}
		if len(c.Metrics) > 0 {
			fmt.Fprintf(&sb, "      metrics: %s\n", formatMetrics(c.Metrics))
		}
		sb.WriteString("\n")
	}

	fmt.Fprintf(&sb, "%d checks: %d passed, %d warned, %d failed, %d errored\n",
		report.Total, report.Passed, report.Warned, report.Failed, report.Errored)

	// Only call the base trustworthy when nothing is wrong. A report that
	// merely "mostly passed" is exactly the sentence an agent would quote back
	// as reassurance, so the closing line has to match the numbers above it.
	switch {
	case report.Failed == 0 && report.Errored == 0 && report.Warned == 0:
		fmt.Fprintf(&sb, "\nData is consistent; every check passed.\n")
	case report.Failed == 0 && report.Errored == 0:
		fmt.Fprintf(&sb, "\nNo failures, but %d check(s) raised warnings — read them before trusting derived answers.\n", report.Warned)
	case report.Errored > 0:
		fmt.Fprintf(&sb, "\n%d check(s) could not complete, so this score is a lower bound: treat the unverified areas as unknown, not as passing.\n", report.Errored)
	default:
		fmt.Fprintf(&sb, "\n%d check(s) FAILED. Do not present knowledge from this database as accurate until they are fixed or the affected data is re-scanned.\n", report.Failed)
	}

	return sb.String()
}

// statusMark renders a status as a leading marker. The glyph set is
// deliberately distinct per state so the level survives being read aloud or
// truncated in a terminal.
func statusMark(s integrity.Status) string {
	switch s {
	case integrity.StatusPass:
		return "✅"
	case integrity.StatusWarn:
		return "⚠️ "
	case integrity.StatusFail:
		return "❌"
	default:
		return "⛔"
	}
}

func joinIDs(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return strings.Join(parts, ", ")
}

// formatMetrics sorts the keys so two runs over an unchanged database produce
// byte-identical output — a receipt that reshuffles itself between calls is
// hard to diff and easy to misread.
func formatMetrics(m map[string]int64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}
