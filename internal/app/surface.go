package app

// Binding surface registry (P32, machine-checked).
//
// Why this file exists. The audit of "which binding has an MCP twin, and which is
// deliberately desktop-only" used to live in a hand-maintained comment block dated
// 2026-10-02 at the top of bindings.go. The MCP side already had an automated pin
// (TestToolsRegistered asserts the exact tool list and its count); the binding
// side had prose that must be remembered. Every binding added since then meant
// editing a comment, and forgetting meant the next reader trusted a stale claim —
// which is the failure this repo has now hit repeatedly enough that it keeps
// writing it down.
//
// So: the classification is data here, and surface_test.go asserts it is
// COMPLETE (every exported *App method appears), CURRENT (no entry names a method
// that no longer exists), EXPLAINED (a desktop-only binding must carry a reason),
// and CONSISTENT with the HTTP blocklist. A new binding with no entry fails the
// build instead of silently drifting the documentation.
//
// Reading the categories:
//
//   - SurfaceMCPBacked — an agent can reach the same capability through the named
//     MCP tool. The tool name is recorded, and cmd/mcp's registry test cross-checks
//     that it actually exists.
//   - SurfaceDesktopOnly — no MCP twin, on purpose. The Reason is not a comment:
//     the test requires it to be non-empty, and it is the answer to "why is this
//     not exposed to agents". Most of these are the human gate (compile, approve,
//     merge, community synthesis) or UI chrome whose shape is deliberately lossy.
//   - SurfaceLifecycle — process control, never an agent capability. Kept in sync
//     with httpapi's RPC blocklist by test assertion rather than by hope.
//
// WHICH SURFACE THIS TABLE IS ABOUT. The categories describe MCP reachability —
// "can an AI agent get at this capability". They are NOT a statement about the
// HTTP bridge: /api/rpc exposes every binding except the four on its
// blocklist (lifecycle + the Wails-events-only streaming method), including the
// desktop-only ones. That is deliberate — the browser/standalone runtime
// reaches the desktop UI's bindings through that bridge, and the UI is what
// drives the human gate (the Review page calls ApproveWikiPage). So the
// desktop-only entries are "not MCP-reachable", not "unreachable outside the
// app".
//
// The local threat model makes that acceptable on its own terms: the bridge is
// loopback-only and guarded against DNS rebinding (httpapi/guard.go), and a
// local process that could call ApproveWikiPage could equally well open the
// SQLite file and UPDATE it directly. The gate worth keeping is the one that
// blocks a REMOTE page from reaching the knowledge base, and that is the
// guard's job, not this table's.
//
// Two cross-surface checks keep the three views from drifting apart:
// internal/app/frontend_contract_test.go asserts every method the frontend
// calls really exists on *App, and internal/httpapi/rpc_test.go asserts the
// blocklist is exactly the reviewed set and that the frontend calls none of it.

// SurfaceCategory classifies how a binding is reachable outside the desktop UI.
type SurfaceCategory string

const (
	// SurfaceMCPBacked has an agent-facing MCP twin (see the tool name).
	SurfaceMCPBacked SurfaceCategory = "mcp-backed"
	// SurfaceDesktopOnly is intentionally unreachable by agents.
	SurfaceDesktopOnly SurfaceCategory = "desktop-only"
	// SurfaceLifecycle controls the process; blocked on the HTTP bridge.
	SurfaceLifecycle SurfaceCategory = "lifecycle"
)

// BindingSurface is one registry entry.
type BindingSurface struct {
	Category SurfaceCategory
	// MCPTool is the agent-facing twin. Required for SurfaceMCPBacked, forbidden
	// otherwise (an entry claiming desktop-only while naming a tool is exactly the
	// stale-audit shape this file replaces).
	MCPTool string
	// Reason is required for SurfaceDesktopOnly and SurfaceLifecycle.
	Reason string
}

