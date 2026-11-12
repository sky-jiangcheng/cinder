// Package integrity answers the question nothing else in Cinder can answer:
// how much of the knowledge base can be trusted?
//
// The product promise is a structured, searchable, trustworthy model of the
// local Git projects, and an AI is the main consumer of that model. Nothing on
// the write path can tell us whether the model still matches reality. The FTS5
// index can drift away from project_notes, after which search silently returns
// fewer hits than there are notes and no query anywhere reports an error. A
// database can sit several migrations behind the binary that is reading it. The
// repo_meta knowledge cache the AI quotes can be months old and still be served
// as if it were current. ADR 0006 froze the scope and TODO.md lists the
// remaining work, but decisions are not evidence. This package is the data-side
// receipt that the model was actually verified - the counterpart of archify's
// artifact checks, which have no database equivalent.
//
// Contract for every check in this package:
//
//   - Read-only. Only Query and QueryRow are used: no Exec, no transaction, no
//     PRAGMA. A report can therefore be produced while a scan is writing to the
//     same file, and this package can never repair - or damage - what it
//     inspects. TestSourceIsReadOnly enforces it by walking the sources.
//   - Never fatal. A query error, a missing table or a panic becomes a
//     StatusError entry for that single check, so one broken check can never
//     hide the other five results.
//   - Empty-database safe. A fresh install has no projects, no notes and no
//     indexes; every check must answer "pass" or "warn" there, never crash and
//     never claim more than it knows.
//   - Existence is not evidence. The failure mode this package exists to catch
//     is the one that looks healthy: a version stamp that lies about the
//     columns behind it, an index rowid that hides stale terms, a cache row
//     holding nothing but column defaults. A rowid count, a stored version
//     number or a row count are all satisfied by those states, so every check
//     verifies the thing the number stands for, not the number.
package integrity

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"repotory/internal/errcode"
)

// Status is the outcome of a single check.
type Status string

// Check outcomes, ordered from healthiest to least useful. "warn" means the
// check ran and found something worth a human's attention; "fail" means the
// stored data is wrong in a way that will mislead the user or the AI; "error"
// means the check itself could not run, which is never the same as "clean" and
// must never be counted as a pass.
const (
	StatusPass  Status = "pass"
	StatusWarn  Status = "warn"
	StatusFail  Status = "fail"
	StatusError Status = "error"
)

// Stable check names, exported so callers (a settings page, a CLI, tests) can
// address one check without duplicating a string literal.
const (
	CheckNameSchemaVersion = "schema_version"
	CheckNameFTSIndex      = "fts_index_integrity"
	CheckNameOrphans       = "orphan_rows"
	CheckNameCoverage      = "scan_coverage"
	CheckNameCacheFresh    = "knowledge_cache_freshness"
	CheckNameNoteVersions  = "note_version_orphans"
)

// maxOffenderSamples caps how many ids a Detail line and the Offenders slice
// carry. A database that drifted years ago can hold thousands of bad ids; the
// counts in Metrics stay exact while the sample stays readable.
const maxOffenderSamples = 10

// Check is the structured result of one integrity check: a stable machine name,
// a human title, a status, one sentence of evidence that means something
// without reading SQL, and the raw numbers behind that sentence.
type Check struct {
	Name    string           `json:"name"`
	Title   string           `json:"title"`
	Status  Status           `json:"status"`
	Detail  string           `json:"detail"`
	Metrics map[string]int64 `json:"metrics,omitempty"`
	// Offenders lists a sample of the offending row ids (notes, repositories,
	// FTS rowids, ...) so a repair tool can act on the report without a
	// second query. Empty when the check found nothing or when the offending
	// ids are not row ids (see CheckKnowledgeCacheFreshness).
	Offenders []int64 `json:"offenders,omitempty"`
	// Error carries the driver or panic message behind StatusError. Kept
	// separate from Detail so the human sentence stays readable.
	Error string `json:"error,omitempty"`
}

// Metric returns one machine-readable number by name, and whether it was
// reported at all. Callers must treat a missing metric as "not measured" rather
// than zero: a check only publishes the metrics that mean something for its
// own verdict.
func (c Check) Metric(name string) (int64, bool) {
	v, ok := c.Metrics[name]
	return v, ok
}

// Report is the aggregate of every check plus the totals a caller needs to turn
// the result into a single trust percentage.
type Report struct {
	Checks  []Check `json:"checks"`
	Total   int     `json:"total"`
	Passed  int     `json:"passed"`
	Warned  int     `json:"warned"`
	Failed  int     `json:"failed"`
	Errored int     `json:"errored"`
	// Score is a 0-100 trust percentage: a pass counts 1, a warning 0.5, a
	// failure or an error 0. A report containing an error is a lower bound on
	// the real score, not a clean bill of health.
	Score   float64 `json:"score"`
	Verdict string  `json:"verdict"`
}

