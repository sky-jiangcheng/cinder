package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"repotory/internal/db"
	"repotory/internal/domain"
)

// Workspace path resolution (ADR-0009 thin client, and the same need for agents).
//
// The question this answers is the one an IDE and an agent both ask and the
// backend could not answer before: "I am standing in this directory — which
// project is mine?" ResolveProject matches a needle INSIDE a project's root_path,
// so a subdirectory — the normal case for a multi-repository project — never
// matches, and the repositories table, which does record the subdirectory, was
// never consulted at all.
//
// Four tiers, most specific first. The ordering is the whole design:
//
//	1. a repository stored at exactly this path            → that repository's project
//	2. the deepest repository path that is an ancestor     → that repository's project
//	3. a project rooted at exactly this path               → that project
//	4. the deepest project root_path that is an ancestor   → that project
//
// Tiers 1-2 beat 3-4 because a repository match carries more information: it
// names the module, not just the container.
//
// Tier 4 is restricted, and that restriction is load-bearing rather than
// defensive. This database contains an auto-grouped project rooted at the user's
// home directory (a scan-time catch-all container). Letting containers win by
// ancestry means every folder under home resolves to it, and the session gets the
// wrong project's knowledge with no signal that anything was guessed — strictly
// worse than "unresolved, pick one". An auto-grouped project may still match
// EXACTLY (tier 3): deliberately opening that folder is a real answer.
//
// Nothing here guesses across a mismatch. If no tier fires, the caller gets the
// candidate catalog plus an explicit reason, which is the same rule
// renderProjectCatalog already applies for an ambiguous name.

// ResolveProjectByPath resolves an absolute directory (an IDE workspace folder, or
// an agent's cwd) to a project and, when the directory sits inside a scanned
// repository, to that repository.
func (s *Service) ResolveProjectByPath(dir string) *ProjectResolution {
	queried := db.NormalizeMatchPath(dir)
	if queried == "" {
		// No path is not "the root of the filesystem": fall back to the existing
		// name/id resolution so an empty argument cannot resolve everything.
		return s.ResolveProject("")
	}
	res := &ProjectResolution{WorkspacePath: queried}

	repos, err := db.GetAllRepositories(s.db)
	if err != nil {
		res.Candidates, _ = db.GetAllProjects(s.db)
		return res
	}
	projects, err := db.GetAllProjects(s.db)
	if err != nil {
		res.Candidates = nil
		return res
	}
	if len(projects) == 0 {
		return res // empty database; the caller renders the "run repotory_scan" hint
	}

	// Tiers 1-2: repository first, exact before ancestor. The matchers live in
	// internal/db so the service and any db-only caller agree by construction.
	repo, repoHow, err := db.PickRepositoryForPath(repos, queried)
	if err != nil {
		return res
	}
	if repo != nil {
		if repo.ProjectID != nil && *repo.ProjectID > 0 {
			for i := range projects {
				if projects[i].ID == *repo.ProjectID {
					res.Project = &projects[i]
					res.Repository = repo
					res.MatchedOn = "repository-" + repoHow
					return res
				}
			}
		}
		// A repository row whose project is gone (or was never set) is a real fact
		// but not an answer on its own — notes and todos hang off a project. Keep the
		// repository for display and let the project tiers try.
		res.Repository = repo
		res.MatchedOn = "repository-" + repoHow + "-orphan"
	}

	// Tiers 3-4. allowCatchAll=false is the container restriction.
	project, projHow, err := db.PickProjectForPath(projects, queried, false)
	if err != nil {
		return res
	}
	if project != nil {
		res.Project = project
		if res.Repository != nil && res.Repository.ProjectID != nil && *res.Repository.ProjectID == project.ID {
			res.MatchedOn = "repository-" + repoHow + "+project-" + projHow
		} else {
			if res.Repository != nil {
				// The repository belongs to a different project than the path's owner:
				// report the project and drop the misleading repository scope.
				res.Repository = nil
			}
			res.MatchedOn = "project-" + projHow
		}
		res.Candidates = nil
		return res
	}

	// Nothing resolved to a project. Candidates plus an explicit reason, never a
	// silent best guess.
	res.Project = nil
	res.Candidates = projects
	if res.Repository != nil {
		// Keep the orphan marker rather than overwriting it with "unresolved":
		// "this folder IS a scanned repository whose project row is gone" is a
		// different and far more actionable diagnosis than "never seen this folder",
		// and the caller cannot re-derive it after the fact. The repository scope is
		// kept for the same reason — mining and note scoping still need a project, so
		// the caller must not treat it as resolved.
		res.MatchedOn = "repository-" + repoHow + "-orphan"
		return res
	}
	res.Repository = nil
	res.MatchedOn = "unresolved"
	return res
}

