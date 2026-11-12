// Package reviewrun runs the incremental commit review (ADR-0053): resolve the
// repositories in scope, list the day's commits, hand each changed file to the
// rules, optionally ask a model to confirm or dismiss each candidate, and store
// the result.
//
// The pipeline lives here rather than on service.Service (ADR-0060, step one)
// because it needs nothing from the service but the database. The model pass is
// the one piece the service still owns — it holds the endpoint, the API key and
// the audit trail (ADR-0030) — and it arrives through the Refiner interface.
// That direction is deliberate: this package never imports internal/service, so
// there is no cycle to police.
package reviewrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/review"
)

// Options controls one run.
type Options struct {
	// Date is the UTC-local day to review. Zero means "yesterday".
	Date time.Time
	// Scope is "collected" or "project:<ids>". Empty reads the config.
	Scope string
	// ProjectIDs, when set, overrides Scope.
	ProjectIDs []int64
	// DryRun computes the report without writing anything.
	DryRun bool
	// Force re-runs a day that already completed.
	Force bool
	// NoAI forces the rule-only path regardless of config.
	NoAI bool
}

// Result is what every trigger returns (CLI, MCP tool, desktop button).
//
// The json tags are load-bearing: this struct crosses the Wails binding to the
// frontend and the RPC bridge to a browser, and Go's default field names are
// capitalised — `AlreadyDone` would arrive as `AlreadyDone`, so a frontend
// reading `already_done` would see undefined and report every idempotent call
// as a fresh run. Snake_case everywhere, matching db.ReviewRun.
type Result struct {
	Run *db.ReviewRun `json:"run"`
	// Findings are the candidates, in rule order, before storage.
	Findings []review.Finding `json:"findings"`
	// Stored is the persisted view (nil on a dry run). Same content, with the
	// run id attached.
	Stored []db.ReviewFinding `json:"stored"`
	// AlreadyDone is set when the day was reviewed before and Force was off.
	AlreadyDone bool `json:"already_done"`
	DryRun      bool `json:"dry_run"`
}

// Refiner is the model pass as the service exposes it: one call per chunk,
// answering with verdicts that index into the chunk they were handed.
//
// A Refiner may return ErrUnavailable to say "the model pass cannot run at all
// here" (no endpoint configured, no key). That is not a failure of the review —
// the rules' output is a complete report — so the caller logs it once and
// continues rule-only rather than counting a failed call per file.
type Refiner interface {
	Verdicts(ctx context.Context, path, content string, chunk []review.Finding) ([]Verdict, error)
}

// ErrUnavailable marks "the model pass is not configured", as opposed to "the
// call was attempted and failed". A refiner that cannot reach a model at all
// wraps it; refine then reports a rule-only run instead of N failed calls.
var ErrUnavailable = errors.New("model pass is not configured")

func isUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }

// Runner executes reviews against one database. Construct it with New; it holds
// no mutable state, so a fresh one per call is fine and avoids a cache that
// would have to be invalidated when the refiner changes.
type Runner struct {
	db      *sql.DB
	refiner Refiner
}

// New returns a Runner. A nil refiner means rules only, whatever the config
// says: the caller that cannot call a model must not pretend it might.
func New(database *sql.DB, refiner Refiner) *Runner {
	return &Runner{db: database, refiner: refiner}
}

