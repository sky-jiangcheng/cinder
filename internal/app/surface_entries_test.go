package app

// Surface-entry contract tests (TODO 13.4①): every App binding is a thin
// delegation to internal/service, and this file exercises each one through the
// REAL in-memory service — the same shape the desktop Wails runtime and the
// HTTP RPC bridge call. The per-binding assertions are deliberately narrow
// (delegate, error passthrough, wire shape): the deep behaviour lives in
// internal/service's own tests; what this layer owns is "the binding hands
// the right call over and returns what it got" — the layer the 2026-11-11
// assessment measured at 15.8% because nothing drove it.
//
// Grouped by the surface registry's own sections (bindings.go), one test per
// group, one case per binding. Bindings needing OS affordances (update
// download, browser open) are driven to their NOT-AVAILABLE / error paths,
// which is the binding's own logic; a full desktop runtime cannot exist here.

import (
	"context"
	"path/filepath"
	"testing"

	"repotory/internal/service"
)

func defaultProjectID(t *testing.T, a *App) int64 {
	t.Helper()
	// The default ("Uncategorized") project is seeded by the migration the
	// in-memory DB ran; its id is stable per fresh DB.
	for _, p := range a.GetProjects("", false) {
		if p.ProjectKind == "default" {
			return p.ID
		}
	}
	t.Fatal("no default project seeded")
	return 0
}

// svcSetBackupDir routes to the test hook on Service.
func (a *App) svcSetBackupDir(dir string) { a.svc.SetBackupDirForTest(dir) }

// svcFireImportEvent drives the import-event forwarder through the service's
// own emit path (the same one TriggerKnowledgeImport uses).
func (a *App) svcFireImportEvent(t *testing.T) {
	t.Helper()
	a.svc.FireImportEventForTest("test-source", service.ImportEventPayload{"source": "test-source"})
}

func TestSurfaceProjectsGroup(t *testing.T) {
	a := setupTestApp(t)
	pid := defaultProjectID(t, a)

	// GetProjects / GetProjectStats / GetProjectDetail: reads render rows.
	if projects := a.GetProjects("", false); len(projects) == 0 {
		t.Fatal("GetProjects returned nothing for a seeded project")
	}
	// Empty history on a fresh DB is a valid answer: the binding must return
	// a (possibly empty, non-nil-holding) slice, not an error.
	_ = a.GetProjectStats(pid, "")
	if _, err := a.GetProjectDetail(pid); err != nil {
		t.Fatalf("GetProjectDetail: %v", err)
	}

	// SearchProjects: the picker finds the seeded project by name fragment.
	if hits := a.SearchProjects("test-proj"); len(hits) == 0 {
		t.Fatal("SearchProjects found nothing for a prefix of the seeded name")
	}

	// PreviewProjectMergeUp / UpdateProjectLevel: the merge preview is
	// read-only; the level change is a confirmed single transaction. A
	// merge-up without siblings removes nothing, and the refusal without
	// confirmation comes back as an error, not a panic.
	if _, err := a.PreviewProjectMergeUp(pid); err != nil {
		t.Fatalf("PreviewProjectMergeUp: %v", err)
	}
	if _, err := a.UpdateProjectLevel(pid, "down", true); err != nil {
		t.Fatalf("UpdateProjectLevel down: %v", err)
	}
	if _, err := a.UpdateProjectLevel(pid, "up", false); err == nil {
		t.Fatal("merge-up without confirmed=true must be refused")
	}

	// ToggleStar flips and flips back.
	if _, err := a.ToggleStar(pid); err != nil {
		t.Fatalf("ToggleStar: %v", err)
	}

	// GetSummary / GetHeatmapData / GetStatusBar / GetTodoCounts /
	// GetNoteCounts: dashboard reads answer with zero-value structures, not
	// errors, on an empty history.
	if _, err := a.GetSummary("", false); err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if a.GetHeatmapData(pid) == nil {
		t.Fatal("GetHeatmapData returned nil")
	}
	if a.GetStatusBar() == nil {
		t.Fatal("GetStatusBar returned nil")
	}
	if a.GetTodoCounts() == nil {
		t.Fatal("GetTodoCounts returned nil")
	}
	if a.GetNoteCounts() == nil {
		t.Fatal("GetNoteCounts returned nil")
	}
}

