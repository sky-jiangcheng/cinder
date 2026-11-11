package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// reviewSchema is migration v30 (ADR-0053 D4). A review report is data, not a
// note (D6): it is machine-produced, it is regenerated daily, and mixing it
// into project_notes would both drown the user's own notes and destroy the
// meaning of "my notes". Two tables, one run row + N finding rows.
var reviewSchema = []string{
	`CREATE TABLE IF NOT EXISTS review_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_date DATE NOT NULL,
		scope TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed')),
		started_at DATETIME NOT NULL,
		finished_at DATETIME,
		repos INTEGER NOT NULL DEFAULT 0,
		commits INTEGER NOT NULL DEFAULT 0,
		files INTEGER NOT NULL DEFAULT 0,
		candidates INTEGER NOT NULL DEFAULT 0,
		findings INTEGER NOT NULL DEFAULT 0,
		skipped INTEGER NOT NULL DEFAULT 0,
		ai_calls INTEGER NOT NULL DEFAULT 0,
		error TEXT,
		UNIQUE (run_date, scope)
	)`,
	// The unique key is the idempotency mechanism: a re-run replaces the run
	// row's contents instead of appending a second report for the same day.
	`CREATE INDEX IF NOT EXISTS idx_review_runs_started ON review_runs(started_at DESC)`,
	`CREATE TABLE IF NOT EXISTS review_findings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id INTEGER NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
		repo_path TEXT NOT NULL,
		commit_sha TEXT NOT NULL DEFAULT '',
		file_path TEXT NOT NULL,
		line INTEGER NOT NULL DEFAULT 0,
		rule TEXT NOT NULL,
		severity TEXT NOT NULL,
		verdict TEXT NOT NULL,
		evidence TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		suggestion TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_review_findings_run ON review_findings(run_id, severity, id)`,
}

// EnsureReviewSchema is migration v30. Additive and transactional, like the
// chat schema: nothing is backfilled because there is nothing to backfill.
func EnsureReviewSchema(database *sql.DB) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range reviewSchema {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create review schema: %w", err)
		}
	}
	return tx.Commit()
}

// ReviewStatus values.
const (
	ReviewStatusRunning = "running"
	ReviewStatusDone    = "done"
	ReviewStatusFailed  = "failed"
)

// ReviewRun is one day's review of one scope.
type ReviewRun struct {
	ID         int64  `json:"id"`
	RunDate    string `json:"run_date"` // YYYY-MM-DD, the day being reviewed
	Scope      string `json:"scope"`    // "collected" or "project:3,7"
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Repos      int    `json:"repos"`
	Commits    int    `json:"commits"`
	Files      int    `json:"files"`
	Candidates int    `json:"candidates"`
	Findings   int    `json:"findings"`
	Skipped    int    `json:"skipped"`
	AICalls    int    `json:"ai_calls"`
	Error      string `json:"error,omitempty"`
}

