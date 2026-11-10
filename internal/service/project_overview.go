package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/integrity"
	"repotory/internal/knowledge"
	"repotory/internal/stats"
)

// ErrProjectNotFound marks the "no such project" case distinctly from a
// storage failure: GetProjectByID's error used to collapse into a plain
// "project not found", so a full disk or a locked database surfaced as 404
// and neither an operator nor an agent could tell "absent" from "broken".
// Transports (httpapi maps it to 404, everything else to 500) should check
// it with errors.Is.
// ErrRepoNotFound is the "no such repository" counterpart of
// ErrProjectNotFound, returned by GetRepoCommits.
var ErrRepoNotFound = errors.New("repository not found")

var ErrProjectNotFound = errors.New("project not found")

// ErrNoteNotFound is the domain answer for "that note is gone" — callers used to
// receive the driver's `sql: no rows in result set`, which then reached the UI
// verbatim in the middle of a Chinese sentence.
var ErrNoteNotFound = errors.New("note not found")

// GetProjectDetail returns a project with all its repositories and stats.
func (s *Service) GetProjectDetail(id int64) (*ProjectDetailResponse, error) {
	project, err := db.GetProjectByID(s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("load project %d: %w", id, err)
	}
	repos, _ := db.GetRepositoriesByProjectID(s.db, id)

	repoList := make([]RepoWithStats, 0, len(repos))
	for _, repo := range repos {
		statsList, _ := db.GetStatsByRepositoryAndDate(s.db, repo.ID, "")
		if statsList == nil {
			statsList = []domain.DailyStat{}
		}
		repoList = append(repoList, RepoWithStats{
			Repository: repo,
			Stats:      statsList,
			WebURL:     stats.RemoteWebURL(repo.Path),
		})
	}
	return &ProjectDetailResponse{Project: project, Repos: repoList}, nil
}

// GetProjectStats returns daily stats for a project, optionally filtered by
// date, with the same on-demand refresh as GetProjects.
func (s *Service) GetProjectStats(id int64, date string) []domain.DailyStat {
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil
	}
	statsList, err := db.GetStatsByProject(s.db, id, date)
	if err != nil {
		log.Printf("get stats error: %v", err)
		return nil
	}
	// On-demand refresh runs in the background so this call never blocks on git.
	// The next load returns the back-filled rows. refreshProjectStatsForDate
	// deduplicates by (project, date), so a dashboard polling this endpoint
	// cannot stack up one git process storm per load.
	if len(statsList) == 0 {
		s.bgGo(fmt.Sprintf("stats refresh project %d %s", id, date), func(_ context.Context) {
			s.refreshProjectStatsForDate(id, date)
		})
	}
	return statsList
}

// UpdateProjectLevel adjusts a project's grouping: "down" splits a multi-repo
// project into per-repo projects, "up" merges sibling projects sharing the
// same parent directory into this one. Both run as a single transaction in
// the db layer.
// GetRepoCommits returns a single repository's commit log, newest first.
//
// The per-day aggregates in daily_stats carry no message and no SHA, so a repo
// row cannot render a commit list from the database - the only stored form is
// "who changed how many lines on which day". This reads git on demand instead,
// the same source the overview's recent-commit feed uses, so the list also
// works for a repo with no forge remote (an internal or worktree checkout)
// instead of dead-ending the user at a link that cannot resolve.
//
// Takes a repo id rather than a path so it cannot be pointed at an arbitrary
// directory on disk.
func (s *Service) GetRepoCommits(repoID int64, limit int) ([]stats.RecentCommit, error) {
	repo, err := db.GetRepositoryByID(s.db, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRepoNotFound
		}
		return nil, fmt.Errorf("load repository %d: %w", repoID, err)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	commits, err := stats.GetRecentCommits([]string{repo.Path}, "", limit)
	if err != nil {
		return nil, err
	}
	// Non-nil empty slice: the JSON contract says "array", so clients can read
	// .length without a null guard.
	if commits == nil {
		return []stats.RecentCommit{}, nil
	}
	return commits, nil
}