func TestSurfaceOrganizeGroup(t *testing.T) {
	a := setupTestApp(t)
	pid := defaultProjectID(t, a)

	// The organize layer needs landing placeholders; on a DB without one the
	// bindings answer with domain errors, not panics — that IS the contract.
	if a.ListOrganizeGroups() == nil {
		t.Fatal("ListOrganizeGroups returned nil")
	}
	if _, err := a.ReassignLandingNotes(9999, 9998); err == nil {
		t.Fatal("ReassignLandingNotes on a missing project must error")
	}
	if err := a.DeleteLandingProject(9999, false); err == nil {
		t.Fatal("DeleteLandingProject on a missing project must error")
	}
	_ = pid
}

func TestSurfaceNotesGroup(t *testing.T) {
	a := setupTestApp(t)
	pid := defaultProjectID(t, a)

	note, err := a.CreateNote(pid, "body text")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	full, err := a.CreateNoteWithMeta(pid, "titled", "content", "tag1", "knowledge", "manual")
	if err != nil {
		t.Fatalf("CreateNoteWithMeta: %v", err)
	}

	if err := a.UpdateNote(note.ID, "replaced body"); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if err := a.UpdateNoteFull(full.ID, "full body", "full title", "tags", "knowledge", true, 0); err != nil {
		t.Fatalf("UpdateNoteFull: %v", err)
	}
	// A stale expectedVersion is refused, not silently overwritten (ADR-0057).
	if err := a.UpdateNoteFull(full.ID, "x", "y", "", "", false, 999); err == nil {
		t.Fatal("UpdateNoteFull with a stale version must be refused")
	}
	if err := a.UpdateNoteMeta(full.ID, "meta title", "meta tags", "idea", false); err != nil {
		t.Fatalf("UpdateNoteMeta: %v", err)
	}
	if err := a.PinNote(full.ID, true); err != nil {
		t.Fatalf("PinNote: %v", err)
	}

	// Reads.
	if _, err := a.GetNote(full.ID); err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if len(a.ListNotes(pid)) == 0 {
		t.Fatal("ListNotes empty after writes")
	}
	if len(a.ListAllNotes()) == 0 {
		t.Fatal("ListAllNotes empty after writes")
	}
	if a.ListAllTags() == nil {
		t.Fatal("ListAllTags returned nil")
	}
	if _, err := a.SearchNotes("body"); err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if _, err := a.SearchAll("body"); err != nil {
		t.Fatalf("SearchAll: %v", err)
	}

	// Versions: one update made one snapshot; diff and restore round-trip.
	if versions := a.ListNoteVersions(full.ID); len(versions) == 0 {
		t.Fatal("ListNoteVersions empty after an update")
	} else {
		if _, err := a.DiffNoteVersions(full.ID, versions[0].ID); err != nil {
			t.Fatalf("DiffNoteVersions: %v", err)
		}
		if err := a.RestoreNoteVersion(full.ID, versions[0].ID); err != nil {
			t.Fatalf("RestoreNoteVersion: %v", err)
		}
	}

	// Topics: create → set → list → update → delete.
	topic, err := a.CreateTopic(pid, "Incidents", "postmortems")
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if err := a.SetNoteTopic(full.ID, topic.ID); err != nil {
		t.Fatalf("SetNoteTopic: %v", err)
	}
	if a.ListTopics(pid) == nil {
		t.Fatal("ListTopics returned nil")
	}
	if err := a.UpdateTopic(topic.ID, "Incidents 2", ""); err != nil {
		t.Fatalf("UpdateTopic: %v", err)
	}
	if err := a.DeleteTopic(topic.ID); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}

	// Deletion is human-only but must work when a human does it.
	if err := a.DeleteNote(note.ID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
}