// Run executes one incremental review. It is safe to call from the CLI, from
// the MCP server and from the desktop ticker: all side effects go through the
// database, and the unique (run_date, scope) key makes a concurrent second run
// fail fast instead of interleaving findings.
func (r *Runner) Run(ctx context.Context, opts Options) (*Result, error) {
	day := opts.Date
	if day.IsZero() {
		day = time.Now().AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	runDate := day.Format("2006-01-02")

	scope := strings.TrimSpace(opts.Scope)
	if len(opts.ProjectIDs) > 0 {
		scope = "project:" + joinIDs(opts.ProjectIDs)
	}
	if scope == "" {
		scope, _ = db.GetConfig(r.db, KeyScope)
		if strings.TrimSpace(scope) == "" {
			scope = DefaultScope
		}
	}

	repos, err := r.repos(scope)
	if err != nil {
		return nil, err
	}

	run := &db.ReviewRun{RunDate: runDate, Scope: scope, Status: db.ReviewStatusRunning}
	if !opts.DryRun {
		id, err := db.StartReviewRun(r.db, runDate, scope, opts.Force)
		if err == db.ErrReviewAlreadyDone {
			existing, _ := db.GetReviewRun(r.db, runDate, scope)
			return &Result{Run: existing, AlreadyDone: true}, nil
		}
		if err != nil {
			return nil, err
		}
		run.ID = id
		// A run that dies mid-flight must leave a row that says so; the
		// deferred failure is the difference between "no report today" and
		// "the report is missing and nobody knows why". run.Status is set to
		// done before the row is finished, so this fires exactly on the paths
		// that never got there — including a panic unwinding through here.
		defer func() {
			if run.Status != db.ReviewStatusDone {
				if err := db.FailReviewRun(r.db, run.ID, fmt.Errorf("review did not complete")); err != nil {
					log.Printf("review: mark run %d failed: %v", run.ID, err)
				}
			}
		}()
	}

	budget := r.maxDiffLines()
	rules := r.ruleSet()
	modelPass := !opts.NoAI && r.refiner != nil && modelPassConfigured(r.db)
	seen := map[string]bool{}
	var (
		findings   []review.Finding
		candidates []db.ReviewFinding
		skipped    int
		files      int
		commits    int
		aiCalls    int
		aiFailures int
	)

	// The cross-repository index includes roots with no commits in the window,
	// so A can find B's provider. Build it only when a file reaches the rules:
	// a day with no changes costs no git calls.
	var crossRepo *review.MultiRepo

	for _, repoPath := range repos {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		repoCommits, err := review.LogCommits(ctx, repoPath, day, day.AddDate(0, 0, 1))
		if err != nil {
			// One unreadable repository must not abort the day: a mount that
			// went away is not a reason to lose the other 19 reports.
			log.Printf("review: %s: %v", repoPath, err)
			skipped++
			continue
		}
		if len(repoCommits) == 0 {
			continue
		}
		for _, c := range repoCommits {
			commits++
			changes, err := review.CommitPatch(ctx, repoPath, c.SHA)
			if err != nil {
				log.Printf("review: %s@%s: %v", repoPath, c.SHA, err)
				skipped++
				continue
			}
			for _, ch := range changes {
				if ch.Binary || ch.Status == review.StatusDeleted {
					continue
				}
				if ch.ChangedLines() > budget {
					skipped++
					continue
				}
				content, err := review.FileAtCommit(ctx, repoPath, c.SHA, ch.Path)
				if err != nil {
					skipped++
					continue
				}
				files++
				f := &review.File{
					Path:    ch.Path,
					Key:     review.PathKey(repoPath, ch.Path),
					Content: content,
					Added:   ch.Added(),
					Removed: ch.Removed(),
					Status:  ch.Status,
				}
				if crossRepo == nil {
					crossRepo = review.NewMultiRepo(ctx, repos)
				}
				var fileFindings []review.Finding
				for _, rule := range rules {
					for _, found := range rule.Check(f, crossRepo) {
						key := repoPath + "\x00" + ch.Path + "\x00" + strconv.Itoa(found.Line) + "\x00" + found.Rule + "\x00" + found.Message
						if seen[key] {
							continue
						}
						seen[key] = true
						fileFindings = append(fileFindings, found)
					}
				}
				// The model pass runs per file, on this file's candidates only,
				// and only when there is something to review — the "zero
				// candidates, zero calls" property in ADR-0053 D2.
				if modelPass && len(fileFindings) > 0 {
					var calls, failures int
					fileFindings, calls, failures = r.refine(ctx, ch.Path, content, fileFindings)
					aiCalls += calls
					aiFailures += failures
				}
				for _, found := range fileFindings {
					// A rule cannot know which repository it was handed
					// (its input is one file), so the location is stamped
					// here — the only place that knows it. The stored rows
					// get the same values from the same loop, and a dry-run
					// preview now carries real paths, which the stored rows
					// never can.
					found.RepoPath = repoPath
					found.FilePath = ch.Path
					found.CommitSHA = c.SHA
					findings = append(findings, found)
					candidates = append(candidates, db.ReviewFinding{
						RunID:      run.ID,
						RepoPath:   repoPath,
						CommitSHA:  c.SHA,
						FilePath:   ch.Path,
						Line:       found.Line,
						Rule:       found.Rule,
						Severity:   string(found.Severity),
						Verdict:    string(found.Verdict),
						Evidence:   found.Evidence,
						Message:    found.Message,
						Suggestion: found.Suggestion,
					})
				}
			}
		}
	}

	if crossRepo != nil {
		if crossRepo.Truncated() || crossRepo.Skipped() > 0 {
			log.Printf("review: cross-repo index truncated=%t skipped=%d", crossRepo.Truncated(), crossRepo.Skipped())
		}
		skipped += crossRepo.Skipped()
	}

	run.Repos = len(repos)
	run.Commits = commits
	run.Files = files
	run.Candidates = len(findings)
	run.Findings = len(findings)
	run.Skipped = skipped
	run.AICalls = aiCalls
	run.Status = db.ReviewStatusDone
	// Model-pass failures stay visible on the run without changing its status:
	// the rule-only report is still usable, but "done" must not read as
	// "the model checked everything".
	if aiFailures > 0 {
		run.Error = fmt.Sprintf("%d model passes failed", aiFailures)
	}

	result := &Result{Findings: findings, DryRun: opts.DryRun}
	if opts.DryRun {
		run.RunDate = runDate
		run.Scope = scope
		result.Run = run
		return result, nil
	}

	if err := db.InsertReviewFindings(r.db, candidates); err != nil {
		err = fmt.Errorf("store findings: %w", err)
		run.Status = db.ReviewStatusFailed
		return nil, err
	}
	if err := db.FinishReviewRun(r.db, run); err != nil {
		run.Status = db.ReviewStatusFailed
		return nil, err
	}
	stored, err := db.ReviewFindings(r.db, run.ID, nil, nil)
	if err != nil {
		return nil, err
	}
	result.Run = run
	result.Stored = stored
	return result, nil
}

// repos resolves the scope to concrete git repositories. Projects can be merge
// directories (ADR-0023), so the project's root_path is not necessarily a
// repository — the repositories table is the only honest list.
func (r *Runner) repos(scope string) ([]string, error) {
	var projectIDs []int64
	switch {
	case scope == DefaultScope || scope == "":
		ids, err := db.GetCollectedProjectIDs(context.Background(), r.db)
		if err != nil {
			return nil, err
		}
		projectIDs = ids
	case strings.HasPrefix(scope, "project:"):
		for _, part := range strings.Split(strings.TrimPrefix(scope, "project:"), ",") {
			n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad scope %q: %w", scope, err)
			}
			projectIDs = append(projectIDs, n)
		}
	default:
		return nil, fmt.Errorf("bad scope %q: want 'collected' or 'project:<ids>'", scope)
	}

	seen := map[string]bool{}
	var repos []string
	for _, id := range projectIDs {
		list, err := db.GetRepositoriesByProjectID(r.db, id)
		if err != nil {
			return nil, err
		}
		for _, repo := range list {
			p := filepath.Clean(repo.Path)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			repos = append(repos, p)
		}
	}
	sort.Strings(repos)
	return repos, nil
}

