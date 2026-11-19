package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/review"
	"repotory/internal/stats"
)

// CommitPage is one page of a searchable commit log. Total is the full match
// count across every repository, so the UI can render real page numbers
// instead of the old "50 and no more" guess.
type CommitPage struct {
	Items  []stats.RecentCommit `json:"items"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
	Limit  int                  `json:"limit"`
}

// CommitDiff is one commit's patch, split per file for the two-pane log view.
type CommitDiff struct {
	SHA   string           `json:"sha"`
	Repo  string           `json:"repo"`
	Files []CommitDiffFile `json:"files"`
}

// CommitDiffFile is one expandable "page" of a commit diff.
type CommitDiffFile struct {
	Path    string           `json:"path"`
	OldPath string           `json:"old_path,omitempty"`
	Status  string           `json:"status"`
	Binary  bool             `json:"binary"`
	Added   int              `json:"added"`
	Deleted int              `json:"deleted"`
	Hunks   []CommitDiffHunk `json:"hunks"`
}

// CommitDiffHunk is one @@ block inside a file.
type CommitDiffHunk struct {
	Header   string           `json:"header"`
	OldStart int              `json:"old_start"`
	NewStart int              `json:"new_start"`
	Lines    []CommitDiffLine `json:"lines"`
}

// CommitDiffLine carries a line number on both sides so the file popup can
// align a before/after pair without re-parsing the patch.
type CommitDiffLine struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	OldNo int    `json:"old_no,omitempty"`
	NewNo int    `json:"new_no,omitempty"`
}

// FileSnapshot is one side of a file's before/after comparison.
type FileSnapshot struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
}

// commitDiffTimeout bounds a single git show. The old review pipeline already
// uses a context; the explorer is interactive, so it needs the same courtesy.
const commitDiffTimeout = 15 * time.Second

var commitSHARe = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// searchRepoPaths resolves the scope to repository paths stored in the DB.
// A repo id is only honoured when it really exists; an arbitrary path from the
// UI can never reach git. Passing repoID=0 means "the whole project".
func (s *Service) searchRepoPaths(projectID, repoID int64) ([]string, error) {
	if repoID > 0 {
		repo, err := db.GetRepositoryByID(s.db, repoID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrRepoNotFound
			}
			return nil, fmt.Errorf("load repository %d: %w", repoID, err)
		}
		return []string{repo.Path}, nil
	}
	repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
	if err != nil {
		return nil, fmt.Errorf("load repositories for project %d: %w", projectID, err)
	}
	paths := make([]string, 0, len(repos))
	for _, r := range repos {
		paths = append(paths, r.Path)
	}
	return paths, nil
}

// SearchCommits returns a page of commits matching query across a project or a
// single repository. An empty query means "all commits", which makes the same
// binding serve both the initial log and every subsequent search.
func (s *Service) SearchCommits(projectID, repoID int64, query string, offset, limit int) (*CommitPage, error) {
	paths, err := s.searchRepoPaths(projectID, repoID)
	if err != nil {
		return nil, err
	}
	items, total, err := stats.SearchCommits(paths, query, offset, limit)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []stats.RecentCommit{}
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return &CommitPage{Items: items, Total: total, Offset: offset, Limit: limit}, nil
}

// GetCommitDiff reads one commit's patch through the same git CLI + unified
// parser the review pipeline uses. Never accepts a path: the repository is
// looked up by id, so a forged request cannot point git at another directory.
func (s *Service) GetCommitDiff(repoID int64, sha string) (*CommitDiff, error) {
	if !commitSHARe.MatchString(sha) {
		return nil, fmt.Errorf("invalid commit id")
	}
	repo, err := db.GetRepositoryByID(s.db, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRepoNotFound
		}
		return nil, fmt.Errorf("load repository %d: %w", repoID, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), commitDiffTimeout)
	defer cancel()
	changes, err := review.CommitPatch(ctx, repo.Path, sha)
	if err != nil {
		return nil, err
	}
	out := &CommitDiff{SHA: sha, Repo: repo.Path, Files: make([]CommitDiffFile, 0, len(changes))}
	for _, fc := range changes {
		file := CommitDiffFile{
			Path:    fc.Path,
			OldPath: fc.OldPath,
			Status:  fc.Status,
			Binary:  fc.Binary,
			Hunks:   make([]CommitDiffHunk, 0, len(fc.Hunks)),
		}
		for _, h := range fc.Hunks {
			hunk := CommitDiffHunk{
				Header:   h.Header,
				OldStart: h.OldStart,
				NewStart: h.NewStart,
				Lines:    make([]CommitDiffLine, 0, len(h.Lines)),
			}
			oldNo, newNo := h.OldStart, h.NewStart
			for _, l := range h.Lines {
				line := CommitDiffLine{Kind: "context", Text: l.Text, OldNo: oldNo, NewNo: newNo}
				switch l.Kind {
				case '+':
					line.Kind = "add"
					file.Added++
					newNo++
				case '-':
					line.Kind = "del"
					file.Deleted++
					oldNo++
				default:
					oldNo++
					newNo++
				}
				hunk.Lines = append(hunk.Lines, line)
			}
			file.Hunks = append(file.Hunks, hunk)
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

// GetCommitFile reads one side of a file's before/after comparison. before=true
// reads the parent commit; a root commit has no parent and returns Exists=false
// instead of an error, which the UI renders as "(file created)".
func (s *Service) GetCommitFile(repoID int64, sha, filePath string, before bool) (*FileSnapshot, error) {
	if !commitSHARe.MatchString(sha) {
		return nil, fmt.Errorf("invalid commit id")
	}
	cleaned := path.Clean(strings.ReplaceAll(filePath, "\\", "/"))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "..") {
		return nil, fmt.Errorf("invalid file path")
	}
	repo, err := db.GetRepositoryByID(s.db, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRepoNotFound
		}
		return nil, fmt.Errorf("load repository %d: %w", repoID, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), commitDiffTimeout)
	defer cancel()
	ref := sha
	if before {
		ref += "^"
	}
	content, err := review.FileAtCommit(ctx, repo.Path, ref, cleaned)
	if err != nil {
		if before {
			return &FileSnapshot{Path: cleaned, Content: "", Exists: false}, nil
		}
		return nil, err
	}
	return &FileSnapshot{Path: cleaned, Content: content, Exists: true}, nil
}