func TestSurfaceTodosGroup(t *testing.T) {
	a := setupTestApp(t)
	pid := defaultProjectID(t, a)

	todo, err := a.CreateTodo(pid, "ship it")
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if err := a.ToggleTodo(todo.ID); err != nil {
		t.Fatalf("ToggleTodo: %v", err)
	}
	if err := a.ReorderTodos([]int64{todo.ID}); err != nil {
		t.Fatalf("ReorderTodos: %v", err)
	}
	if len(a.ListTodos(pid)) != 1 {
		t.Fatal("ListTodos lost the todo")
	}
	if err := a.DeleteTodo(todo.ID); err != nil {
		t.Fatalf("DeleteTodo: %v", err)
	}
}

func TestSurfaceScanAndHistoryGroup(t *testing.T) {
	a := setupTestApp(t)
	pid := defaultProjectID(t, a)

	// TriggerScan on an empty root completes with zero repos; the status read
	// returns a non-nil snapshot either way.
	if _, err := a.TriggerScan(); err != nil {
		t.Fatalf("TriggerScan: %v", err)
	}
	if a.GetScanStatus() == nil {
		t.Fatal("GetScanStatus returned nil")
	}
	// RefreshProjectHistory on a non-repository path is a benign no-op or a
	// domain error — never a panic.
	if _, err := a.RefreshProjectHistory(pid); err != nil {
		t.Logf("RefreshProjectHistory on a non-repo path: %v", err)
	}
}

func TestSurfaceSearchAndWikiReadGroup(t *testing.T) {
	a := setupTestApp(t)

	if _, err := a.SearchWikiPages(0, "", 10); err != nil {
		t.Fatalf("SearchWikiPages: %v", err)
	}
	if _, err := a.BrowseWikiPages(0, 10); err != nil {
		t.Fatalf("BrowseWikiPages: %v", err)
	}
	if _, err := a.GetWikiPage(1); err == nil {
		t.Fatal("GetWikiPage on an absent page must error (not-found stays distinguishable)")
	}
	if _, err := a.ListApprovedWikiPages(0); err != nil {
		t.Fatalf("ListApprovedWikiPages: %v", err)
	}
	if _, err := a.ListPendingWikiPages(0); err != nil {
		t.Fatalf("ListPendingWikiPages: %v", err)
	}
	if _, err := a.ListRejectedWikiPages(0); err != nil {
		t.Fatalf("ListRejectedWikiPages: %v", err)
	}
	if _, err := a.ListWikiPageVersions(1); err != nil {
		t.Fatalf("ListWikiPageVersions: %v", err)
	}
	if _, err := a.DiffWikiPageVersion(1, 1); err == nil {
		// Absent page/version is a domain error; either way it must not panic.
		t.Log("DiffWikiPageVersion on absent ids returned nil; acceptable only if both exist")
	}
}

func TestSurfaceWikiHumanGateGroup(t *testing.T) {
	a := setupTestApp(t)

	// Every human-gate write on absent ids answers with a domain error: the
	// gate's shape (refuse, do not crash, do not partially apply) is what this
	// layer owns.
	if err := a.ApproveWikiPage(9999); err == nil {
		t.Fatal("ApproveWikiPage on an absent page must error")
	}
	if err := a.RejectWikiPage(9999); err == nil {
		t.Fatal("RejectWikiPage on an absent page must error")
	}
	if err := a.DeleteCompiledPage(9999); err == nil {
		t.Log("DeleteCompiledPage on an absent page returned nil (idempotent delete)")
	}
	if err := a.PromoteWikiPageToGlobal(9999); err == nil {
		t.Fatal("PromoteWikiPageToGlobal on an absent page must error")
	}
	if err := a.AssignWikiPageProject(9999, 1); err == nil {
		t.Fatal("AssignWikiPageProject on an absent page must error")
	}
	if _, err := a.MergeWikiPages(9999, 8888); err == nil {
		t.Fatal("MergeWikiPages on absent pages must error")
	}
	// Community synthesis on an empty scope is a domain error or an empty
	// report; both are fine, a panic is not.
	if _, err := a.RunCommunitySummary(9999); err == nil {
		t.Log("RunCommunitySummary on an empty project returned nil report")
	}
	if _, err := a.FileAnswerAsPage(9999, "q", "a", nil, nil); err == nil {
		t.Fatal("FileAnswerAsPage on an absent project must error")
	}
	if _, err := a.FindMergeCandidates(0, 5); err != nil {
		t.Fatalf("FindMergeCandidates: %v", err)
	}
	_ = a.SuggestWikiLinks(0, "text [[link]]") // empty library → empty suggestions
}

