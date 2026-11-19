package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"repotory/internal/aitools"
	"repotory/internal/scanner"

	"repotory/internal/db"
	"repotory/internal/stats"
)

// registerDashboardTools registers the tools that surface the dashboard's
// code-stats and project-listing capabilities to the AI conversation.
func (s *Service) registerDashboardTools(reg *aitools.Registry) {
	reg.Register(
		"query_daily_stats",
		"Query daily code-change stats for one repository or every repository under a directory on a given date. Returns per-repo and totalled files_changed, lines_added, lines_deleted and commits.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repo_path": map[string]any{"type": "string", "description": "Absolute path to a Git repository, or a directory: all Git repositories beneath it are discovered, de-duplicated and summed"},
				"date":      map[string]any{"type": "string", "description": "Date in YYYY-MM-DD format"},
				"author":    map[string]any{"type": "string", "description": "Optional Git author filter"},
			},
			"required": []string{"repo_path", "date"},
		},
		s.toolQueryDailyStats,
	)
	reg.Register(
		"list_projects",
		"List all known projects in Cinder, with names, root paths and repository counts.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		s.toolListProjects,
	)
	reg.Register(
		"get_project_stats",
		"Get aggregated daily code-change stats for one project on a given date.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
				"date":       map[string]any{"type": "string", "description": "Date in YYYY-MM-DD format"},
			},
			"required": []string{"project_id", "date"},
		},
		s.toolGetProjectStats,
	)
	reg.Register(
		"search_notes",
		"Search the local knowledge base (notes and pages) for relevant content.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "description": "The search query"},
				"project_id": map[string]any{"type": "number", "description": "Optional project ID to scope the search"},
			},
			"required": []string{"query"},
		},
		s.toolSearchNotes,
	)
}

// dailyStatsScanDepth bounds directory expansion. A project root usually holds
// repos one or two levels down; four is deep enough for nested monorepos while
// scanner.MaxEntries still caps the walk.
const dailyStatsScanDepth = 4

func (s *Service) toolQueryDailyStats(ctx context.Context, args map[string]any) (any, error) {
	repoPath, _ := args["repo_path"].(string)
	date, _ := args["date"].(string)
	author, _ := args["author"].(string)
	if repoPath == "" {
		return nil, fmt.Errorf("repo_path is required")
	}
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}

	paths, err := resolveStatsRepoPaths(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	perRepo := make([]map[string]any, 0, len(paths))
	total := &stats.Result{}
	for _, p := range paths {
		res, err := stats.QueryStats(p, date, author)
		if err != nil {
			return nil, fmt.Errorf("query %s: %w", p, err)
		}
		if res == nil {
			continue
		}
		total.FilesChanged += res.FilesChanged
		total.LinesAdded += res.LinesAdded
		total.LinesDeleted += res.LinesDeleted
		total.Commits += res.Commits
		perRepo = append(perRepo, map[string]any{
			"repo_path":     p,
			"files_changed": res.FilesChanged,
			"lines_added":   res.LinesAdded,
			"lines_deleted": res.LinesDeleted,
			"commits":       res.Commits,
		})
	}

	return map[string]any{
		"repo_path":     repoPath,
		"date":          date,
		"author":        author,
		"repo_count":    len(perRepo),
		"repos":         perRepo,
		"files_changed": total.FilesChanged,
		"lines_added":   total.LinesAdded,
		"lines_deleted": total.LinesDeleted,
		"commits":       total.Commits,
	}, nil
}

// resolveStatsRepoPaths turns repo_path into a de-duplicated list of actual
// git repositories. A path that is itself a repo stays a single-element list
// (exact old behaviour); a directory expands to every git repo beneath it.
// 找不到任何仓库时返回明确 error —— 不能让工具把「范围里没有仓库」伪装成
// 「这些仓库今天 0 提交」。
func resolveStatsRepoPaths(ctx context.Context, repoPath string) ([]string, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("invalid repo_path %q: %w", repoPath, err)
	}
	if info, statErr := os.Stat(filepath.Join(abs, ".git")); statErr == nil && info.IsDir() {
		return []string{abs}, nil
	}

	found, err := scanner.ScanRepositories(ctx, []string{abs}, dailyStatsScanDepth)
	if err != nil {
		return nil, fmt.Errorf("scan repositories under %q: %w", repoPath, err)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no git repository found at or under %q — pass a repository path or a directory containing one", repoPath)
	}

	// scanner walks one root so paths are unique by construction, but de-dup
	// anyway: overlapping roots or symlinked trees must never double-count a
	// repo's commits in the aggregate.
	seen := make(map[string]bool, len(found))
	paths := make([]string, 0, len(found))
	for _, r := range found {
		clean := filepath.Clean(r.Path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		paths = append(paths, clean)
	}
	return paths, nil
}

func (s *Service) toolListProjects(ctx context.Context, _ map[string]any) (any, error) {
	projects, err := db.GetAllProjects(s.db)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		repos, _ := db.GetRepositoriesByProjectID(s.db, p.ID)
		out = append(out, map[string]any{
			"project_id": p.ID,
			"name":       p.Name,
			"root_path":  p.RootPath,
			"repo_count": len(repos),
			"is_starred": p.IsStarred,
			"collected":  p.Collected,
		})
	}
	return out, nil
}