// Check returns one check of the report by its stable name.
func (r *Report) Check(name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// RunAll executes every check and returns the aggregate report. The order is
// fixed and deliberately front-loaded: structural facts first (schema version,
// search index), then referential facts, then the coverage and freshness
// numbers. A reader who only looks at the first lines still sees the two
// problems that silently corrupt everything downstream.
//
// Every check is independently guarded, so a nil handle, a missing table or a
// panic is reported as that check's error status and the remaining checks still
// run.
func RunAll(db *sql.DB) *Report {
	report := &Report{Checks: []Check{
		CheckSchemaVersion(db),
		CheckFTSIndexIntegrity(db),
		CheckOrphanRows(db),
		CheckScanCoverage(db),
		CheckKnowledgeCacheFreshness(db),
		CheckNoteVersionOrphans(db),
	}}
	report.summarize()
	return report
}

// summarize fills in the counts, the score and the one-line verdict. It is a
// pure function of the collected checks so the math can be tested without a
// database.
func (r *Report) summarize() {
	r.Total = len(r.Checks)
	weights := 0.0
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			r.Passed++
			weights++
		case StatusWarn:
			r.Warned++
			weights += 0.5
		case StatusFail:
			r.Failed++
		case StatusError:
			r.Errored++
		}
	}
	if r.Total == 0 {
		r.Score = 0
		r.Verdict = "no checks ran"
		return
	}
	r.Score = weights / float64(r.Total) * 100
	switch {
	case r.Failed == 0 && r.Errored == 0 && r.Warned == 0:
		r.Verdict = fmt.Sprintf("all %d checks pass: the knowledge base is consistent with the data behind it", r.Total)
	case r.Failed == 0 && r.Errored == 0:
		r.Verdict = fmt.Sprintf("%d/%d checks pass, %d warn: usable, but not fully verified", r.Passed, r.Total, r.Warned)
	case r.Errored > 0:
		// Never present a partial run as a good score: an errored check is an
		// unknown, and an unknown is not evidence of health.
		r.Verdict = fmt.Sprintf("%d/%d checks pass, %d fail, %d warn, %d could not run: the score is a lower bound", r.Passed, r.Total, r.Failed, r.Warned, r.Errored)
	default:
		r.Verdict = fmt.Sprintf("%d/%d checks pass, %d fail, %d warn: the knowledge base has integrity problems", r.Passed, r.Total, r.Failed, r.Warned)
	}
}

// checkFn is the body of one check, run inside guarded.
type checkFn func(*sql.DB) Check

// guarded turns a check body into a total function. Two failure modes are
// absorbed here, because a health report is exactly the code that must never
// crash the app: it is the code a user runs when something already feels wrong.
// A nil handle or a missing table reaches us as a driver error the check turns
// into a finding; a nil-pointer surprise inside a check reaches us as a panic.
func guarded(db *sql.DB, run checkFn, name, title string) (c Check) {
	if db == nil {
		return Check{
			Name:   name,
			Title:  title,
			Status: StatusError,
			Detail: "no database handle was passed to the check",
			Error:  "nil *sql.DB",
		}
	}
	defer func() {
		if r := recover(); r != nil {
			c = Check{
				Name:   name,
				Title:  title,
				Status: StatusError,
				Detail: "check panicked instead of returning a result",
				Error:  fmt.Sprintf("%v", r),
			}
		}
	}()
	return run(db)
}

// checkErr builds the error result for a check whose query could not run.
func checkErr(name, title, detail string, err error) Check {
	return Check{
		Name:   name,
		Title:  title,
		Status: StatusError,
		Detail: detail,
		Error:  err.Error(),
	}
}

// count runs a single-value aggregate. Scanning through sql.NullInt64 keeps the
// empty-database path safe: aggregates over zero rows are NULL, not an error.
func count(db *sql.DB, query string, args ...any) (int64, error) {
	var v sql.NullInt64
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		return 0, err
	}
	return v.Int64, nil
}

// sampleIDs collects up to limit ids, used to show *which* rows are wrong
// rather than only how many.
func sampleIDs(db *sql.DB, limit int, query string, args ...any) ([]int64, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// drift reports how many ids of table `from` have no counterpart in table
// `to`, plus a sample of them. Table names are always compile-time constants
// from the checks above, never user input, so interpolating them into SQL is
// safe; ids come from parameters.
func drift(db *sql.DB, from, to string) (int64, []int64, error) {
	n, err := count(db, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE id NOT IN (SELECT id FROM %s)", from, to))
	if err != nil {
		return 0, nil, err
	}
	if n == 0 {
		return 0, nil, nil
	}
	sample, err := sampleIDs(db, maxOffenderSamples,
		fmt.Sprintf("SELECT id FROM %s WHERE id NOT IN (SELECT id FROM %s) LIMIT %d", from, to, maxOffenderSamples))
	if err != nil {
		return n, nil, err
	}
	return n, sample, nil
}

// isMissingTable recognises the driver's "no such table" error so a check can
// report "the index was never created" as a finding about the database instead
// of an opaque error about a query. The shared predicate lives in errcode.
func isMissingTable(err error) bool {
	return errcode.IsMissingTable(err)
}

// isMissingColumn recognises the driver's "no such column" error. The schema
// shape probe uses it to turn a missing column into a finding about the
// database rather than into a check that could not run.
func isMissingColumn(err error) bool {
	return errcode.IsMissingColumn(err)
}

// formatIDs renders a sample for the human-readable Detail line.
func formatIDs(ids []int64, limit int) string {
	if len(ids) == 0 {
		return ""
	}
	shown := ids
	suffix := ""
	if len(shown) > limit {
		shown = shown[:limit]
		suffix = fmt.Sprintf(", +%d more", len(ids)-limit)
	}
	parts := make([]string, 0, len(shown))
	for _, id := range shown {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ", ") + suffix
}

// pct is an integer percentage that reports 0 for an empty denominator: no data
// is not 100% coverage, and dividing by zero would panic the health report.
func pct(part, whole int64) int64 {
	if whole <= 0 {
		return 0
	}
	return part * 100 / whole
}