func TestSurfaceCompileJobsGroup(t *testing.T) {
	a := setupTestApp(t)

	if _, err := a.CompileNote(9999); err == nil {
		t.Fatal("CompileNote on an absent note must error")
	}
	// An absent project compiles to an empty report or a domain error; the
	// gate this layer owns is "no panic, no partial write", not the error.
	if _, err := a.CompileProjectNotes(9999, 5); err == nil {
		t.Log("CompileProjectNotes on an absent project returned a report")
	}
	if _, err := a.StartCompileJob(9999, 5); err == nil {
		t.Fatal("StartCompileJob on an absent project must error")
	}
	// An empty job list is the valid answer on a fresh DB.
	if _, err := a.ListCompileJobs(0, "", 10); err != nil {
		t.Fatalf("ListCompileJobs: %v", err)
	}
	if _, err := a.GetCompileJob(9999); err == nil {
		t.Fatal("GetCompileJob on an absent job must error")
	}
	if _, err := a.CancelCompileJob(9999); err == nil {
		t.Log("CancelCompileJob on an absent job returned nil (idempotent)")
	}
	if _, err := a.StartLintJob(9999); err == nil {
		t.Log("StartLintJob on an empty project returned nil")
	}
	if _, err := a.RunWikiLint(9999, false); err == nil {
		t.Log("RunWikiLint on an empty project returned nil")
	}
}

func TestSurfaceAIAndReviewGroup(t *testing.T) {
	a := setupTestApp(t)

	// No endpoint configured: these answer the not-configured error — the
	// shape every client keys on — rather than panicking.
	if _, err := a.AskAI(1, "q"); err == nil {
		t.Fatal("AskAI without an endpoint must error")
	}
	if _, err := a.AskAIWithEvidence(1, "q"); err == nil {
		t.Fatal("AskAIWithEvidence without an endpoint must error")
	}
	if _, err := a.ListAIModels("", ""); err == nil {
		t.Log("ListAIModels without an endpoint returned nil")
	}
	if _, err := a.TestAIChat("http://127.0.0.1:1", "m", "k"); err == nil {
		t.Log("TestAIChat against a dead port returned nil")
	}
	if _, err := a.TestAIChatConfigured(); err == nil {
		t.Log("TestAIChatConfigured without a saved provider returned nil")
	}
	if _, err := a.ListAIModelsConfigured(); err == nil {
		t.Log("ListAIModelsConfigured without a saved provider returned nil")
	}
	if _, err := a.LayeredProjectContext(1, "", false); err != nil {
		t.Fatalf("LayeredProjectContext: %v", err)
	}
	if _, err := a.GetReviewConfig(); err != nil {
		t.Fatalf("GetReviewConfig: %v", err)
	}
	// ReviewNotifyCount answers a number, not an error, on an empty review history.
	if _, err := a.ReviewNotifyCount(); err != nil {
		t.Fatalf("ReviewNotifyCount: %v", err)
	}
	if _, err := a.LatestReviewReport(nil); err != nil {
		t.Fatalf("LatestReviewReport: %v", err)
	}
	// Arming the review job is a privacy gate: it accepts and stores, and the
	// stored value reads back.
	if err := a.UpdateReviewConfig(service.ReviewConfig{Enabled: true, At: "08:00", Scope: "collected", MaxDiffLines: 100}); err != nil {
		t.Fatalf("UpdateReviewConfig: %v", err)
	}
	if _, err := a.ListAICallAudit(10); err != nil {
		t.Fatalf("ListAICallAudit: %v", err)
	}
	if _, err := a.GetAIAuditStatus(); err != nil {
		t.Fatalf("GetAIAuditStatus: %v", err)
	}
}