// ruleSet returns the rules to run, honouring per-rule config. Every rule reads
// `review.rule.<id>`; an absent key keeps the default (on for normal rules, off
// for OptInRule ones), so an existing install that never touched the switches
// keeps all nine default rules on. This mirrors Config so the settings page and
// the runner cannot disagree.
func (r *Runner) ruleSet() []review.Rule {
	all := review.DefaultRules()
	out := make([]review.Rule, 0, len(all))
	for _, rule := range all {
		if !ruleEnabled(r.db, rule) {
			continue
		}
		out = append(out, rule)
	}
	return out
}

// maxDiffLines reads the per-file budget. A file over budget is skipped and
// counted, never silently dropped — a report that quietly ignores the one
// 5000-line generated file is fine, one that quietly ignores a real refactor
// is not.
func (r *Runner) maxDiffLines() int {
	v, _ := db.GetConfig(r.db, KeyMaxDiff)
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return DefaultMaxDiff
	}
	return n
}

// modelPassConfigured reports whether the model pass is enabled in config. `review.ai` defaults to
// 1 — the feature as a whole is off by default (`review.enabled`), so a user
// who turned the feature on gets the second opinion unless they said otherwise.
func modelPassConfigured(database *sql.DB) bool {
	v, err := db.GetConfig(database, KeyAI)
	if err != nil || strings.TrimSpace(v) == "" {
		return true
	}
	return v == "1" || strings.EqualFold(v, "true")
}