func (s *Service) toolGetProjectStats(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	date, _ := args["date"].(string)
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	statsList, err := db.GetStatsByProject(s.db, projectID, date)
	if err != nil {
		return nil, err
	}
	repos, _ := db.GetRepositoriesByProjectID(s.db, projectID)
	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	var totalAdded, totalDeleted, totalFiles int
	authorStats := make(map[string]map[string]int)
	for _, st := range statsList {
		totalAdded += st.LinesAdded
		totalDeleted += st.LinesDeleted
		totalFiles += st.FilesChanged
		if authorStats[st.Author] == nil {
			authorStats[st.Author] = map[string]int{}
		}
		authorStats[st.Author]["lines_added"] += st.LinesAdded
		authorStats[st.Author]["lines_deleted"] += st.LinesDeleted
		authorStats[st.Author]["files_changed"] += st.FilesChanged
	}
	repoList := make([]map[string]any, 0, len(repos))
	for _, r := range repos {
		repoList = append(repoList, map[string]any{
			"repo_id": r.ID,
			"path":    r.Path,
			"name":    r.Path,
		})
	}
	return map[string]any{
		"project_id":   projectID,
		"project_name": project.Name,
		"date":         date,
		"total": map[string]any{
			"lines_added":   totalAdded,
			"lines_deleted": totalDeleted,
			"files_changed": totalFiles,
		},
		"by_author": authorStats,
		"repos":     repoList,
	}, nil
}

func (s *Service) toolSearchNotes(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	hits, err := s.SearchAll(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		if projectID > 0 && h.ProjectID != projectID {
			continue
		}
		out = append(out, map[string]any{
			"id":         h.ID,
			"type":       h.Type,
			"title":      h.Title,
			"snippet":    h.Snippet,
			"project_id": h.ProjectID,
			"rank":       h.Rank,
		})
		if len(out) >= 10 {
			break
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Tool-calling loop for AskAI / AskAIWithEvidence
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Additional dashboard tools
// ---------------------------------------------------------------------------

// registerDashboardToolsExtra registers the additional dashboard tools,
// including the batch-project query tool used when a user picks several
// projects.
func (s *Service) registerDashboardToolsExtra(reg *aitools.Registry) {
	reg.Register(
		"get_recent_commits",
		"Get the most recent commits across one project's repositories, newest first. Returns hash, time, message, author and branch per commit.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
				"limit":      map[string]any{"type": "number", "description": "Max commits to return (default 10, max 50)"},
			},
			"required": []string{"project_id"},
		},
		s.toolGetRecentCommits,
	)
	reg.Register(
		"get_heatmap",
		"Get daily code-change heatmap for one project over a trailing window of days. Returns per-day lines_added, lines_deleted and commits.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
				"days":       map[string]any{"type": "number", "description": "Number of trailing days (default 30, max 90)"},
			},
			"required": []string{"project_id"},
		},
		s.toolGetHeatmap,
	)
	reg.Register(
		"get_project_overview",
		"Get a project's overview: name, path, repository count, total and personal lines added/deleted for today, and whether today is below the daily standard.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id": map[string]any{"type": "number", "description": "Project ID (from list_projects)"},
			},
			"required": []string{"project_id"},
		},
		s.toolGetProjectOverview,
	)
	reg.Register(
		"query_project_stats",
		"Query aggregate daily code-change stats for one or more projects. Required when the user has selected several projects from project list. Returns one record per project.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "number"},
					"description": "Project IDs obtained from list_projects.",
				},
				"date": map[string]any{
					"type":        "string",
					"description": "Date in YYYY-MM-DD format.",
				},
			},
			"required": []string{"project_ids", "date"},
		},
		s.toolQueryProjectStats,
	)
}

func (s *Service) toolGetRecentCommits(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		if int(v) < limit {
			limit = int(v)
		}
		if limit > 50 {
			limit = 50
		}
	}
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return []map[string]any{}, nil
	}
	var paths []string
	for _, r := range repos {
		paths = append(paths, r.Path)
	}
	commits, err := stats.GetRecentCommits(paths, "", limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(commits))
	for _, c := range commits {
		out = append(out, map[string]any{
			"hash":    c.Hash,
			"time":    c.Time,
			"message": c.Message,
			"author":  c.Author,
			"repo":    c.Repo,
			"branch":  c.Branch,
		})
	}
	return out, nil
}

func (s *Service) toolGetHeatmap(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	days := 30
	if v, ok := args["days"].(float64); ok && v > 0 {
		if int(v) < days {
			days = int(v)
		}
		if days > 90 {
			days = 90
		}
	}
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	today := stats.GetTodayDate()
	start := stats.DaysBefore(today, days-1)
	heatmap, err := db.GetHeatmapData(s.db, start, today, "", projectID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(heatmap))
	for _, h := range heatmap {
		out = append(out, map[string]any{
			"date":          h.Date,
			"lines_added":   h.LinesAdded,
			"lines_deleted": h.LinesDeleted,
			"commits":       h.Commits,
		})
	}
	return out, nil
}

