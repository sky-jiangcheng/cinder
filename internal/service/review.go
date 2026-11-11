package service

import (
	"context"
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

// Incremental commit review (ADR-0053). The service is the whole pipeline:
// resolve which repositories to look at, list the day's commits, hand each
// changed file to the rules, and store the candidates. Model review is P2 and
// lives in review_ai.go; when it is off (the default for the first release)
// this file alone produces a complete, reproducible report.

// Config keys (ADR-0053 D5). They live in app_config like every other user
// setting, so the settings page and the CLI read the same source.
const (
	reviewEnabledKey   = "review.enabled"
	reviewAtKey        = "review.at"
	reviewScopeKey     = "review.scope"
	reviewAIKey        = "review.ai"
	reviewNotifyKey    = "review.notify"
	reviewMaxDiffKey   = "review.max_diff_lines"
	reviewOptInRuleKey = "review.rule." // + rule id -> "1"
)

// Defaults for the keys above. `review.enabled=0` is not caution theatre: the
// job reads source code and (with AI on) sends excerpts off the machine, so it
// must be a deliberate choice (ADR-0053 D5).
const (
	reviewDefaultAt      = "09:30"
	reviewDefaultScope   = "collected"
	reviewDefaultNotify  = "high"
	reviewDefaultMaxDiff = 2000
)

// ReviewOptions controls one run.
type ReviewOptions struct {
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

// ReviewResult is what a caller gets back: the run row (nil on a dry run), the
// candidates as computed, and counters.
// ReviewResult is what every trigger returns (CLI, MCP tool, desktop button).
//
// The json tags are load-bearing: this struct crosses the Wails binding to the
// frontend and the RPC bridge to a browser, and Go's default field names are
// capitalised — `AlreadyDone` would arrive as `AlreadyDone`, so a frontend
// reading `already_done` would see undefined and report every idempotent call
// as a fresh run. Snake_case everywhere, matching db.ReviewRun.
type ReviewResult struct {
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

// reviewRuleSet returns the rules to run, honouring per-rule config. Every
// rule reads `review.rule.<id>`; an absent key keeps the default (on for
// normal rules, off for OptInRule ones), so an existing install that never
// touched the switches keeps all nine default rules on. This mirrors
// GetReviewConfig so the settings page and the runner cannot disagree.
func (s *Service) reviewRuleSet() []review.Rule {
	all := review.DefaultRules()
	out := make([]review.Rule, 0, len(all))
	for _, r := range all {
		on := true
		if opt, ok := r.(review.OptInRule); ok && opt.OptIn() {
			on = false
		}
		if v, _ := db.GetConfig(s.db, reviewOptInRuleKey+r.ID()); v != "" {
			on = v == "1" || strings.EqualFold(v, "true")
		}
		if !on {
			continue
		}
		out = append(out, r)
	}
	return out
}

// reviewMaxDiffLines reads the per-file budget. A file over budget is skipped
// and counted, never silently dropped — a report that quietly ignores the one
// 5000-line generated file is fine, one that quietly ignores a real refactor
// is not.
func (s *Service) reviewMaxDiffLines() int {
	v, _ := db.GetConfig(s.db, reviewMaxDiffKey)
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return reviewDefaultMaxDiff
	}
	return n
}

// RunReview executes one incremental review. It is safe to call from the CLI,
// from the MCP server and from the desktop ticker: all side effects go through
// the database, and the unique (run_date, scope) key makes a concurrent second
// run fail fast instead of interleaving findings.
func (s *Service) RunReview(ctx context.Context, opts ReviewOptions) (*ReviewResult, error) {
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
		scope, _ = db.GetConfig(s.db, reviewScopeKey)
		if strings.TrimSpace(scope) == "" {
			scope = reviewDefaultScope
		}
	}

	repos, err := s.reviewRepos(scope)
	if err != nil {
		return nil, err
	}

	run := &db.ReviewRun{RunDate: runDate, Scope: scope, Status: db.ReviewStatusRunning}
	if !opts.DryRun {
		id, err := db.StartReviewRun(s.db, runDate, scope, opts.Force)
		if err == db.ErrReviewAlreadyDone {
			existing, _ := db.GetReviewRun(s.db, runDate, scope)
			return &ReviewResult{Run: existing, AlreadyDone: true}, nil
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
				if err := db.FailReviewRun(s.db, run.ID, fmt.Errorf("review did not complete")); err != nil {
					log.Printf("review: mark run %d failed: %v", run.ID, err)
				}
			}
		}()
	}

	budget := s.reviewMaxDiffLines()
	rules := s.reviewRuleSet()
	aiEnabled := !opts.NoAI && s.reviewAIEnabled()
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
				if aiEnabled && len(fileFindings) > 0 {
					var calls, failures int
					fileFindings, calls, failures = s.refineWithCandidates(ctx, ch.Path, content, fileFindings)
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

	result := &ReviewResult{Findings: findings, DryRun: opts.DryRun}
	if opts.DryRun {
		run.RunDate = runDate
		run.Scope = scope
		result.Run = run
		return result, nil
	}

	if err := db.InsertReviewFindings(s.db, candidates); err != nil {
		err = fmt.Errorf("store findings: %w", err)
		run.Status = db.ReviewStatusFailed
		return nil, err
	}
	if err := db.FinishReviewRun(s.db, run); err != nil {
		run.Status = db.ReviewStatusFailed
		return nil, err
	}
	stored, err := db.ReviewFindings(s.db, run.ID, nil, nil)
	if err != nil {
		return nil, err
	}
	result.Run = run
	result.Stored = stored
	return result, nil
}

// reviewRepos resolves the scope to concrete git repositories. Projects can be
// merge directories (ADR-0023), so the project's root_path is not necessarily
// a repository — the repositories table is the only honest list.
func (s *Service) reviewRepos(scope string) ([]string, error) {
	var projectIDs []int64
	switch {
	case scope == reviewDefaultScope || scope == "":
		ids, err := db.GetCollectedProjectIDs(context.Background(), s.db)
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
		list, err := db.GetRepositoriesByProjectID(s.db, id)
		if err != nil {
			return nil, err
		}
		for _, r := range list {
			p := filepath.Clean(r.Path)
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

// LatestReview returns the most recent completed run with its findings, which
// is what the desktop page and the MCP read tool both render.
func (s *Service) LatestReview(severities, verdicts []string) (*db.ReviewRun, []db.ReviewFinding, error) {
	run, err := db.LatestReviewRun(s.db)
	if err != nil || run == nil {
		return nil, nil, err
	}
	findings, err := db.ReviewFindings(s.db, run.ID, severities, verdicts)
	if err != nil {
		return nil, nil, err
	}
	return run, findings, nil
}

// StoredPreview converts the in-memory candidates of a run into the same row
// shape a stored report has, so the preview (dry run) and the report render
// identically: same fields, same locations, one conversion.
//
// This function exists because until 2026-11-09 each surface made its own
// translation and each one dropped the file path: the CLI filled empty
// strings, the MCP returned `Stored` — always nil on a dry run, so the tool
// answered "here is your preview" with an empty list. A preview without
// paths is not a preview; you cannot go and look.
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

// ReviewRunsSince returns the completed runs of the last n days, newest
// first. It backs the MCP read tool's "days" argument: an agent asking "what
// did the review say this week" must not have to issue seven calls.
func (s *Service) ReviewRunsSince(days int) ([]db.ReviewRun, error) {
	if days <= 0 {
		days = 1
	}
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return db.ReviewRunsInRange(s.db, since)
}

// ReviewReport returns one run by date+scope and its findings.
func (s *Service) ReviewReport(runDate, scope string, severities, verdicts []string) (*db.ReviewRun, []db.ReviewFinding, error) {
	run, err := db.GetReviewRun(s.db, runDate, scope)
	if err != nil || run == nil {
		return nil, nil, err
	}
	findings, err := db.ReviewFindings(s.db, run.ID, severities, verdicts)
	if err != nil {
		return nil, nil, err
	}
	return run, findings, nil
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}