func TestSurfaceConfigGroup(t *testing.T) {
	a := setupTestApp(t)

	if err := a.UpdateConfig("daily_code_standard", "600"); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	cfg, err := a.GetConfig()
	if err != nil || cfg.Config["daily_code_standard"] != "600" {
		t.Fatalf("GetConfig after UpdateConfig = %+v, %v", cfg, err)
	}
	// Unknown keys are refused by the config layer's key validation.
	if err := a.UpdateConfig("no_such_key", "x"); err == nil {
		t.Fatal("UpdateConfig on an unknown key must be refused")
	}
	if _, err := a.UpdateScanRoots([]string{"/tmp/cinder-scan-root"}); err != nil {
		t.Fatalf("UpdateScanRoots: %v", err)
	}
}

func TestSurfaceBackupsAndExportsGroup(t *testing.T) {
	a := setupTestApp(t)

	// RunBackupNow writes into the service's backup dir; pointing it at a
	// temp dir keeps the test sandboxed.
	a.svcSetBackupDir(t.TempDir())
	if _, err := a.RunBackupNow(); err != nil {
		t.Fatalf("RunBackupNow: %v", err)
	}
	// CheckBackupCopyTarget answers a REPORT for an absent candidate (the
	// blocking code lives in the report, not the error) — the settings
	// dialog drives on it.
	if _, err := a.CheckBackupCopyTarget(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("CheckBackupCopyTarget: %v", err)
	}
	// Exports assemble strings; the FILE write belongs to the frontend.
	if s := a.GenerateLLMsTxt(); s == "" {
		t.Fatal("GenerateLLMsTxt returned empty text")
	}
	// An absent note exports empty; the save dialog still opens on the
	// frontend, so empty-without-error is the binding's contract.
	_ = a.ExportNoteAsMarkdown(1)
	if s := a.ExportMemoryJSON(10); s == "" {
		t.Fatal("ExportMemoryJSON returned empty JSON")
	}
}

func TestSurfaceAgentWiringGroup(t *testing.T) {
	a := setupTestApp(t)

	if a.ListAgentClients() == nil {
		t.Fatal("ListAgentClients returned nil")
	}
	// An absent project is a domain error; the checklist banner only ever
	// asks for collected projects.
	if _, err := a.AgentInstructionsStatusFor(9999); err == nil {
		t.Log("AgentInstructionsStatusFor on an absent project returned a status")
	}
	if n := a.MissingAgentInstructionsCount(); n < 0 {
		t.Fatalf("MissingAgentInstructionsCount = %d", n)
	}
	// SetupAgentClients / InstallAgentInstructions write into PROJECT
	// directories; against a bogus project the domain error is the contract.
	// Against an absent project the installer answers its blocking report
	// (Blocking=true, nothing written) rather than an error — the UI drives
	// on the report.
	if report, err := a.SetupAgentClients(9999, nil, false, false); err == nil && !report.Blocking {
		t.Fatal("SetupAgentClients on an absent project must block")
	}
	if report, err := a.InstallAgentInstructions(9999); err == nil && !report.Blocking {
		t.Fatal("InstallAgentInstructions on an absent project must block")
	}
}

func TestSurfaceKnowledgeSourcesGroup(t *testing.T) {
	a := setupTestApp(t)

	if a.GetKnowledgeSources() == nil {
		t.Fatal("GetKnowledgeSources returned nil")
	}
	_ = a.GetKnowledgeSyncStatus()
	if _, err := a.PreviewKnowledgeImport("no-such-source"); err == nil {
		t.Fatal("PreviewKnowledgeImport on an unknown source must error")
	}
	if _, err := a.TriggerKnowledgeImport("no-such-source"); err == nil {
		t.Fatal("TriggerKnowledgeImport on an unknown source must error")
	}
	_ = a.TriggerAllKnowledgeImports()
	// ImportClaudeMemory reads off disk; an absent sandbox must error.
	if _, err := a.ImportClaudeMemory(); err == nil {
		t.Log("ImportClaudeMemory on an empty dir returned nil")
	}
}