// GetProjectCommits returns a project's merged commit log across all its
// repositories, newest first. Same on-demand git read as GetRepoCommits; the
// author filter is empty (all authors) so the merged "全部" view and a single
// repo view agree on what they show.
func (s *Service) GetProjectCommits(projectID int64, limit int) ([]stats.RecentCommit, error) {
	repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
	if err != nil {
		return nil, fmt.Errorf("load repositories for project %d: %w", projectID, err)
	}
	// An empty grouping returns an empty log; only a bogus project id is an
	// error. GetRepositoriesByProjectID returns an empty slice (no error) for
	// a missing project, so existence needs its own check.
	if len(repos) == 0 {
		if _, err := db.GetProjectByID(s.db, projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrProjectNotFound
			}
			return nil, fmt.Errorf("load project %d: %w", projectID, err)
		}
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	paths := make([]string, 0, len(repos))
	for _, r := range repos {
		paths = append(paths, r.Path)
	}
	commits, err := stats.GetRecentCommits(paths, "", limit)
	if err != nil {
		return nil, err
	}
	if commits == nil {
		return []stats.RecentCommit{}, nil
	}
	return commits, nil
}

// ErrMergeNeedsConfirmation guards the destructive half of the grouping control. A
// "merge up" does not just change a number: it absorbs sibling projects — moving
// their repositories, notes and todos into the surviving project — and then DELETES
// those rows. That is deletion, and deletion in this product is an explicit,
// human-confirmed act, so the merge refuses to run until the caller says so
// deliberately. The refusal is enforced here, not in the UI, because the same
// binding is reachable from any HTTP client.
var ErrMergeNeedsConfirmation = errors.New("merge up requires explicit confirmation")

// MergePreview is what PreviewProjectMergeUp reports and what the confirmation
// dialog is built from: the sibling rows that would disappear, with the counts of
// what goes with them. The numbers come from db.MergePlan, i.e. from the very
// listing the merge consumes, so a preview cannot understate the damage.
type MergePreview struct {
	TargetID   int64                 `json:"target_id"`
	TargetName string                `json:"target_name"`
	NewRoot    string                `json:"new_root"`
	Siblings   []domain.MergeSibling `json:"siblings"`
	Repos      int                   `json:"repos"`
	Notes      int                   `json:"notes"`
	Todos      int                   `json:"todos"`
}