// ReviewFinding is one stored candidate.
type ReviewFinding struct {
	ID         int64  `json:"id"`
	RunID      int64  `json:"run_id"`
	RepoPath   string `json:"repo_path"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
	Rule       string `json:"rule"`
	Severity   string `json:"severity"`
	Verdict    string `json:"verdict"`
	Evidence   string `json:"evidence,omitempty"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

const reviewRunColumns = `id, run_date, scope, status, started_at, COALESCE(finished_at, ''),
	repos, commits, files, candidates, findings, skipped, ai_calls, COALESCE(error, '')`

func scanReviewRun(row interface{ Scan(...any) error }) (*ReviewRun, error) {
	var r ReviewRun
	if err := row.Scan(&r.ID, &r.RunDate, &r.Scope, &r.Status, &r.StartedAt, &r.FinishedAt,
		&r.Repos, &r.Commits, &r.Files, &r.Candidates, &r.Findings, &r.Skipped, &r.AICalls, &r.Error); err != nil {
		return nil, err
	}
	return &r, nil
}

// StartReviewRun inserts (or resets) the run row for a date+scope and returns
// its id. An existing 'done' row is only reused when force is true — the
// caller has already decided that; anything else (running/failed) is reset,
// because a crashed run must not block the day.
//
// Findings of the previous run are deleted in the same transaction, so a
// re-run can never double-report (the acceptance criterion in ADR-0053).
func StartReviewRun(database *sql.DB, runDate, scope string, force bool) (int64, error) {
	tx, err := database.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var (
		id     int64
		status string
	)
	err = tx.QueryRow(`SELECT id, status FROM review_runs WHERE run_date = ? AND scope = ?`,
		runDate, scope).Scan(&id, &status)
	switch {
	case err == sql.ErrNoRows:
		res, err := tx.Exec(`INSERT INTO review_runs (run_date, scope, status, started_at)
			VALUES (?, ?, ?, ?)`, runDate, scope, ReviewStatusRunning, time.Now().Format(time.RFC3339))
		if err != nil {
			return 0, err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	case err != nil:
		return 0, err
	case status == ReviewStatusDone && !force:
		return 0, ErrReviewAlreadyDone
	default:
		if _, err := tx.Exec(`DELETE FROM review_findings WHERE run_id = ?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE review_runs SET status = ?, started_at = ?, finished_at = NULL,
			repos = 0, commits = 0, files = 0, candidates = 0, findings = 0, skipped = 0,
			ai_calls = 0, error = NULL WHERE id = ?`,
			ReviewStatusRunning, time.Now().Format(time.RFC3339), id); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// ErrReviewAlreadyDone is returned by StartReviewRun when today's report for
// this scope already completed and force was not set. The caller turns it into
// "already reviewed today" rather than an error the user must understand.
var ErrReviewAlreadyDone = fmt.Errorf("review already completed for this date and scope")

// FinishReviewRun stamps a run as done with its counters. A non-empty
// run.Error is written to the existing error column while the status stays
// done, so partial model failures are visible without a new status value.
func FinishReviewRun(database *sql.DB, run *ReviewRun) error {
	var runErr any
	if run.Error != "" {
		runErr = run.Error
	}
	_, err := database.Exec(`UPDATE review_runs SET status = ?, finished_at = ?,
		repos = ?, commits = ?, files = ?, candidates = ?, findings = ?, skipped = ?, ai_calls = ?, error = ?
		WHERE id = ?`,
		ReviewStatusDone, time.Now().Format(time.RFC3339),
		run.Repos, run.Commits, run.Files, run.Candidates, run.Findings, run.Skipped, run.AICalls, runErr, run.ID)
	return err
}

// FailReviewRun records a failure. The row stays (status='failed'), which is
// both the debuggable history and the reason a same-day retry is allowed.
func FailReviewRun(database *sql.DB, id int64, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	_, err := database.Exec(`UPDATE review_runs SET status = ?, finished_at = ?, error = ? WHERE id = ?`,
		ReviewStatusFailed, time.Now().Format(time.RFC3339), msg, id)
	return err
}

// InsertReviewFindings writes one run's findings in a single transaction.
// A finding is never updated after the fact: the model review pass (P2)
// rewrites the run's findings wholesale, so there is one writer per run and
// no partial-update semantics to get wrong.
func InsertReviewFindings(database *sql.DB, findings []ReviewFinding) error {
	if len(findings) == 0 {
		return nil
	}
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO review_findings
		(run_id, repo_path, commit_sha, file_path, line, rule, severity, verdict, evidence, message, suggestion)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, f := range findings {
		if _, err := stmt.Exec(f.RunID, f.RepoPath, f.CommitSHA, f.FilePath, f.Line,
			f.Rule, f.Severity, f.Verdict, f.Evidence, f.Message, f.Suggestion); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetReviewRun looks up one run by date and scope.
func GetReviewRun(database *sql.DB, runDate, scope string) (*ReviewRun, error) {
	row := database.QueryRow(`SELECT `+reviewRunColumns+` FROM review_runs
		WHERE run_date = ? AND scope = ?`, runDate, scope)
	r, err := scanReviewRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// LatestReviewRun returns the most recent completed run, or nil.
func LatestReviewRun(database *sql.DB) (*ReviewRun, error) {
	row := database.QueryRow(`SELECT ` + reviewRunColumns + ` FROM review_runs
		WHERE status = 'done' ORDER BY run_date DESC, id DESC LIMIT 1`)
	r, err := scanReviewRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ReviewRunsInRange returns completed runs whose run_date falls in the given
// window, newest first. `review_latest {days}` (P3) reads through this.
func ReviewRunsInRange(database *sql.DB, since string) ([]ReviewRun, error) {
	rows, err := database.Query(`SELECT `+reviewRunColumns+` FROM review_runs
		WHERE status = 'done' AND run_date >= ? ORDER BY run_date DESC, id DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewRun
	for rows.Next() {
		r, err := scanReviewRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ReviewFindings returns one run's findings, optionally filtered by severity
// and verdict. Ordering is severity first (high, medium, low) then file path:
// the report is read top-down and the reader must see the worst thing first.
func ReviewFindings(database *sql.DB, runID int64, severities, verdicts []string) ([]ReviewFinding, error) {
	q := `SELECT id, run_id, repo_path, commit_sha, file_path, line, rule, severity, verdict,
		evidence, message, suggestion FROM review_findings WHERE run_id = ?`
	args := []any{runID}
	if len(severities) > 0 {
		q += ` AND severity IN (` + placeholders(len(severities)) + `)`
		for _, s := range severities {
			args = append(args, s)
		}
	}
	if len(verdicts) > 0 {
		q += ` AND verdict IN (` + placeholders(len(verdicts)) + `)`
		for _, v := range verdicts {
			args = append(args, v)
		}
	}
	q += ` ORDER BY CASE severity WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END, file_path, line`
	rows, err := database.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// A run with zero findings must serialize as [], never null: Go marshals
	// a nil slice as JSON null, and the desktop page reads findings.length —
	// a nil slice here turned "reviewed, nothing found" into a TypeError the
	// moment the page opened. Zero rows is still a LIST, so start from an
	// empty one and let append grow it.
	out := make([]ReviewFinding, 0)
	for rows.Next() {
		var f ReviewFinding
		if err := rows.Scan(&f.ID, &f.RunID, &f.RepoPath, &f.CommitSHA, &f.FilePath, &f.Line,
			&f.Rule, &f.Severity, &f.Verdict, &f.Evidence, &f.Message, &f.Suggestion); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// placeholders returns "?, ?, ?" for n arguments.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