// bindingSurfaces must cover every exported method of *App. Enforced by
// TestBindingSurfaceRegistryIsComplete.
var bindingSurfaces = map[string]BindingSurface{
	// ---- lifecycle -----------------------------------------------------------
	"Startup":  {SurfaceLifecycle, "", "process start; a remote call would boot subsystems the caller never asked for"},
	"Shutdown": {SurfaceLifecycle, "", "closes the database — one POST must not be able to do that"},
	"Service":  {SurfaceLifecycle, "", "returns the *Service itself; not a serialisable capability"},
	"Health":   {SurfaceMCPBacked, "repotory_integrity", "liveness probe; /health is its REST twin"},

	// ---- projects ------------------------------------------------------------
	"GetProjects":            {SurfaceMCPBacked, "repotory_projects_list", ""},
	"GetProjectStats":        {SurfaceMCPBacked, "repotory_projects_stats", ""},
	"GetProjectDetail":       {SurfaceDesktopOnly, "", "GUI drill-down screen; agents get the same facts from repotory_context in one call"},
	"GetProjectOverview":     {SurfaceMCPBacked, "repotory_context", "mined project knowledge; context is the agent-facing render of it"},
	"GetRepoCommits":         {SurfaceDesktopOnly, "", "commit table for the detail page"},
	"GetProjectCommits":      {SurfaceDesktopOnly, "", "commit table for the detail page"},
	"UpdateProjectLevel":     {SurfaceDesktopOnly, "", "project grouping is a human organising act"},
	"PreviewProjectMergeUp":  {SurfaceDesktopOnly, "", "read-only half of the destructive merge-up confirmation flow; grouping is human-only, so its preview has no MCP twin either"},
	"QueryProjectStatsBatch": {SurfaceDesktopOnly, "", "batch view behind the tool-calling project picker; per-project facts are repotory_projects_stats"},
	"ToggleStar":             {SurfaceDesktopOnly, "", "collection is UI state"},
	"RefreshProjectHistory":  {SurfaceDesktopOnly, "", "long-running git refresh driven from the detail page"},
	"SearchProjects":         {SurfaceDesktopOnly, "", "project picker in the UI"},

	// ---- scan ----------------------------------------------------------------
	"TriggerScan":   {SurfaceMCPBacked, "repotory_scan", ""},
	"GetScanStatus": {SurfaceDesktopOnly, "", "progress polling for the scan UI; the agent-facing scan tool waits and reports"},

	// ---- dashboard -----------------------------------------------------------
	"GetSummary":     {SurfaceDesktopOnly, "", "today/week counters for the dashboard header; agents get their numbers from repotory_context and repotory_integrity"},
	"GetHeatmapData": {SurfaceDesktopOnly, "", "heatmap grid for the dashboard; activity is already in repotory_context's Activity section"},
	"GetStatusBar":   {SurfaceDesktopOnly, "", "the dashboard status strip; it bundles counters no agent needs as a single call"},
	"GetTodoCounts":  {SurfaceDesktopOnly, "", "count badges on the dashboard tiles; open todos are already inside repotory_context"},
	"GetNoteCounts":  {SurfaceDesktopOnly, "", "per-level note counters for the dashboard tiles"},

	// ---- search --------------------------------------------------------------
	"SearchNotes": {SurfaceMCPBacked, "repotory_notes_search", ""},
	"SearchAll":   {SurfaceMCPBacked, "repotory_notes_search", ""},

	// ---- notes ---------------------------------------------------------------
	"GetNote":            {SurfaceMCPBacked, "repotory_notes_read", ""},
	"ListNotes":          {SurfaceMCPBacked, "repotory_notes_list", ""},
	"ListAllNotes":       {SurfaceMCPBacked, "repotory_notes_list", ""},
	"ListAllTags":        {SurfaceDesktopOnly, "", "tag filter chips in the knowledge page"},
	"CreateNote":         {SurfaceMCPBacked, "repotory_notes_create", ""},
	"CreateNoteWithMeta": {SurfaceMCPBacked, "repotory_notes_create", ""},
	"UpdateNote":         {SurfaceMCPBacked, "repotory_notes_update", ""},
	"UpdateNoteFull":     {SurfaceMCPBacked, "repotory_notes_update", ""},
	"UpdateNoteMeta":     {SurfaceMCPBacked, "repotory_notes_update", ""},
	// OrganizeNote writes nothing — it returns an AI proposal the human
	// reviews in the editor and saves themselves, which is why an agent may
	// draft with it without bypassing the review gate (ADR-0015). The write
	// stays behind repotory_notes_update, as it always was.
	"OrganizeNote":       {SurfaceMCPBacked, "repotory_notes_organize", ""},
	"DeleteNote":         {SurfaceDesktopOnly, "", "deletion is a human action; no agent path deletes knowledge"},
	"PinNote":            {SurfaceDesktopOnly, "", "pinning changes the UI sort order only; it alters no knowledge content"},
	"MoveNote":           {SurfaceDesktopOnly, "", "moving a note between projects re-files knowledge; a human decides where it belongs"},
	"ListTopics":         {SurfaceDesktopOnly, "", "the topic layer's read side; an agent gets the note itself, and the topic is a filing label a human curates"},
	"CreateTopic":        {SurfaceDesktopOnly, "", "creating a filing label is an organising act, like MoveNote which also has no MCP twin"},
	"UpdateTopic":        {SurfaceDesktopOnly, "", "renaming a filing label re-files knowledge; a human decides what a subject is called"},
	"DeleteTopic":        {SurfaceDesktopOnly, "", "deleting a filing label changes how knowledge is organised; a human decides"},
	"SetNoteTopic":       {SurfaceDesktopOnly, "", "filing a note under a topic is the same organising act as MoveNote"},
	"ListNoteVersions":   {SurfaceDesktopOnly, "", "version-history panel; agents read the live note"},
	"RestoreNoteVersion": {SurfaceDesktopOnly, "", "restoring overwrites current content — human only"},
	"DiffNoteVersions":   {SurfaceDesktopOnly, "", "version diff for the note history panel; agents read the live note via notes_read"},

	// ---- todos ---------------------------------------------------------------
	"ListTodos":    {SurfaceDesktopOnly, "", "todo panel; repotory_context already surfaces open todos"},
	"CreateTodo":   {SurfaceDesktopOnly, "", "todo creation is a human planning act in this UI"},
	"ToggleTodo":   {SurfaceDesktopOnly, "", "checking off is the human decision lint exists to support"},
	"DeleteTodo":   {SurfaceDesktopOnly, "", "deletion is a human action"},
	"ReorderTodos": {SurfaceDesktopOnly, "", "manual todo ordering is a personal prioritisation act, not a data operation"},

	// ---- config --------------------------------------------------------------
	"GetConfig":       {SurfaceDesktopOnly, "", "settings page; secrets are masked for it and it is still not an agent surface"},
	"UpdateConfig":    {SurfaceDesktopOnly, "", "changing privacy gates must never be something an agent can do to itself"},
	"UpdateScanRoots": {SurfaceDesktopOnly, "", "granting new filesystem scope is a human decision"},

	// ---- update check ----------------------------------------------------------
	"CheckUpdate":            {SurfaceDesktopOnly, "", "desktop-app self-maintenance; headless and MCP users update through their own deployment channel"},
	"OpenBrowserURL":         {SurfaceDesktopOnly, "", "opens the system browser for the update card's download button; an agent has no browser to open and no install to perform"},
	"StartUpdateDownload":    {SurfaceDesktopOnly, "", "fetches a release bundle to a local file and verifies its digest; an agent has no installer to hand it to, and downloading on a user's behalf is a desktop-maintenance act"},
	"UpdateDownloadState":    {SurfaceDesktopOnly, "", "progress of the desktop app's own update download; it is UI state for a download the agent cannot start"},
	"CancelUpdateDownload":   {SurfaceDesktopOnly, "", "abandons the desktop app's own update download; nothing else to cancel"},
	"OpenDownloadedUpdate":   {SurfaceDesktopOnly, "", "hands the downloaded bundle to the OS installer; an agent must not launch installers on the user's machine"},
	"RevealDownloadedUpdate": {SurfaceDesktopOnly, "", "shows the downloaded bundle in the OS file manager; a desktop affordance for a human holding the installer, and an agent has no file manager to show it in"},
	"EjectInstallerVolume":   {SurfaceDesktopOnly, "", "unmounts the installer disk image on the user's machine; an agent must not change what the user's Finder shows"},
	"GetInstallInfo":         {SurfaceDesktopOnly, "", "reports where the app is installed and where bundles are written; an agent asking this is asking about the human's machine layout, which it has no business doing"},

	// ---- exports -------------------------------------------------------------
	"GenerateLLMsTxt":       {SurfaceDesktopOnly, "", "the /api/ai_context REST twin serves agents and the dsh plugin; the binding feeds the UI preview"},
	"ExportNoteAsMarkdown":  {SurfaceDesktopOnly, "", "writes to a path chosen in a native save dialog; an agent should not pick where user files land"},
	"ExportMemoryJSON":      {SurfaceDesktopOnly, "", "file save; OMP export is provisional (ADR-0011)"},
	"RunBackupNow":          {SurfaceDesktopOnly, "", "writes a local snapshot file; the hourly ticker already covers the cadence (ADR-0025 L1)"},
	"CheckBackupCopyTarget": {SurfaceDesktopOnly, "", "inspects a candidate off-disk backup directory before the user points the product at it; it reveals where the user's data lives, so an agent has no business asking (ADR-0025 L3)"},

	// ---- AI client wiring -----------------------------------------------------
	// Writing MCP config files into the user's project directory, and the
	// Claude Code SessionEnd hook, are setup acts on the user's machine: they
	// edit configuration the user owns and can be pointed at another project.
	// An agent doing that to itself is not a capability any MCP tool should
	// offer, so these stay desktop-only (the CLI's repotory-init is the
	// terminal twin of the same work).
	"ListAgentClients":  {SurfaceDesktopOnly, "", "the client list behind the settings picker; it exists so the UI cannot offer a client the backend would reject"},
	"SetupAgentClients": {SurfaceDesktopOnly, "", "writes MCP config files, a SessionEnd hook and the repotory block in AGENTS.md/CLAUDE.md into the user's project directory; that is a setup act on the user's machine, and an agent performing it would edit configuration — and instruction files it will later read — without being asked (the CLI's repotory-init is the terminal twin)"},
	// The collected-project instruction checklist (ADR-0054 follow-up). The
	// status answers are desktop checklist chrome; an agent that scanned can
	// already read agent_instructions_missing off repotory_scan's result, so a
	// second agent-facing query would duplicate surface without new
	// information. The installer writes into the user's tracked instruction
	// files — the same setup-act reasoning as SetupAgentClients, one notch
	// narrower (block only, no configs, no hook).
	"AgentInstructionsStatusFor":    {SurfaceDesktopOnly, "", "the project page checklist banner's data; agents see agent_instructions_missing on repotory_scan instead"},
	"MissingAgentInstructionsCount": {SurfaceDesktopOnly, "", "dashboard badge count; same duplication argument as AgentInstructionsStatusFor"},
	"InstallAgentInstructions":      {SurfaceDesktopOnly, "", "writes the managed repotory block into the user's AGENTS.md/CLAUDE.md — the same setup act SetupAgentClients guards, scoped to the instruction block only; an agent performing it would edit instruction files it will later read, without being asked"},

	// ---- knowledge sources / memory -----------------------------------------
	"GetKnowledgeSources":        {SurfaceDesktopOnly, "", "plugins settings tab"},
	"GetKnowledgeSyncStatus":     {SurfaceDesktopOnly, "", "periodic-sync status row in the plugins settings tab"},
	"TriggerKnowledgeImport":     {SurfaceDesktopOnly, "", "importing another tool's memory is an explicit human opt-in (ADR-0011 privacy gate)"},
	"PreviewKnowledgeImport":     {SurfaceDesktopOnly, "", "read-only dry-run of one import; same human opt-in surface as the trigger it previews (ADR-0011 privacy gate)"},
	"TriggerAllKnowledgeImports": {SurfaceDesktopOnly, "", "batch import of other tools' memory files; same per-click consent as the single-source triggers"},
	"ImportClaudeMemory":         {SurfaceDesktopOnly, "", "reads another tool's memory files off disk; the explicit per-click consent is the point (ADR-0011 decision 4)"},
	"CreateHandoff":              {SurfaceMCPBacked, "repotory_handoff", ""},
	"CaptureClaudeHandoff":       {SurfaceDesktopOnly, "", "reads another tool's session transcript; consent is per-click in settings"},
	"ListRecentHandoffs":         {SurfaceDesktopOnly, "", "memory-ring chrome: content-free entries, reading a handoff stays behind notes_read"},
	"LatestHandoff":              {SurfaceDesktopOnly, "", "IDE status chrome: timestamp/title only by design, the body belongs to the knowledge reader"},
	"RebuildEmbeddings":          {SurfaceDesktopOnly, "", "bulk sends note text to a configured endpoint; the gate lives in settings, not in an agent call"},

	// ---- AI ------------------------------------------------------------------
	"AskAI":                  {SurfaceDesktopOnly, "", "the agent-facing twin is repotory_ask, which has no binding (served straight off the service)"},
	"AskAIWithEvidence":      {SurfaceDesktopOnly, "", "AI ask panel; agents use repotory_ask"},
	"StartAskStream":         {SurfaceDesktopOnly, "", "replies over Wails events, so an HTTP/MCP caller would get a stream id nobody can read; the browser path is the SSE endpoint"},
	"FileAnswerAsPage":       {SurfaceDesktopOnly, "", "filing an answer into the knowledge layer is a human decision (ADR-0014 decision 3)"},
	"RunWikiLint":            {SurfaceDesktopOnly, "", "lint writes todos; scheduled and manual paths are the app's own"},
	"LayeredProjectContext":  {SurfaceDesktopOnly, "", "agent-side assembly is repotory_context; this binding is the UI/CLI preview"},
	"ListAIModels":           {SurfaceDesktopOnly, "", "model picker in the settings tab; it enumerates a configured endpoint and is not a knowledge capability"},
	"TestAIChat":             {SurfaceDesktopOnly, "", "settings 'test connection' button"},
	"TestAIChatConfigured":   {SurfaceDesktopOnly, "", "settings 'test connection' against the SAVED provider: the masked key can only be read server-side, never echoed to a client"},
	"ListAIModelsConfigured": {SurfaceDesktopOnly, "", "settings model picker against the SAVED provider; same stored-key rationale as TestAIChatConfigured"},
	"RebuildPageEmbeddings":  {SurfaceDesktopOnly, "", "admin re-embed of approved pages (ADR-0018 lane 2); same endpoint-spend rationale as RebuildEmbeddings"},
	"GetReviewConfig":        {SurfaceDesktopOnly, "", "the daily review switches: whether a job that reads the user's source (and may send excerpts to a model) is armed is a human-facing fact, not an agent capability"},
	"UpdateReviewConfig":     {SurfaceDesktopOnly, "", "arming a job that reads source code and can call a model on the user's behalf is a privacy gate, and an agent must not flip its own gate (ADR-0053 D5)"},
	"LatestReviewReport":     {SurfaceMCPBacked, "repotory_review_latest", ""},
	"RunReviewNow":           {SurfaceMCPBacked, "repotory_review_run", ""},
	"ReviewNotifyCount":      {SurfaceDesktopOnly, "", "the rail dot is an attention budget, not a knowledge capability: it answers 'what has the user not looked at yet'"},
	"GetAIAuditStatus":       {SurfaceMCPBacked, "repotory_ai_audit", ""},
	"ListAICallAudit":        {SurfaceMCPBacked, "repotory_ai_audit", ""},

	// ---- wiki compile & review (the human gate) ------------------------------
	"CompileNote":           {SurfaceDesktopOnly, "", "an agent able to write pending knowledge would bypass the human gate the whole design rests on (ADR-0015)"},
	"CompileProjectNotes":   {SurfaceDesktopOnly, "", "same gate; batch form"},
	"StartCompileJob":       {SurfaceDesktopOnly, "", "same gate, queued form"},
	"GetCompileJob":         {SurfaceDesktopOnly, "", "job progress for the review page"},
	"ListCompileJobs":       {SurfaceDesktopOnly, "", "job list for the review page"},
	"StartLintJob":          {SurfaceDesktopOnly, "", "same gate as RunWikiLint"},
	"CancelCompileJob":      {SurfaceDesktopOnly, "", "UI control of a user-started job"},
	"ListPendingWikiPages":  {SurfaceDesktopOnly, "", "the review queue IS the human gate's UI"},
	"ApproveWikiPage":       {SurfaceDesktopOnly, "", "publishing into retrieval is a human act and has no other caller"},
	"RejectWikiPage":        {SurfaceDesktopOnly, "", "reject writes an error-page tombstone rather than deleting; the review decision is human-only (ADR-0015 decision 3)"},
	"ListRejectedWikiPages": {SurfaceDesktopOnly, "", "the error-page list in the review tab, so tombstones stay visible to the person who can clean them up"},
	"DeleteCompiledPage":    {SurfaceDesktopOnly, "", "permanent removal of never-published pages: human only"},

	// ---- approved page browsing (ADR-0032) ------------------------------------
	// Reading a page a human already approved is a pure read: the gate is on
	// the write side (ADR-0015). The scope bindings below are a different case
	// — they decide what speaks for the user across projects — and stay
	// human-only.
	"BrowseWikiPages": {SurfaceMCPBacked, "repotory_wiki_search", ""},
	"SearchWikiPages": {SurfaceMCPBacked, "repotory_wiki_search", ""},
	"GetWikiPage":     {SurfaceMCPBacked, "repotory_wiki_read", ""},

	// ---- page scope: project tier <-> global tier (ADR-0031) -------------------
	"ListApprovedWikiPages":   {SurfaceDesktopOnly, "", "the scope surface a person looks at in order to decide which pages speak for the user across projects"},
	"PromoteWikiPageToGlobal": {SurfaceDesktopOnly, "", "widening a page's scope puts it into the cross-project profile that steers every answer; that is a curated human statement, and an agent doing it is the no-gate write ADR-0015 refuses (ADR-0031)"},
	"AssignWikiPageProject":   {SurfaceDesktopOnly, "", "the inverse of the promotion and what makes it reversible; scope is a human curation, not an agent's inference (ADR-0031)"},

	// ---- knowledge graph (ADR-0019 / 0020 / 0021) ----------------------------
	"FindMergeCandidates":    {SurfaceDesktopOnly, "", "read-only, but it is the surface a person looks at in order to decide — exposing it invites an agent to merge without the human"},
	"SuggestWikiLinks":       {SurfaceDesktopOnly, "", "assists human authoring by suggesting [[wikilinks]] based on existing approved concept vocabulary"},
	"MergeWikiPages":         {SurfaceDesktopOnly, "", "a merge destroys information and may only follow a human decision (ADR-0019 decision 1, pinned by TestMergeHasNoAutoPath)"},
	"RunCommunitySummary":    {SurfaceDesktopOnly, "", "writes model output into the library; reachable by an agent it would defeat the review queue (ADR-0020 decision 9)"},
	"ResolveWorkspacePath":   {SurfaceDesktopOnly, "", "agents pass `path` to repotory_context instead; a second client-side matcher is the disagreement this avoids"},
	"BindWorkspaceToProject": {SurfaceDesktopOnly, "", "filing a workspace folder under a project decides where that folder's sessions get their knowledge from; that is a human decision, and the write has no MCP twin for the same reason MoveNote has none"},
	"ListWikiPageVersions":   {SurfaceDesktopOnly, "", "the page history panel (ADR-0021); snapshots exist so a HUMAN can notice stale approved content, not so an agent can act on it"},
	"DiffWikiPageVersion":    {SurfaceDesktopOnly, "", "snapshot-vs-current diff on the review page; it supports a human approve/reject decision"},

	// ---- organize (ADR-0023) -------------------------------------------------
	"ListOrganizeGroups":   {SurfaceDesktopOnly, "", "read-only, but it is the surface a person looks at to decide where unfiled notes belong; an agent choosing for the user is the curation this feature deliberately leaves to a human"},
	"ReassignLandingNotes": {SurfaceDesktopOnly, "", "re-filing notes between projects is a human organising act, like MoveNote which also has no MCP twin"},
	"DeleteLandingProject": {SurfaceDesktopOnly, "", "deletes a landing placeholder and may delete its notes; deletion is human-only and is a confirmed action, never an agent-callable cascade"},
}

// mcpBackedBindings returns the bindings that claim an MCP twin, for the
// cross-check in cmd/mcp (which can import this package without a cycle).
func mcpBackedBindings() map[string]string {
	out := map[string]string{}
	for name, s := range bindingSurfaces {
		if s.Category == SurfaceMCPBacked && s.MCPTool != "" {
			out[name] = s.MCPTool
		}
	}
	return out
}