// BindRepositoryToProject files a workspace folder under a project.
//
// ADR-0044 decision 6. The resolution above is READ-ONLY and it is the right
// shape for an agent: "here is what this folder resolves to". But an IDE
// panel that cannot resolve its workspace had nowhere to go next — the chip
// was disabled and the user was told to go scan the project somewhere else.
//
// This is that next step, and it writes exactly one thing: the workspace
// folder becomes a repository row pointing at the chosen project. The next
// resolution then hits tier 1 (repository-exact) on its own — the binding is
// not remembered anywhere else, and there is no second matcher to keep in
// sync.
//
// The write is deliberately narrow: it files a DIRECTORY under a project, it
// does not move knowledge, and it never touches notes. Which project a
// workspace belongs to is a human decision, which is why this has no MCP twin
// (the same reasoning as MoveNote).
func (s *Service) BindRepositoryToProject(path string, projectID int64) (*ProjectResolution, error) {
	dir := db.NormalizeMatchPath(path)
	if dir == "" {
		return nil, fmt.Errorf("a workspace path is required")
	}
	if projectID <= 0 {
		return nil, fmt.Errorf("a project id is required")
	}
	projects, err := db.GetAllProjects(s.db)
	if err != nil {
		return nil, err
	}
	known := false
	for i := range projects {
		if projects[i].ID == projectID {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("project %d does not exist", projectID)
	}
	if err := db.UpsertRepository(s.db, dir, projectID); err != nil {
		return nil, err
	}
	// Resolve again so the caller gets the resolution the binding produced —
	// the caller shows this to the user, and a stale "unresolved" would be a lie.
	return s.ResolveProjectByPath(dir), nil
}

// repositoryScopeID is the id to narrow mining and notes to, or 0 when the
// resolution carries no repository. Centralised so context building and search
// cannot disagree about what "scoped" means.
func repositoryScopeID(res *ProjectResolution) int64 {
	if res == nil || res.Repository == nil {
		return 0
	}
	return res.Repository.ID
}

// ------------------------------------------------------------------ note scoping

// scopeNotesToRepository orders a project's notes by their attachment to one
// repository.
//
// It deliberately NARROWS BY RANK, NOT BY FILTER. note_repositories had no writer
// at all until this change, so a strict filter would return zero notes for every
// existing project and read as "this module has no knowledge" — a wrong answer
// produced by a correct-looking query. Everything still appears; what changes is
// that notes recorded while standing in this repository lead, and the caller
// states which scope it used.
func (s *Service) scopeNotesToRepository(notes []domain.Note, repositoryID int64) (near, other []domain.Note) {
	if repositoryID <= 0 {
		return nil, notes
	}
	ids, err := db.NotesForRepository(s.db, repositoryID)
	if err != nil {
		return nil, notes // a read failure must not cost the caller their notes
	}
	inRepo := make(map[int64]bool, len(ids))
	for _, id := range ids {
		inRepo[id] = true
	}
	for _, n := range notes {
		if inRepo[n.ID] {
			near = append(near, n)
		} else {
			other = append(other, n)
		}
	}
	return near, other
}

// attachNoteToRepository records which repository a note was written inside.
// Called from the handoff and capture paths when the caller supplied a workspace
// path that resolved to a repository.
//
// Failing here never fails the handoff: the note exists and is useful at project
// scope; losing the finer attribution is a degradation the user should see in the
// result, not an error that discards their session record.
func (s *Service) attachNoteToRepository(noteID, repositoryID int64) error {
	if noteID <= 0 || repositoryID <= 0 {
		return nil
	}
	return db.AttachNoteToRepositoryByID(s.db, noteID, repositoryID)
}

// rankHitsByRepository re-orders search hits so notes recorded inside one
// repository lead. Same rank-not-filter rule as scopeNotesToRepository, and the
// sort is STABLE: relevance ordering inside each group is left exactly as the
// search produced it, so this can never silently reorder bm25 results.
//
// Todo hits are carried along untouched — note_repositories attributes notes, not
// todos, so pretending to scope todos would drop them.
func (s *Service) rankHitsByRepository(hits []domain.SearchHit, repositoryID int64) []domain.SearchHit {
	if repositoryID <= 0 || len(hits) == 0 {
		return hits
	}
	ids, err := db.NotesForRepository(s.db, repositoryID)
	if err != nil || len(ids) == 0 {
		return hits
	}
	inRepo := make(map[int64]bool, len(ids))
	for _, id := range ids {
		inRepo[id] = true
	}
	near := make([]domain.SearchHit, 0, len(hits))
	rest := make([]domain.SearchHit, 0, len(hits))
	for _, h := range hits {
		if h.Type == "note" && inRepo[h.ID] {
			near = append(near, h)
		} else {
			rest = append(rest, h)
		}
	}
	return append(near, rest...)
}

// ResolveWorkspace is the single entry point both the MCP tools and the desktop
// bindings use: a name/id query first, and a workspace path when the name lookup
// could not decide. Keeping one entry point is what stops the agent path and the
// RankHitsByWorkspace resolves a workspace path and ranks notes recorded in the
// repository it lands inside. An unresolved path returns the hits untouched: a
// caller that passes a folder the database has never seen gets the ordinary
// search, not an empty or reordered surprise.
func (s *Service) RankHitsByWorkspace(hits []domain.SearchHit, path string) []domain.SearchHit {
	if strings.TrimSpace(path) == "" {
		return hits
	}
	return s.rankHitsByRepository(hits, repositoryScopeID(s.ResolveProjectByPath(path)))
}

// ResolveWorkspace is the single entry point the MCP tools share, so an agent and
// the IDE cannot disagree about which project a folder belongs to.
//
// An explicit project_id / project_name ALWAYS wins, and the path is consulted
// only when the name or id lookup could not decide. That precedence is the point,
// and it was inverted in the first cut of this function: answering a caller's
// named project with whatever its cwd happens to resolve to is a silent
// substitution — the caller asks for project 7 and gets the knowledge of whatever
// repository it happens to be standing in. The same rule already governed
// repotory_handoff, so aligning here also made the two tools agree.
func (s *Service) ResolveWorkspace(query, path string) *ProjectResolution {
	res := s.ResolveProject(query)
	if res != nil && res.Project != nil {
		return res
	}
	if strings.TrimSpace(path) != "" {
		if byPath := s.ResolveProjectByPath(path); byPath != nil && byPath.Project != nil {
			return byPath
		}
	}
	return res
}

// workspaceScopeLine is the sentence the context document and the search output
// print so the reader knows what was actually matched. An unannounced narrowing
// is indistinguishable from a broken tool.
func workspaceScopeLine(res *ProjectResolution, near, other int) string {
	if res == nil || res.Repository == nil {
		return ""
	}
	repoName := filepath.Base(res.Repository.Path)
	how := res.MatchedOn
	if how == "" {
		how = "path"
	}
	if near == 0 {
		return fmt.Sprintf("_Scope: repository `%s` (matched by %s). No note is attributed to it yet, "+
			"so the %d project notes below are unfiltered; handoffs recorded from this folder will "+
			"lead next time._\n\n", repoName, how, other)
	}
	return fmt.Sprintf("_Scope: repository `%s` (matched by %s). %d note(s) recorded in this repository "+
		"lead; %d further project notes follow._\n\n", repoName, how, near, other)
}
