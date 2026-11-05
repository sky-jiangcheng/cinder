package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"repotory/internal/db"
)

// Metrics reports persisted observations, never a live filesystem/git census.
// Repository/daily counts use the matched repository, or all repositories of a
// matched project. Notes and review counts use that repository's owning project
// (review scope is exact, not the retrieval scope's project-plus-global rule).
type Metrics struct {
	RepositoryCount    int     `json:"repositoryCount"`
	TotalLines         *int64  `json:"totalLines"`
	CommitsToday       int     `json:"commitsToday"`
	AdditionsToday     int     `json:"additionsToday"`
	DeletionsToday     int     `json:"deletionsToday"`
	NotesCount         int     `json:"notesCount"`
	PendingReviewCount int     `json:"pendingReviewCount"`
	RefreshedAt        *string `json:"refreshedAt"`
}

func (s *Service) GetMetrics(ctx context.Context, path string) (*Metrics, error) {
	if err := validateChatPath(path); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repoID, projectID := int64(0), int64(0)
	if path != "" {
		// Reuse the workspace matchers, not ResolveProjectByPath: that UI helper
		// intentionally swallows DB errors, which must be real MCP errors here.
		repos, err := db.GetAllRepositories(s.db)
		if err != nil {
			return nil, err
		}
		repo, _, err := db.PickRepositoryForPath(repos, path)
		if err != nil {
			return nil, err
		}
		if repo != nil {
			repoID, projectID = repo.ID, -1 // orphan repositories have no knowledge scope
			if repo.ProjectID != nil {
				projectID = *repo.ProjectID
			}
		} else {
			projects, err := db.GetAllProjects(s.db)
			if err != nil {
				return nil, err
			}
			project, _, err := db.PickProjectForPath(projects, path, false)
			if err != nil {
				return nil, err
			}
			if project == nil {
				return &Metrics{}, nil // an unknown path must NEVER fall back to global totals
			}
			projectID = project.ID
		}
	}
	// Repository matching takes precedence over project matching, including
	// orphan repositories. Project id still scopes the note/review counts.
	repoProjectID := projectID
	if repoID > 0 {
		repoProjectID = 0
	}
	const scope = `WITH scoped AS (SELECT r.* FROM repositories r
	 WHERE (? = 0 OR r.id = ?) AND (? = 0 OR r.project_id = ?)) `
	scopeArgs := []any{repoID, repoID, repoProjectID, repoProjectID}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &Metrics{}
	args := append([]any{}, scopeArgs...)
	// refreshRepoStatsRange stores one 'all' row PLUS a personal author row.
	// Summing both double counts commits/lines. Today uses the same local date
	// source as the existing refresh and heatmap services; this calls no git IO.
	args = append(args, projectID, projectID, projectID, projectID, s.git.GetTodayDate())
	err = tx.QueryRowContext(ctx, scope+`SELECT
	 (SELECT COUNT(*) FROM scoped),
	 COALESCE(SUM(d.commits), 0), COALESCE(SUM(d.lines_added), 0), COALESCE(SUM(d.lines_deleted), 0),
	 (SELECT COUNT(*) FROM project_notes WHERE (? = 0 OR project_id = ?)),
	 (SELECT COUNT(*) FROM wiki_pages WHERE status = 'pending' AND (? = 0 OR project_id = ?)),
	 (SELECT strftime('%Y-%m-%dT%H:%M:%fZ', MAX(last_scanned_at)) FROM scoped)
	 FROM (SELECT ds.* FROM daily_stats ds JOIN scoped r ON r.id = ds.repository_id
	 WHERE date(ds.stat_date) = ? AND ds.author = 'all') d`, args...).Scan(
		&out.RepositoryCount, &out.CommitsToday, &out.AdditionsToday, &out.DeletionsToday,
		&out.NotesCount, &out.PendingReviewCount, &out.RefreshedAt)
	if err != nil {
		return nil, fmt.Errorf("read stored metrics: %w", err)
	}

	// repo_meta.languages is the existing LOC source. Older mining also caches
	// whole project containers under the first repo id, so blindly summing cache
	// rows can double count siblings. Only singleton projects rooted at their
	// repository have unambiguous attribution. Missing/ambiguous/partial caches
	// mean unknown, not zero and not a misleading partial total.
	rows, err := tx.QueryContext(ctx, scope+`SELECT r.path, COALESCE(p.root_path, ''),
	 COALESCE(m.languages, ''), (SELECT COUNT(*) FROM repositories rr WHERE rr.project_id = r.project_id)
	 FROM scoped r LEFT JOIN projects p ON p.id = r.project_id
	 LEFT JOIN repo_meta m ON m.repository_id = r.id`, scopeArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	total, complete := int64(0), out.RepositoryCount > 0
	for rows.Next() {
		var repoPath, root, languages string
		var siblings int
		if err := rows.Scan(&repoPath, &root, &languages, &siblings); err != nil {
			return nil, err
		}
		// Same shape as knowledge.LanguageStat, but a pointer distinguishes a
		// recorded zero from a malformed cache that omitted its count.
		var counts []struct {
			Language string `json:"language"`
			Count *int64 `json:"count"`
		}
		if siblings != 1 || db.NormalizeMatchPath(repoPath) != db.NormalizeMatchPath(root) || json.Unmarshal([]byte(languages), &counts) != nil || len(counts) == 0 {
			complete = false
			continue
		}
		for _, c := range counts {
			if c.Language == "" || c.Count == nil || *c.Count < 0 || *c.Count > (1<<63-1)-total {
				complete = false
				continue
			}
			total += *c.Count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if complete {
		out.TotalLines = &total
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
