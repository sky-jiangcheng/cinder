package service

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/domain"
)

// Workspace path resolution tests (ADR-0009 thin client; the four tiers).
//
// The fixtures use POSIX-style absolute paths because that is what the matcher
// compares. On macOS/Linux filepath.Separator is "/" so these run as written;
// the rules under test are separator-generic (isPathAncestor joins with the
// platform separator), not string-prefix hacks.

func addRepo(t *testing.T, database *sql.DB, projectID int64, path string) int64 {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := db.UpsertRepositoryTx(tx, path, projectID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("upsert repo %s: %v", path, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var id int64
	if err := database.QueryRow("SELECT id FROM repositories WHERE path = ?", path).Scan(&id); err != nil {
		t.Fatalf("load repo id: %v", err)
	}
	return id
}

// addProject inserts a project row directly; seedProject only covers the
// non-auto-grouped case and these tests need both kinds.
func addProject(t *testing.T, database *sql.DB, name, rootPath string, autoGrouped bool) int64 {
	t.Helper()
	res, err := database.Exec(
		`INSERT INTO projects(name, root_path, is_auto_grouped) VALUES (?,?,?)`,
		name, rootPath, boolToInt(autoGrouped))
	if err != nil {
		t.Fatalf("insert project %s: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// workspaceFixture reproduces the shape that motivated the change: a collected
// multi-repository project (CBiPay with 11 modules) sitting under an auto-grouped
// catch-all rooted at the home directory.
type workspaceFixture struct {
	project   int64
	catchAll  int64
	repo      int64 // <project>/pay-common
	repoID    int64
	nested    string // <project>/pay-common/src/main
	homeRoot  string
	projectPt string
}

func newWorkspaceFixture(t *testing.T, svc *Service) workspaceFixture {
	t.Helper()
	home := "/Users/dev"
	projectPt := home + "/Workspace/Java/CBIBank/CBiPay"
	repo := projectPt + "/pay-common"
	nested := repo + "/src/main"
	f := workspaceFixture{
		catchAll:  addProject(t, svc.db, "dev-home", home, true),
		project:   addProject(t, svc.db, "CBiPay", projectPt, false),
		homeRoot:  home,
		projectPt: projectPt,
		nested:    nested,
	}
	f.repoID = addRepo(t, svc.db, f.project, repo)
	f.repo = f.repoID
	// A sibling module, so "longest path wins" has something to beat.
	addRepo(t, svc.db, f.project, projectPt+"/pay-account")
	return f
}

// The four tiers, each asserted by the MatchedOn it reports — not just by which
// project came back, because "which project" is the same answer for tiers 1-4 and
// would let a wrong tier pass.
func TestResolveProjectByPath_Tiers(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	cases := []struct {
		name        string
		dir         string
		wantProject int64
		wantRepo    int64 // 0 = expect no repository scope
		wantHow     string
	}{
		{"tier1 repository exact", f.projectPt + "/pay-common", f.project, f.repoID, "repository-exact"},
		{"tier2 repository ancestor (module subdir)", f.nested, f.project, f.repoID, "repository-ancestor"},
		{"tier3 project exact", f.projectPt, f.project, 0, "project-exact"},
		{"tier4 project ancestor, no repo match", f.projectPt + "/docs/notes", f.project, 0, "project-ancestor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := svc.ResolveProjectByPath(tc.dir)
			if res == nil || res.Project == nil {
				t.Fatalf("no project resolved (candidates=%d, how=%q)", len(res.Candidates), res.MatchedOn)
			}
			if res.Project.ID != tc.wantProject {
				t.Fatalf("resolved project %d, want %d", res.Project.ID, tc.wantProject)
			}
			var gotRepo int64
			if res.Repository != nil {
				gotRepo = res.Repository.ID
			}
			if gotRepo != tc.wantRepo {
				t.Fatalf("repository scope = %d, want %d (how=%q)", gotRepo, tc.wantRepo, res.MatchedOn)
			}
			if res.MatchedOn != tc.wantHow {
				t.Fatalf("MatchedOn = %q, want %q", res.MatchedOn, tc.wantHow)
			}
		})
	}
}

// Tier 4's restriction, and the failure it prevents. The catch-all project is
// rooted at the home directory, so an unrestricted ancestor match would resolve
// EVERY folder under home to it — including a folder that belongs to no project at
// all. A wrong-but-confident answer is worse than "unresolved, pick one".
func TestResolveProjectByPath_AutoGroupedContainerCannotAdoptSubdirs(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	// A folder under home that is inside no collected project and no repository.
	orphan := filepath.Join(f.homeRoot, "Downloads", "scratch")
	res := svc.ResolveProjectByPath(orphan)
	if res.Project != nil {
		t.Fatalf("the home-directory container adopted an unrelated folder: project=%s how=%q",
			res.Project.Name, res.MatchedOn)
	}
	if res.MatchedOn != "unresolved" {
		t.Fatalf("MatchedOn = %q, want unresolved", res.MatchedOn)
	}
	if len(res.Candidates) == 0 {
		t.Fatal("an unresolved path must still hand back the catalog so the caller can choose")
	}

	// Opening the container EXACTLY is still a legitimate answer.
	exact := svc.ResolveProjectByPath(f.homeRoot)
	if exact.Project == nil || exact.Project.ID != f.catchAll {
		t.Fatalf("an exact match on a container should resolve, got %+v", exact.Project)
	}
	if exact.MatchedOn != "project-exact" {
		t.Fatalf("MatchedOn = %q, want project-exact", exact.MatchedOn)
	}
}

// Longest-wins among sibling repositories, so a nested repository inside another
// is preferred over its outer container.
func TestResolveProjectByPath_DeetestRepositoryWins(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	// A repository nested inside the pay-common module (vendored/submodule shape).
	inner := addRepo(t, svc.db, f.project, f.projectPt+"/pay-common/vendor/lib")

	res := svc.ResolveProjectByPath(f.projectPt + "/pay-common/vendor/lib/src")
	if res.Repository == nil || res.Repository.ID != inner {
		t.Fatalf("the deepest repository should win, got %+v", res.Repository)
	}
}

// Case folding is a fallback, not a rule: on a case-sensitive filesystem two
// paths differing in case are different directories, so an exact match must win
// whenever one exists.
func TestResolveProjectByPath_CaseFoldIsFallbackOnly(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	res := svc.ResolveProjectByPath(strings.ToUpper(f.projectPt) + "/PAY-COMMON")
	if res.Project == nil || res.Repository == nil {
		t.Fatalf("case-folded path should still resolve, got %+v", res)
	}
	if !strings.Contains(res.MatchedOn, "case-folded") {
		t.Fatalf("MatchedOn = %q, want it to admit the case fold", res.MatchedOn)
	}
}

// A repository whose project row is gone must not be reported as a resolution.
func TestResolveProjectByPath_OrphanRepositoryDoesNotResolve(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	orphanPath := "/Users/dev/elsewhere/loose-repo"
	orphanID := addRepo(t, svc.db, f.project, orphanPath)
	if _, err := svc.db.Exec("UPDATE repositories SET project_id = NULL WHERE id = ?", orphanID); err != nil {
		t.Fatalf("null the project: %v", err)
	}

	res := svc.ResolveProjectByPath(orphanPath)
	if res.Project != nil {
		t.Fatalf("an orphan repository must not resolve to a project, got %s", res.Project.Name)
	}
	if !strings.HasPrefix(res.MatchedOn, "repository-exact-orphan") {
		t.Fatalf("MatchedOn = %q, want the orphan marker so the caller can explain it", res.MatchedOn)
	}
	if len(res.Candidates) == 0 {
		t.Fatal("still needs a catalog to offer")
	}
}

// An empty or junk path must not be read as "the filesystem root" — that would
// make every ancestor rule match everything.
func TestResolveProjectByPath_EmptyAndJunk(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	for _, bad := range []string{"", "   ", "/", "."} {
		res := svc.ResolveProjectByPath(bad)
		if res == nil {
			t.Fatalf("nil resolution for %q", bad)
		}
		// Empty delegates to the name/id resolution (which may pick the sole
		// project); what it must never do is claim a repository scope.
		if res.Repository != nil {
			t.Fatalf("%q must not resolve to a repository, got %+v", bad, res.Repository)
		}
	}
	// A relative path is not an absolute workspace: it must not adopt anything by
	// ancestry, because the stored paths are absolute.
	rel := svc.ResolveProjectByPath("pay-common")
	if rel.Project != nil && strings.HasPrefix(rel.MatchedOn, "project-ancestor") {
		t.Fatalf("a relative path must not win by ancestry: %q", rel.MatchedOn)
	}
	_ = f
}

// The precedence the first cut got backwards: a caller that names a project must
// never be answered with the project its cwd happens to resolve to.
func TestResolveWorkspaceExplicitProjectWinsOverPath(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	other := addProject(t, svc.db, "Other", "/Users/dev/other", false)

	res := svc.ResolveWorkspace(formatInt(other), f.projectPt+"/pay-common")
	if res.Project == nil || res.Project.ID != other {
		t.Fatalf("the named project must win, got %+v", res.Project)
	}
	// And it must not carry a repository scope from a path it did not use.
	if res.Repository != nil {
		t.Fatalf("an unused path must not contribute a scope: %+v", res.Repository)
	}

	// With nothing named, the path decides.
	fallback := svc.ResolveWorkspace("", f.projectPt+"/pay-common")
	if fallback.Project == nil || fallback.Project.ID != f.project {
		t.Fatalf("path should resolve when no project is named, got %+v", fallback.Project)
	}
}

func formatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

// ------------------------------------------------------------------ scoping

// The rank-not-filter rule, asserted on the case that actually exists today:
// note_repositories is empty, so a filter would empty the context.
func TestScopeNotesToRepository_RanksWithoutFiltering(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	attached := mustCreateNote(t, svc, f.project, "written inside pay-common")
	elsewhere := mustCreateNote(t, svc, f.project, "written elsewhere")
	if err := db.AttachNoteToRepository(svc.db, attached, f.repoID); err != nil {
		t.Fatalf("attach: %v", err)
	}

	notes := []domain.Note{{ID: elsewhere, Content: "b"}, {ID: attached, Content: "a"}}
	near, other := svc.scopeNotesToRepository(notes, f.repoID)
	if len(near) != 1 || near[0].ID != attached {
		t.Fatalf("attached note should lead, got %+v", near)
	}
	if len(other) != 1 || other[0].ID != elsewhere {
		t.Fatalf("the rest must be preserved, not dropped, got %+v", other)
	}

	// No repository: everything stays put.
	n2, o2 := svc.scopeNotesToRepository(notes, 0)
	if len(n2) != 0 || len(o2) != 2 {
		t.Fatalf("without a repository scope nothing may be reordered, got near=%d other=%d", len(n2), len(o2))
	}
}

func mustCreateNote(t *testing.T, svc *Service, projectID int64, content string) int64 {
	t.Helper()
	n, err := db.CreateNoteEx(svc.db, projectID, content, content, "", "knowledge", "manual")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	return n.ID
}

// Search hits rank, never disappear — and todos are left alone, because the
// attribution table describes notes.
func TestRankHitsByRepositoryKeepsEverything(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	inRepo := mustCreateNote(t, svc, f.project, "repo note")
	if err := db.AttachNoteToRepository(svc.db, inRepo, f.repoID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	hits := []domain.SearchHit{
		{Type: "note", ID: mustCreateNote(t, svc, f.project, "other note")},
		{Type: "todo", ID: 42},
		{Type: "note", ID: inRepo},
	}
	got := svc.rankHitsByRepository(hits, f.repoID)
	if len(got) != 3 {
		t.Fatalf("ranking must not drop hits, got %d", len(got))
	}
	if got[0].ID != inRepo {
		t.Fatalf("the repository note should lead, got %+v", got)
	}
	if got[1].Type != "note" || got[2].Type != "todo" {
		t.Fatalf("relative order of the rest should be preserved: %+v", got)
	}
	// Unresolved scope returns the input untouched.
	same := svc.rankHitsByRepository(hits, 0)
	if same[0].ID != hits[0].ID {
		t.Fatal("repository id 0 must not reorder anything")
	}
}

// ------------------------------------------------------------------ handoff writer

// The writer note_repositories never had. Asserted end to end because the whole
// repository scope is dead without it.
func TestHandoffAttributesNoteToRepository(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	res, err := svc.CreateHandoffNote(HandoffInput{
		ProjectID:     f.project,
		Agent:         "vscode-extension",
		Summary:       "Wired the FTS5 rebuild path",
		Changes:       []string{"internal/db/search.go"},
		WorkspacePath: f.projectPt + "/pay-common/src",
	})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if res.RepositoryID != f.repoID {
		t.Fatalf("the handoff should report the repository it was attributed to, got %+v", res)
	}
	ids, err := db.NotesForRepository(svc.db, f.repoID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(ids) != 1 || ids[0] != res.NoteID {
		t.Fatalf("note_repositories should hold the new note, got %v", ids)
	}

	// A path outside any repository must not fail the handoff and must not
	// attribute anything.
	plain, err := svc.CreateHandoffNote(HandoffInput{
		ProjectID: f.project, Agent: "x", Summary: "second", Changes: []string{"y"},
		WorkspacePath: "/tmp/not-scanned",
	})
	if err != nil {
		t.Fatalf("handoff without a repo should still succeed: %v", err)
	}
	if plain.RepositoryID != 0 {
		t.Fatalf("nothing should be attributed for an unmatched path, got %+v", plain)
	}
	ids, _ = db.NotesForRepository(svc.db, f.repoID)
	if len(ids) != 1 {
		t.Fatalf("the unmatched handoff leaked an attribution: %v", ids)
	}
}

// A path that resolves to a DIFFERENT project than the handoff claims must not
// rewrite the attribution — the note belongs to the project the caller named.
func TestHandoffRefusesCrossProjectAttribution(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	other := addProject(t, svc.db, "Other", "/Users/dev/other", false)

	res, err := svc.CreateHandoffNote(HandoffInput{
		ProjectID: other, Agent: "x", Summary: "s", Changes: []string{"c"},
		WorkspacePath: f.projectPt + "/pay-common",
	})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if res.RepositoryID != 0 {
		t.Fatalf("a repository from another project must not be attributed, got %+v", res)
	}
	ids, _ := db.NotesForRepository(svc.db, f.repoID)
	if len(ids) != 0 {
		t.Fatalf("cross-project attribution leaked: %v", ids)
	}
}

// ------------------------------------------------------------------ mining scope

// The multi-repository fix: with a repository resolved, mining and commits come
// from THAT repository, and a foreign repository id is refused rather than
// silently substituted.
func TestGetRepositoryOverviewNarrowsAndRefuses(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	ov, err := svc.GetRepositoryOverview(f.project, f.repoID)
	if err != nil {
		t.Fatalf("repository overview: %v", err)
	}
	if ov == nil {
		t.Fatal("nil overview")
	}
	// A repository that belongs to another project must not be answered with this
	// project's first repository.
	foreign := addRepo(t, svc.db, f.catchAll, "/Users/dev/somewhere-else")
	if _, err := svc.GetRepositoryOverview(f.project, foreign); err == nil {
		t.Fatal("a foreign repository id must be refused, not substituted")
	} else if !strings.Contains(err.Error(), "not part of project") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ------------------------------------------------------------------ context output

// The rendered context must SAY what it scoped to. An unannounced narrowing is
// indistinguishable from a broken tool — which is the complaint that started this.
func TestBuildProjectContextAnnouncesItsScope(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	mustCreateNote(t, svc, f.project, "a project note")

	res := svc.ResolveProjectByPath(f.projectPt + "/pay-common")
	out := svc.BuildProjectContext(res)
	if !strings.Contains(out, "pay-common") {
		t.Fatalf("the context should name the repository it scoped to:\n%s", out)
	}
	if !strings.Contains(out, "_Scope: repository `pay-common`") {
		t.Fatalf("missing the scope line:\n%s", out)
	}
	// With nothing attributed yet, it must say the notes are unfiltered rather than
	// implying a scope it could not honour.
	if !strings.Contains(out, "unfiltered") {
		t.Fatalf("expected the honest empty-scope wording:\n%s", out)
	}
}

// ------------------------------------------------------------------- binding

// Binding a workspace folder to a project (ADR-0044 decision 6).
//
// The whole point of the binding is that the NEXT resolution hits on its own:
// the folder becomes a repository of that project, and tier 1 (repository-exact)
// fires. If the binding returned a resolution that only the binding path could
// produce, the panel would have to keep a second truth — and that is the
// disagreement ADR-0009 exists to prevent.
func TestBindRepositoryToProject_MakesTheNextResolutionHit(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	// A folder that belongs to no project at all: not under CBiPay, and the
	// auto-grouped catch-all rooted at home may not adopt it (tier 4 is
	// restricted), so this really is unresolved before the binding.
	loose := f.homeRoot + "/elsewhere/pay-loose"

	// Before: the folder belongs to no project.
	if res := svc.ResolveProjectByPath(loose); res.Project != nil {
		t.Fatalf("precondition: %s should not resolve, got project %d", loose, res.Project.ID)
	}

	res, err := svc.BindRepositoryToProject(loose, f.project)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if res == nil || res.Project == nil || res.Project.ID != f.project {
		t.Fatalf("binding should resolve to project %d, got %+v", f.project, res)
	}
	if res.MatchedOn != "repository-exact" {
		t.Fatalf("MatchedOn = %q, want repository-exact", res.MatchedOn)
	}

	// And the resolution on its own — not the binding's return value — must hit.
	again := svc.ResolveProjectByPath(loose)
	if again.Project == nil || again.Project.ID != f.project {
		t.Fatalf("the folder should now resolve on its own, got %+v", again)
	}
	if again.MatchedOn != "repository-exact" {
		t.Fatalf("MatchedOn = %q, want repository-exact", again.MatchedOn)
	}
}

// Re-binding the same folder must MOVE it, not add a second row: two
// repository rows for one path would make the resolution depend on row order.
func TestBindRepositoryToProject_MovesTheFolderInsteadOfDuplicating(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)
	other := addProject(t, svc.db, "CBiPay-Other", f.projectPt+"-other", false)
	loose := f.homeRoot + "/elsewhere/pay-loose"

	if _, err := svc.BindRepositoryToProject(loose, f.project); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if _, err := svc.BindRepositoryToProject(loose, other); err != nil {
		t.Fatalf("second bind: %v", err)
	}

	repos, err := db.GetAllRepositories(svc.db)
	if err != nil {
		t.Fatalf("load repositories: %v", err)
	}
	// Compare NORMALISED paths on both sides. The binding writes
	// db.NormalizeMatchPath(path), which is filepath.Clean — on Windows that
	// turns "/Users/dev/..." into "\Users\dev\...", so a raw string comparison
	// against the fixture finds nothing there. Normalising both sides also
	// makes the assertion catch the case it is really about: a SECOND row for
	// the same folder written with a different separator.
	want := db.NormalizeMatchPath(loose)
	count := 0
	for _, r := range repos {
		if db.NormalizeMatchPath(r.Path) == want {
			count++
			if r.ProjectID == nil || *r.ProjectID != other {
				t.Fatalf("the folder should now belong to %d, got %+v", other, r.ProjectID)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one repository row for %s, got %d", loose, count)
	}
}

// The refusals: an unknown project, an empty path and a zero id must all be
// refused rather than silently filed under something.
func TestBindRepositoryToProject_RefusesBadInput(t *testing.T) {
	svc, _ := setupService(t)
	f := newWorkspaceFixture(t, svc)

	if _, err := svc.BindRepositoryToProject(f.projectPt+"/x", 0); err == nil {
		t.Fatal("a zero project id must be refused")
	}
	if _, err := svc.BindRepositoryToProject(f.projectPt+"/x", 999999); err == nil {
		t.Fatal("an unknown project id must be refused")
	}
	if _, err := svc.BindRepositoryToProject("   ", f.project); err == nil {
		t.Fatal("an empty path must be refused")
	}

	// A refused binding must not have written anything.
	repos, err := db.GetAllRepositories(svc.db)
	if err != nil {
		t.Fatalf("load repositories: %v", err)
	}
	// Normalised on both sides, for the same reason as above: the row a
	// successful binding WOULD have written is the normalised path, so the
	// refusal has to be checked against that form.
	refused := db.NormalizeMatchPath(f.projectPt + "/x")
	for _, r := range repos {
		if db.NormalizeMatchPath(r.Path) == refused {
			t.Fatalf("a refused binding must not write a repository row")
		}
	}
}