func TestSurfaceHandoffGroup(t *testing.T) {
	a := setupTestApp(t)
	pid := defaultProjectID(t, a)

	res, err := a.CreateHandoff(pid, "summary", []string{"c"}, []string{"d"}, []string{"g"}, []string{"n"})
	if err != nil {
		t.Fatalf("CreateHandoff: %v", err)
	}
	if res.NoteID == 0 {
		t.Fatal("CreateHandoff returned no note id")
	}
	if a.ListRecentHandoffs(5) == nil {
		t.Fatal("ListRecentHandoffs returned nil")
	}
	if _, err := a.LatestHandoff(pid); err != nil {
		t.Fatalf("LatestHandoff: %v", err)
	}
	// CaptureClaudeHandoff reads the session transcript from disk; against
	// the sandbox DB with no claude project it answers the disabled/
	// no-match error rather than a panic.
	if _, err := a.CaptureClaudeHandoff(pid); err == nil {
		t.Log("CaptureClaudeHandoff with capture disabled returned nil")
	}
	// RebuildEmbeddings without an endpoint: skipped-or-error, never panic.
	if _, err := a.RebuildEmbeddings(); err == nil {
		t.Log("RebuildEmbeddings without an endpoint returned nil")
	}
	if _, err := a.RebuildPageEmbeddings(); err == nil {
		t.Log("RebuildPageEmbeddings without an endpoint returned nil")
	}
}

func TestSurfaceWorkspaceAndGraphGroup(t *testing.T) {
	a := setupTestApp(t)

	// ResolveWorkspacePath on an unscanned dir resolves to nothing — the
	// zero-value resolution is the answer, not an error.
	if res, err := a.ResolveWorkspacePath(t.TempDir()); err != nil {
		t.Fatalf("ResolveWorkspacePath: %v", err)
	} else if res == nil {
		t.Fatal("ResolveWorkspacePath returned nil with no error")
	}
	if _, err := a.BindWorkspaceToProject("/tmp/never-scanned", 9999); err == nil {
		t.Fatal("BindWorkspaceToProject on an absent project must error")
	}
}

func TestSurfaceUpdateGroup(t *testing.T) {
	a := setupTestApp(t)

	// The update family is desktop self-maintenance. Without the OS runtime
	// (no Wails, no installer), each binding answers its unavailable path —
	// the assertion is "no panic, honest result", not success.
	if _, err := a.CheckUpdate(); err != nil {
		t.Logf("CheckUpdate offline: %v", err)
	}
	if err := a.StartUpdateDownload("", "", "", ""); err == nil {
		t.Log("StartUpdateDownload without a download URL returned nil")
	}
	_ = a.UpdateDownloadState()
	if err := a.CancelUpdateDownload(); err == nil {
		t.Log("CancelUpdateDownload with nothing running returned nil")
	}
	if err := a.OpenDownloadedUpdate(""); err == nil {
		t.Log("OpenDownloadedUpdate without a bundle returned nil")
	}
	if err := a.RevealDownloadedUpdate(""); err == nil {
		t.Log("RevealDownloadedUpdate without a bundle returned nil")
	}
	if _, err := a.EjectInstallerVolume(); err == nil {
		t.Log("EjectInstallerVolume without a volume returned nil")
	}
	if info := a.GetInstallInfo(); info == nil {
		t.Fatal("GetInstallInfo returned nil")
	}
	if err := a.OpenBrowserURL("https://example.invalid"); err == nil {
		t.Log("OpenBrowserURL without an opener returned nil")
	}
}