// Latest returns the most recent completed run with its findings, which is what
// the desktop page and the MCP read tool both render.
func (r *Runner) Latest(severities, verdicts []string) (*db.ReviewRun, []db.ReviewFinding, error) {
	run, err := db.LatestReviewRun(r.db)
	if err != nil || run == nil {
		return nil, nil, err
	}
	findings, err := db.ReviewFindings(r.db, run.ID, severities, verdicts)
	if err != nil {
		return nil, nil, err
	}
	return run, findings, nil
}

// Report returns one run by date+scope and its findings.
func (r *Runner) Report(runDate, scope string, severities, verdicts []string) (*db.ReviewRun, []db.ReviewFinding, error) {
	run, err := db.GetReviewRun(r.db, runDate, scope)
	if err != nil || run == nil {
		return nil, nil, err
	}
	findings, err := db.ReviewFindings(r.db, run.ID, severities, verdicts)
	if err != nil {
		return nil, nil, err
	}
	return run, findings, nil
}

// RunsSince returns the completed runs of the last n days, newest first. It
// backs the MCP read tool's "days" argument: an agent asking "what did the
// review say this week" must not have to issue seven calls.
func (r *Runner) RunsSince(days int) ([]db.ReviewRun, error) {
	if days <= 0 {
		days = 1
	}
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return db.ReviewRunsInRange(r.db, since)
}

// StoredPreview converts the in-memory candidates of a run into the same row
// shape a stored report has, so the preview (dry run) and the report render
// identically: same fields, same locations, one conversion.
//
// This function exists because until 2026-11-09 each surface made its own
// translation and each one dropped the file path: the CLI filled empty strings,
// the MCP returned `Stored` — always nil on a dry run, so the tool answered
// "here is your preview" with an empty list. A preview without paths is not a
// preview; you cannot go and look.
func StoredPreview(findings []review.Finding) []db.ReviewFinding {
	out := make([]db.ReviewFinding, 0, len(findings))
	for _, f := range findings {
		out = append(out, db.ReviewFinding{
			RepoPath:   f.RepoPath,
			CommitSHA:  f.CommitSHA,
			FilePath:   f.FilePath,
			Line:       f.Line,
			Rule:       f.Rule,
			Severity:   string(f.Severity),
			Verdict:    string(f.Verdict),
			Evidence:   f.Evidence,
			Message:    f.Message,
			Suggestion: f.Suggestion,
		})
	}
	return out
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}