func (s *Service) toolGetProjectOverview(ctx context.Context, args map[string]any) (any, error) {
	projectID := int64(0)
	if v, ok := args["project_id"].(float64); ok {
		projectID = int64(v)
	}
	if projectID == 0 {
		return nil, fmt.Errorf("project_id is required")
	}
	project, err := db.GetProjectByID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
	if err != nil {
		return nil, err
	}
	today := s.git.GetTodayDate()
	statsList, err := db.GetStatsByProject(s.db, projectID, today)
	if err != nil {
		return nil, err
	}
	var totalAdded, totalDeleted, myAdded, myDeleted int
	author := s.gitUser()
	for _, st := range statsList {
		totalAdded += st.LinesAdded
		totalDeleted += st.LinesDeleted
		if st.Author == author {
			myAdded += st.LinesAdded
			myDeleted += st.LinesDeleted
		}
	}
	codeStd := s.dailyCodeStandard()
	isWorkday := s.git.IsWorkday(today)
	belowStandard := isWorkday && myAdded < codeStd
	return map[string]any{
		"project_id":   projectID,
		"project_name": project.Name,
		"root_path":    project.RootPath,
		"repo_count":   len(repos),
		"date":         today,
		"total": map[string]any{
			"lines_added":   totalAdded,
			"lines_deleted": totalDeleted,
		},
		"mine": map[string]any{
			"lines_added":   myAdded,
			"lines_deleted": myDeleted,
		},
		"is_workday":     isWorkday,
		"below_standard": belowStandard,
		"daily_standard": codeStd,
	}, nil
}

// ---------------------------------------------------------------------------
// Streaming tool-calling loop (for AskAIWithEvidenceStream)
// ---------------------------------------------------------------------------

func (s *Service) toolQueryProjectStats(ctx context.Context, args map[string]any) (any, error) {
	projectIDsRaw, _ := args["project_ids"].([]any)
	date, _ := args["date"].(string)
	if len(projectIDsRaw) == 0 {
		return nil, fmt.Errorf("project_ids is required")
	}
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	out := make([]any, 0, len(projectIDsRaw))
	for _, v := range projectIDsRaw {
		pid, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("invalid project_id %v", v)
		}
		res, err := s.toolGetProjectStats(ctx, map[string]any{
			"project_id": pid,
			"date":       date,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// queryStatsBatchStat is the payload returned for one project when batch-querying
// project code-change stats from the dashboard.
type queryStatsBatchStat struct {
	ProjectID   int64  `json:"project_id"`
	ProjectName string `json:"project_name"`
	Date        string `json:"date"`
	Total       struct {
		LinesAdded   int `json:"lines_added"`
		LinesDeleted int `json:"lines_deleted"`
		FilesChanged int `json:"files_changed"`
	} `json:"total"`
	ByAuthor map[string]map[string]int `json:"by_author"`
	Repos    []map[string]any          `json:"repos"`
}

func (s *Service) QueryProjectStatsBatch(projectIDs []int64, date string) ([]queryStatsBatchStat, error) {
	if date == "" {
		date = s.git.GetYesterdayDate()
	}
	if err := s.git.ValidateDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}
	out := make([]queryStatsBatchStat, 0, len(projectIDs))
	for _, pid := range projectIDs {
		statsList, err := db.GetStatsByProject(s.db, pid, date)
		if err != nil {
			return nil, err
		}
		repos, _ := db.GetRepositoriesByProjectID(s.db, pid)
		project, err := db.GetProjectByID(s.db, pid)
		if err != nil {
			return nil, err
		}

		var totalAdded, totalDeleted, totalFiles int
		byAuthor := make(map[string]map[string]int)
		for _, st := range statsList {
			totalAdded += st.LinesAdded
			totalDeleted += st.LinesDeleted
			totalFiles += st.FilesChanged
			if byAuthor[st.Author] == nil {
				byAuthor[st.Author] = map[string]int{}
			}
			byAuthor[st.Author]["lines_added"] += st.LinesAdded
			byAuthor[st.Author]["lines_deleted"] += st.LinesDeleted
			byAuthor[st.Author]["files_changed"] += st.FilesChanged
		}
		repoList := make([]map[string]any, 0, len(repos))
		for _, r := range repos {
			repoList = append(repoList, map[string]any{
				"repo_id": r.ID,
				"path":    r.Path,
				"name":    r.Path,
			})
		}
		out = append(out, queryStatsBatchStat{
			ProjectID:   pid,
			ProjectName: project.Name,
			Date:        date,
			Total: struct {
				LinesAdded   int `json:"lines_added"`
				LinesDeleted int `json:"lines_deleted"`
				FilesChanged int `json:"files_changed"`
			}{
				LinesAdded:   totalAdded,
				LinesDeleted: totalDeleted,
				FilesChanged: totalFiles,
			},
			ByAuthor: byAuthor,
			Repos:    repoList,
		})
	}
	return out, nil
}