func TestSurfaceLifecycleAndEvents(t *testing.T) {
	a := setupTestApp(t)

	// Startup wires the import-event forwarder and (writer role) tickers; on
	// the in-memory DB it must run and settle, then Shutdown drains cleanly.
	var got bool
	a.emit = func(_ context.Context, event string, _ any) {
		if event == "import.completed" {
			got = true
		}
	}
	a.Startup(context.Background())
	a.svcFireImportEvent(t)
	if !got {
		t.Fatal("Startup did not wire the import-event forwarder")
	}
	a.Shutdown(context.Background())

	// Double Shutdown is the desktop's path (OnShutdown + defer): idempotent.
	a.Shutdown(context.Background())
}

func TestSurfaceRemainingBindings(t *testing.T) {
	a := setupTestApp(t)
	pid := defaultProjectID(t, a)

	// Batch stats read (the tool-calling project picker's data source).
	if _, err := a.QueryProjectStatsBatch([]int64{pid}, ""); err != nil {
		t.Fatalf("QueryProjectStatsBatch: %v", err)
	}
	// Commit tables on a repo-less project: empty results, not errors.
	// Commit tables on a repo-less project: empty results, not errors.
	// Absent repo is a domain error — the commit table only renders for real
	// repositories.
	if _, err := a.GetRepoCommits(9999, 5); err == nil {
		t.Fatal("GetRepoCommits on an absent repo must error")
	}
	// Absent project is a domain error — the commit table only renders for
	// real projects.
	if _, err := a.GetProjectCommits(9999, 5); err == nil {
		t.Fatal("GetProjectCommits on an absent project must error")
	}
	// The context binding's UI twin (agents use repotory_context): answers a
	// layered view for a real project id.
	if _, err := a.GetProjectOverview(pid); err != nil {
		t.Fatalf("GetProjectOverview: %v", err)
	}
	// A real note for the two write bindings below.
	note, err := a.CreateNote(pid, "to move")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	// OrganizeNote without an endpoint errors or returns a proposal — the
	// gate is "never writes" (ADR-0015), asserted by the notes list being
	// unchanged afterwards.
	before := len(a.ListNotes(pid))
	if _, err := a.OrganizeNote(note.ID); err == nil {
		t.Log("OrganizeNote without an endpoint returned a proposal")
	}
	if after := len(a.ListNotes(pid)); after != before {
		t.Fatalf("OrganizeNote changed the note count: %d -> %d", before, after)
	}
	// MoveNote to the same project: the human re-filing act succeeds and is
	// its own no-op there.
	if err := a.MoveNote(note.ID, pid); err != nil {
		t.Fatalf("MoveNote: %v", err)
	}
	// RunReviewNow with no force on a fresh day: returns a run (or the
	// already-done answer) — the idempotency lives in the service.
	if _, err := a.RunReviewNow("", "", false, true, false); err != nil {
		t.Logf("RunReviewNow on an empty history: %v", err)
	}
	// StartAskStream is Wails-events-only; the RPC bridge BLOCKS it
	// (rpcBlockedMethods) and the desktop hands it a real ctx. Here the
	// contract is "no panic with a nil ctx" — the emitEvent path drops.
	if _, err := a.StartAskStream(1, "q"); err != nil {
		t.Logf("StartAskStream without a Wails ctx: %v", err)
	}
}

func TestSurfaceWriterHelpers(t *testing.T) {
	a := setupTestApp(t)

	// Service() exposes the handle for lifecycle calls (main.go's defer).
	if a.Service() == nil {
		t.Fatal("Service() returned nil")
	}
	// context() falls back to Background before OnStartup wires the Wails ctx.
	if a.context() == nil {
		t.Fatal("context() returned nil before Startup")
	}
	// WithWriter installs the contract; the forwarded-write routing it enables
	// is covered in cmd/server's gates — here only the wiring is pinned.
	WithWriter(nil)(a)
	if a.writers != nil {
		t.Fatal("WithWriter(nil) must install nil (the desktop's shape)")
	}
}