// PreviewProjectMergeUp answers "what would + do right now?" without changing
// anything. An empty NewRoot means there is nothing above this project to merge into.
func (s *Service) PreviewProjectMergeUp(id int64) (*MergePreview, error) {
	project, parentDir, siblings, err := db.MergePlan(s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	out := &MergePreview{
		TargetID: project.ID, TargetName: project.Name, NewRoot: parentDir,
		Siblings: siblings,
	}
	if out.Siblings == nil {
		out.Siblings = []domain.MergeSibling{}
	}
	for _, sib := range out.Siblings {
		out.Repos += sib.Repos
		out.Notes += sib.Notes
		out.Todos += sib.Todos
	}
	return out, nil
}

func (s *Service) UpdateProjectLevel(id int64, direction string, confirmed bool) (*LevelUpdateResult, error) {
	var newLevel int
	var err error
	switch direction {
	case "down":
		newLevel, err = db.SplitProjectDown(s.db, id)
	case "up":
		if !confirmed {
			return nil, ErrMergeNeedsConfirmation
		}
		newLevel, err = db.MergeProjectUp(s.db, id)
	default:
		return nil, fmt.Errorf("direction must be 'up' or 'down'")
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	return &LevelUpdateResult{Success: true, NewLevel: newLevel}, nil
}

// ToggleStar flips the starred status of a project.
func (s *Service) ToggleStar(projectID int64) (bool, error) {
	return db.ToggleProjectStar(s.db, projectID)
}

// SearchProjects searches for projects by name or path and enriches the
// results with yesterday's stats.
func (s *Service) SearchProjects(query string) []ProjectResponse {
	projects, err := db.SearchProjects(s.db, query)
	if err != nil {
		log.Printf("search projects error: %v", err)
		return nil
	}
	return s.enrichProjects(projects, s.git.GetYesterdayDate())
}

// ProjectOverview is the mined-knowledge payload for a project detail page.
type ProjectOverview struct {
	ReadmeExcerpt   string                     `json:"readme_excerpt"`
	TechStack       []knowledge.Tech           `json:"tech_stack"`
	Languages       []knowledge.LanguageStat   `json:"languages"`
	Dependencies    []knowledge.Dependency     `json:"dependencies"`
	TopContributors []knowledge.TopContributor `json:"top_contributors"`
	Activity        *knowledge.ActivityStat    `json:"activity"`
	RecentCommits   []stats.RecentCommit       `json:"recent_commits"`
	Cached          bool                       `json:"cached"`
	Mining          bool                       `json:"mining,omitempty"`
}

// mineRetryBackoff keeps a broken miner from being retried by every adjacent
// project-detail request after a failure.
const mineRetryBackoff = 2 * time.Minute

// repoMetaStale reports whether a repo_meta cache is older than the integrity
// report's staleness window. An unparseable timestamp counts as stale so a
// legacy row is refreshed instead of being trusted forever.
func repoMetaStale(updatedAt string) bool {
	t, err := time.Parse("2006-01-02 15:04:05", updatedAt)
	if err != nil {
		return true
	}
	return time.Since(t) > time.Duration(integrity.StaleAfterDays)*24*time.Hour
}

// mineRecentlyFailed reports whether mining for repoID failed inside the
// backoff window, so a burst of loads cannot re-trigger it back-to-back.
func (s *Service) mineRecentlyFailed(repoID int64) bool {
	if repoID <= 0 {
		return false
	}
	v, ok := s.mineBackoff.Load(repoID)
	if !ok {
		return false
	}
	return time.Since(v.(time.Time)) < mineRetryBackoff
}

// recordMineFailure writes the failure to the shared last-error field and arms
// the per-repo backoff. syncLastErr is the status field the settings page reads.
func (s *Service) recordMineFailure(repoID int64, err error) {
	s.syncMu.Lock()
	s.syncLastErr = err
	s.syncMu.Unlock()
	if repoID > 0 {
		s.mineBackoff.Store(repoID, time.Now())
	}
}

// GetProjectOverview returns mined knowledge for a project: README excerpt,
// detected tech stack, language breakdown and recent commits. Mined results
// are cached in repo_meta so repeated loads do not re-walk the working tree.
func (s *Service) GetProjectOverview(projectID int64) (*ProjectOverview, error) {
	return s.projectOverview(projectID, 0)
}

// GetRepositoryOverview is GetProjectOverview narrowed to ONE repository inside
// the project — the shape a workspace needs when it is opened on
// `<project>/<module>` rather than on the project root.
//
// This is not just a nicer label. Migration v13 dropped the mined cache for every
// multi-repo project because mining walked the PROJECT root, which for a
// container of sibling repositories is not a git repository at all — so a project
// like CBiPay (11 repositories) has no honest tech-stack answer at project scope,
// and `repos[0]` was never one. Narrowing to the repository the user is actually
// in gives mining a real root, which is the case v13 could only invalidate.
func (s *Service) GetRepositoryOverview(projectID, repositoryID int64) (*ProjectOverview, error) {
	return s.projectOverview(projectID, repositoryID)
}

func (s *Service) projectOverview(projectID, preferRepoID int64) (*ProjectOverview, error) {
	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("load project %d: %w", projectID, err)
	}
	repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
	if err != nil {
		log.Printf("project overview: load repositories for project %d: %v", projectID, err)
	}

	resp := &ProjectOverview{}

	// Scope selection. preferRepoID narrows to that repository (and mines at ITS
	// path); otherwise the historical behaviour stands: the cache key is the first
	// repository, and mining runs at the project root.
	scopeRepos := repos
	miningRoot := project.RootPath
	var cacheRepoID int64
	if preferRepoID > 0 {
		var chosen *domain.Repository
		for i := range repos {
			if repos[i].ID == preferRepoID {
				chosen = &repos[i]
				break
			}
		}
		if chosen == nil {
			// The repository exists but not under THIS project: answering with the
			// project's first repo would be a silent substitution, so refuse.
			return nil, fmt.Errorf("repository %d is not part of project %d: %w",
				preferRepoID, projectID, db.ErrRepositoryNotFound)
		}
		scopeRepos = []domain.Repository{*chosen}
		cacheRepoID = chosen.ID
		miningRoot = chosen.Path
	} else if len(repos) > 0 {
		cacheRepoID = repos[0].ID
	}
	cacheHit := false
	needMine := false
	if cacheRepoID > 0 {
		meta, err := db.GetRepoMeta(s.db, cacheRepoID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("project overview: load repo_meta for repo %d: %v", cacheRepoID, err)
		} else if err == nil && meta != nil && meta.TechStack != "" {
			// Only malformed JSON invalidates the cache. Valid JSON of an
			// unexpected shape (legacy "[]" placeholders in a struct column)
			// is logged and ignored: an empty column never cost the user their
			// README or tech stack before, and should not now.
			valid := true
			decode := func(field, data string, v any) {
				if data == "" {
					return
				}
				if !json.Valid([]byte(data)) {
					log.Printf("project overview: corrupt %s cache for repo %d", field, cacheRepoID)
					valid = false
					return
				}
				if !jsonUnmarshal(data, v) {
					log.Printf("project overview: unexpected %s shape for repo %d; ignoring", field, cacheRepoID)
				}
			}
			decode("tech_stack", meta.TechStack, &resp.TechStack)
			decode("languages", meta.Languages, &resp.Languages)
			decode("dependencies", meta.Dependencies, &resp.Dependencies)
			decode("top_contributors", meta.TopContributors, &resp.TopContributors)
			decode("activity", meta.Activity, &resp.Activity)
			if valid {
				resp.ReadmeExcerpt = meta.ReadmeExcerpt
				resp.Cached = true
				cacheHit = true
				if repoMetaStale(meta.UpdatedAt) {
					needMine = true
				}
			} else {
				// A corrupt cache is a miss: drop every partially decoded field
				// so the response never mixes stale and empty data.
				resp.TechStack = nil
				resp.Languages = nil
				resp.Dependencies = nil
				resp.TopContributors = nil
				resp.Activity = nil
			}
		}
	}

	// Mine when the cache is missing or stale. A stale cache is still returned,
	// but a fresh mine runs in the background so the next load is current.
	if !cacheHit {
		needMine = true
	}
	if needMine && !s.mineRecentlyFailed(cacheRepoID) {
		resp.Mining = !cacheHit
		miningRepos := scopeRepos
		root := miningRoot
		s.bgGo(fmt.Sprintf("mine project %d repo %d", projectID, cacheRepoID), func(_ context.Context) {
			s.mineAndCache(cacheRepoID, root, miningRepos)
		})
	}
	// Recent commits are always fresh. Not filtered by gitUser either: these
	// feed the "Recent commits" section of the copied AI context, where the
	// team's latest commits are the point — a mine-only filter silently drops
	// them whenever git user.name doesn't match a committer (same blanking
	// defect the status strip had, see summary.go GetStatusBar).
	repoPaths := make([]string, 0, len(scopeRepos))
	for _, r := range scopeRepos {
		repoPaths = append(repoPaths, r.Path)
	}
	if commits, err := s.git.GetRecentCommits(repoPaths, "", 8); err == nil && commits != nil {
		resp.RecentCommits = commits
	}
	// Normalize nil slices to empty (non-nil) so JSON consumers never see
	// `null` for a field documented as an array. A nil slice marshals to JSON
	// `null`, which crashes frontends that read `field.length` directly
	// (e.g. ProjectOverviewSection on the project detail page).
	if resp.TechStack == nil {
		resp.TechStack = []knowledge.Tech{}
	}
	if resp.Languages == nil {
		resp.Languages = []knowledge.LanguageStat{}
	}
	if resp.Dependencies == nil {
		resp.Dependencies = []knowledge.Dependency{}
	}
	if resp.TopContributors == nil {
		resp.TopContributors = []knowledge.TopContributor{}
	}
	if resp.RecentCommits == nil {
		resp.RecentCommits = []stats.RecentCommit{}
	}
	return resp, nil
}

// mineAndCache runs knowledge mining in the background and caches the result.
// It uses a sync.Map to skip duplicate mining requests for the same repo.
func (s *Service) mineAndCache(cacheRepoID int64, rootPath string, repos []domain.Repository) {
	// Dedup: if a mining goroutine is already running for this repo, skip.
	if cacheRepoID > 0 {
		if _, loaded := s.miningInFlight.LoadOrStore(cacheRepoID, true); loaded {
			return
		}
		defer s.miningInFlight.Delete(cacheRepoID)
	}
	minePath := rootPath
	if minePath == "" && len(repos) > 0 {
		minePath = repos[0].Path
	}
	if minePath == "" {
		return
	}
	k, err := knowledge.Mine(minePath)
	if err != nil || k == nil {
		if err == nil {
			err = fmt.Errorf("miner returned no knowledge")
		}
		log.Printf("mineAndCache: mine %q failed: %v", minePath, err)
		s.recordMineFailure(cacheRepoID, err)
		return
	}
	// The project root of a grouping is a container directory, not a git
	// repository, so Mine's per-path git probes (activity, top contributors)
	// silently return zero — they only succeed where `git` itself works. Aggregate
	// across the actual repositories instead. This applies to a single repository
	// too, and that is the common case, not an edge: one repo under
	// <projectRoot>/<repo> leaves `len(repos) > 1` false, so the old guard cached a
	// zeroed activity for every such project (4 of the 5 repo_meta rows on a real
	// library, one of them a repository with 6,660 commits). Languages/tech stack
	// walk the working tree and are already container-safe.
	if len(repos) >= 1 {
		paths := make([]string, 0, len(repos))
		for _, r := range repos {
			paths = append(paths, r.Path)
		}
		k.Activity = knowledge.AggregateActivity(paths)
		k.TopContributors = knowledge.AggregateContributors(paths, 5)
	}
	if cacheRepoID > 0 {
		ts, _ := json.Marshal(k.TechStack)
		ls, _ := json.Marshal(k.Languages)
		ds, _ := json.Marshal(k.Dependencies)
		tc, _ := json.Marshal(k.TopContributors)
		aa, _ := json.Marshal(k.Activity)
		if err := db.UpsertRepoMeta(s.db, cacheRepoID, string(ts), k.ReadmeExcerpt, string(ls), string(ds), string(tc), string(aa)); err != nil {
			log.Printf("mineAndCache: upsert repo_meta for repo %d failed: %v", cacheRepoID, err)
			s.recordMineFailure(cacheRepoID, err)
		} else {
			s.mineBackoff.Delete(cacheRepoID)
		}
	}
}

// ProjectSummary is the compact project payload used by the CLI and the MCP
// server: the project record with its repositories.
type ProjectSummary struct {
	Project   *domain.Project     `json:"project"`
	Repos     []domain.Repository `json:"repos"`
	RepoCount int                 `json:"repo_count"`
}

// GetProjectSummary returns a project with its repositories for read-only
// consumers (CLI / MCP).
func (s *Service) GetProjectSummary(id int64) (*ProjectSummary, error) {
	project, err := db.GetProjectByID(s.db, id)
	if err != nil {
		return nil, err
	}
	repos, err := db.GetRepositoriesByProjectID(s.db, id)
	if err != nil {
		return nil, err
	}
	return &ProjectSummary{Project: project, Repos: repos, RepoCount: len(repos)}, nil
}

// ListProjects returns every project. An empty result is an empty slice, never
// nil: this is the single place the "empty serialises as [] not null" rule is
// applied, because every consumer (desktop UI, /api/rpc, MCP, dsh) renders this
// straight to JSON and each one previously re-implementing the fix is how one of
// them eventually forgets to.
func (s *Service) ListProjects() []domain.Project {
	projects, err := db.GetAllProjects(s.db)
	if err != nil {
		log.Printf("list projects error: %v", err)
		return []domain.Project{}
	}
	// The agent-facing list offers only scanner-backed projects: the seeded
	// default ("Uncategorized") and any import-placeholder landing bucket have no
	// repository to act on, and this must match the context resolver's candidate
	// set so an id picked here also resolves in repotory_context (ADR-0023). The
	// desktop dashboard uses GetProjects, which still surfaces these buckets.
	projects = filterRealProjects(projects)
	if projects == nil {
		return []domain.Project{}
	}
	return projects
}
