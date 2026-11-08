---
title: API Reference
order: 22
---

# API Reference

Cinder's external interface is the **Wails binding surface**: Go methods are exposed to the frontend via Wails Bind (`window.go.main.App.<MethodName>`), and the method names plus JSON payloads are the contract. The desktop app does not listen on an HTTP port by default; when an HTTP shape is needed, `cmd/server` provides a loopback-only JSON API (reusing the same service layer).

> The binding layer is a thin delegation (`internal/app`); all implementations live in `internal/service`, shared by the CLI and MCP.

## Contract changes in 1.7.0

- `GetHeatmapData(projectId int64)`: new parameter — `0` means global, `>0` limits to that project's repos (the project detail page previously used global data by mistake)
- Removed dead methods that were never called by the frontend: `ExportProjectStats`, `ExportHeatmapCSV`, `GetNoteVersion`, `ScanForRepositories`, `RefreshStats`, `RefreshAllStats`, `RefreshProjectStats`

---

## Headless HTTP server (`repotory server`)

A JSON API for external runtimes such as the DeepSeek Harness dsh-plugin. It shares the same `internal/service` implementation and the same SQLite database as the desktop app, CLI, and MCP.

| Endpoint | Method | Description |
|------|------|------|
| `/health` | GET | service and database health (`version` = release version, `core_version` = memory-layer contract version, see [ADR-0048](../adr/0048-core-contract-version.md)) |
| `/api/ai_context` | GET/POST | the entire knowledge base as Markdown (llms.txt style) |
| `/api/search?q=...&all=1` | GET | FTS5 full-text search (notes only by default; `all=1` includes todos) |
| `/api/project/{id}/detail` | GET | project + repos with historical stats |
| `/api/project/{id}/overview` | GET | knowledge mining results (README / tech stack / dependencies, etc.) |
| `/api/project/{id}/stats?date=` | GET | project stats for a given day |

> **Trust boundary**: this server has **no authentication**, and everything it returns is the user's complete local knowledge base. Security relies entirely on `cmd/server` binding to `127.0.0.1` only — reachability is equivalent to "another process on this machine". **Never** change this to `0.0.0.0` or expose it to the network through a reverse proxy; for remote access, add an authentication scheme and submit an ADR first.

### Optional: same-origin frontend hosting (`--web-dist`)

By default the server is API-only, exactly as in production. With `--web-dist <dir>` (or the `REPOTORY_WEB_DIST` env var), the same process also serves the built frontend, so a browser reaches both the SPA and `/api/rpc` on **one origin** — no dev server or cross-origin proxy needed:

```bash
cd web && npm run build:server              # base /, outputs web/dist-server
cd .. && go build -o cinder-server ./cmd/server
./cinder-server --port 18766 --web-dist web/dist-server
```

- `/api/*` and `/health` always route to the API mux (`cmd/server`'s `isAPIPath` is pinned against `httpapi`'s registered routes by a test); every other path prefers a real static file and falls back to `index.html` (client-side routing)
- A missing static asset (a path with a non-`.html` extension) is a real `404`, not an HTML fallback — missing files are never disguised as MIME errors
- Relative paths are resolved to absolute at startup and the actual serving directory is logged; path traversal is forced back to the root, and the trust boundary is unchanged (loopback only)
- Only the `build:server` frontend artifact works with this flag: the desktop build `npm run build` produces `web/dist` with base `./` (wails:// origin only) — mixing them blanks the page under one runtime

---

## `/api/rpc` (JSON-RPC bridge)

`/api/rpc` is a **full write surface**: it exposes the same `App` object the desktop UI binds (every exported method of `internal/app`) over JSON-RPC, and the web frontend's transport uses it in browser/standalone mode. Its capability equals the desktop UI — beyond the read-only endpoints listed above, `CreateNote`, `DeleteNote`, `UpdateConfig`, `UpdateScanRoots`, `TriggerScan`, `ReloadPlugins` and every other method are callable through it. Any security assessment must count this endpoint in the exposure.

- **Request**: `POST /api/rpc` with `{"method": "<App method name>", "args": [positional JSON args]}`; `args` may be omitted (missing parameters become zero values; a `context.Context` parameter receives the request context)
- **Response**: `{"result": <first return value>}`; a method returning `(T, error)` with a non-nil error yields `422` + `{"error": "..."}`
- **Status codes**: `400` bad args / `403` blocked lifecycle method / `404` unknown method / `405` not POST / `413` body over 1 MiB / `501` unsupported signature (variadic or non-`(T, error)` pair) / `503` binding unavailable
- **Lifecycle blocklist**: `Startup`, `Shutdown`, `Service` are Wails runtime internals and return `403` (previously a single `{"method":"Shutdown"}` could silently close the server's database handle)
- **Body limit**: 1 MiB (legitimate payloads — a note or handoff record — sit far below it)

---

## Projects

| Method | Signature | Description |
|------|------|------|
| `GetProjects` | `(date string, starredOnly bool) → ProjectResponse[]` | project list (yesterday by default; triggers an on-demand single-day refresh when neither today nor yesterday has data) |
| `GetProjectDetail` | `(id) → ProjectDetail` | project + repo list, each with historical stats |
| `GetProjectStats` | `(id, date) → DailyStat[]` | project stats for a given day (yesterday by default) |
| `GetProjectOverview` | `(id) → ProjectOverview` | knowledge mining: README / tech stack / languages / dependencies / contributors / activity / recent commits (cached in repo_meta; mined asynchronously on cache miss) |
| `ResolveWorkspacePath` | `(path) → ProjectResolution` | read-only: resolve an absolute directory to its owning project and, when the folder sits inside a scanned repository, that repository (the four-tier path resolution). Backs the IDE's scope display; there is no MCP twin — agents pass `path` to `repotory_context` instead. See [AI Integration](../features/ai-integration.md) |
| `SearchProjects` | `(query) → ProjectResponse[]` | fuzzy search by name/path (enriched with yesterday's stats) |
| `ToggleStar` | `(id) → bool` | toggle star, returns the new state (atomic UPDATE) |
| `UpdateProjectLevel` | `(id, "up"\|"down", confirmed) → {success, new_level}` | merge/split projects (single transaction). `"up"` absorbs and **deletes** sibling projects (with their repos/notes/todos) and requires `confirmed=true`; an unconfirmed call errors out and changes nothing. `"down"` splits by repository and ignores the flag |
| `PreviewProjectMergeUp` | `(id) → MergePreview` | read-only preview of which siblings `"up"` would absorb and delete, with the migrated repo/note/todo counts — the confirmation dialog's data source, sharing one sibling listing with the merge itself (`db.MergePlan`), so the preview cannot understate. No MCP twin (desktop-only, like `UpdateProjectLevel`) |
| `RefreshProjectHistory` | `(id) → {success}` | backfill the last 365 days of stats for this project |
| `ListOrganizeGroups` | `() → OrganizeGroup[]` | knowledge-base organize: lists landing projects (kind `import_placeholder` / `default`) that still hold unfiled notes, with their note count and whether a scanned repository points at them. Read-only, no MCP twin — organizing is a human act (see [ADR-0023](../adr/0023-note-ownership-knowledge-organize.md)) |
| `ReassignLandingNotes` | `(sourceId, targetId) → int` | re-file every note from a landing project into a target real project (or "Uncategorized"); returns the number moved. Source must be a landing project; target must be a scanned project or the default. No MCP twin |
| `DeleteLandingProject` | `(id, deleteNotes bool) → error` | delete an `import_placeholder` landing project; when it still holds notes `deleteNotes` must be true to confirm removing them along with it. The default and scanned projects are always refused — deletion is a confirmed action, never an automatic cascade. No MCP twin |

**OrganizeGroup**: `{ project_id, name, root_path, kind, note_count, repo_bound }`.

**MergePreview**: `{ target_id, target_name, new_root, siblings: MergeSibling[], repos, notes, todos }`, where `MergeSibling`: `{ id, name, root_path, repos, notes, todos }`; an empty `siblings` means there is nothing to absorb.

**ProjectResponse** (excerpt):

```json
{
  "id": 1, "name": "my-project", "root_path": "/Users/me/code/my-project",
  "is_starred": true, "repo_count": 2, "project_kind": "scanned",
  "total_added": 1200, "total_deleted": 300,
  "my_added": 800, "my_deleted": 200, "my_files": 15,
  "my_added_7d": 4200,
  "is_workday": true, "below_standard": false
}
```

`my_added` is **single-day** personal additions (anchored on the same `date` as `GetProjects`, yesterday by default) and feeds the dashboard's daily goal and the `below_standard` check; `my_added_7d` is personal additions over the **7-day window** ending on that same anchor, for surfaces that rank by recent activity — a single day reads 0 whenever there were no personal commits, which silently degrades the ordering to alphabetical.

## Scanning

| Method | Signature | Description |
|------|------|------|
| `TriggerScan` | `() → {success, task_id}` | full async scan, returns immediately |
| `GetScanStatus` | `() → ScanStatus` | `{running, backfilling, message, progress, total}` |

## Summary and status

| Method | Signature | Description |
|------|------|------|
| `GetSummary` | `(date) → Summary` | global daily summary (team/personal additions & deletions, files, repo count, workday flag) |
| `GetHeatmapData` | `(projectId) → {days: HeatmapDay[]}` | heatmap for the last year; `projectId>0` limits to a project |
| `GetStatusBar` | `() → StatusBarData` | current time + latest commit (30s cache) |
| `GetTodoCounts` / `GetNoteCounts` | `() → counts[]` | per-project todos (incomplete/total) and note counts |

## Search

| Method | Signature | Description |
|------|------|------|
| `SearchNotes` | `(query) → SearchHit[]` | FTS5 note search (bm25 + snippet highlighting) |
| `SearchAll` | `(query) → SearchHit[]` | combined search over notes + todos |

## Notes

| Method | Description |
|------|------|
| `ListNotes(projectID)` / `ListAllNotes()` / `ListAllTags()` | project notes / all notes (with project name) / all tags |
| `CreateNote(projectID, content)` | create (defaults: kind=other, source=manual) |
| `CreateNoteWithMeta(projectID, title, content, tags, kind, source)` | create with metadata |
| `UpdateNote(noteID, content)` / `UpdateNoteMeta(noteID, title, tags, kind, pinned)` | update content / metadata |
| `PinNote(noteID, pinned)` / `MoveNote(noteID, projectID)` / `DeleteNote(noteID)` | pin / move / delete |
| `ListNoteVersions(noteID)` | version list (latest 50) |
| `RestoreNoteVersion(noteID, versionID)` | restore to a historical version |
| `DiffNoteVersions(noteID, versionID)` | line-level diff of version vs current (`+/-/space` prefix) |

A successful create emits the plugin event `note.created`.

## Topics (project → topic → note)

A topic is the layer between a project and its notes: a project holds topics, a note is filed under one of them. A topic is an **independent entity** (its own row, its own id, its own lifecycle), which is what makes renaming it a single UPDATE instead of a rewrite across every note, and what makes "how many notes are in this topic" answerable at all. A topic belongs to **exactly one project** — the same subject name in two projects is two topics, because a topic's meaning comes from its project's context.

| Method | Description |
|------|------|
| `ListTopics(projectID)` | the project's topics with their note counts. Read-only, no MCP twin |
| `CreateTopic(projectID, name, description)` | create a topic in a project; the name is normalized (trimmed, internal whitespace collapsed) and a duplicate within the same project is rejected |
| `UpdateTopic(topicID, name, description)` | rename a topic / change its description; a rename onto an existing name is rejected |
| `DeleteTopic(topicID)` | delete a topic. The notes filed under it are **kept** (`topic_id` falls back to "no topic") — deleting a filing label must never destroy knowledge |
| `SetNoteTopic(noteID, topicID)` | file a note under a topic; `topicID=0` clears the filing. A topic from another project is rejected (the db layer re-checks the project match on write) |

**NoteTopic**: `{ id, project_id, name, description, sort_order, note_count, created_at, updated_at }`.

Writing a topic is a **human organizing act** (like `MoveNote`), so all five bindings are desktop-only with no MCP twin.

## Todos

`ListTodos(projectID)` / `CreateTodo(projectID, title)` / `ToggleTodo(id)` / `DeleteTodo(id)` / `ReorderTodos(ids[])` (reordered in a single transaction).

## Knowledge-page review and scope

Compiled knowledge pages land as pending first, and only a human publishes them into retrieval (ADR-0015). **Scope** says where a page may speak: the project tier (the default) or the global tier (`project_id = 0`, which enters L3, the cross-project profile of `repotory_context`, and is visible to retrieval in every project). Both moves are **human acts**, so this family of bindings is desktop-only with no MCP twin.

| Method | Description |
|------|------|
| `ListPendingWikiPages(projectID)` | the pending queue (each entry carries its source notes and edges, so it can be compared before approval); `projectID=0` means every project |
| `ApproveWikiPage(pageID)` | publishes a page into retrieval. Neither the compiler nor the lint can reach it |
| `RejectWikiPage(pageID)` | replaces a pending page with an error-page tombstone (the row is kept, so inbound links stay resolvable); approved pages are refused |
| `ListRejectedWikiPages(projectID)` | the error-page list |
| `DeleteCompiledPage(pageID)` | permanently removes a never-published page (pending or rejected); approved pages are refused |
| `ListApprovedWikiPages(projectID)` | the approved-page list (the scope surface); `projectID=0` includes the global pages |
| `PromoteWikiPageToGlobal(pageID)` | promotes an approved page into the global tier. **Only approved pages may be promoted** (a pending page is invisible to every project-scoped review feed, so promoting one would create an orphan nobody could approve); idempotent |
| `AssignWikiPageProject(pageID, projectID)` | moves a global page back into a named project (which is what makes the promotion reversible); the target project must exist |

Neither move changes a page's status or content, and neither touches `updated_at` (freshness semantics belong to content revisions). A global page is stored as SQL `NULL` — `project_id` carries a foreign key and the database runs with `PRAGMA foreign_keys=ON`, so the literal 0 is rejected by the constraint; Go projects it to 0 uniformly. See ADR-0031.

## Reading approved knowledge pages (ADR-0032)

The family above is the write side, and those are human acts; this family is the **read side** — a page that passed the gate is readable by people. **Approved only**: pending and rejected pages still appear on the review page and nowhere else.

| Method | Description |
|------|------|
| `BrowseWikiPages(projectID, limit)` | the browse feed of approved pages, newest first; `projectID=0` means every project |
| `SearchWikiPages(projectID, query, limit)` | searches approved pages (bm25 + CJK relaxation); the scope matches the evidence path exactly: a project scope means that project's pages **plus the global ones**, no scope means all of them |
| `GetWikiPage(pageID)` | reads one page's body, slug, kind, scope, outbound / inbound edges and the ids of the notes it was compiled from; **anything that is not approved is refused** (the gate governs reading too) |

All three have MCP twins (`repotory_wiki_search` / `repotory_wiki_read`), and both IDE distribution surfaces consume them (the VS Code chat console, the JetBrains tool window) — the plugins only call the bindings, they never write retrieval of their own.

**Human browsing does not write `access_count`**: that column means "this page was surfaced into some answer's evidence", and a human opening a page is a different visit — mixing them would make one signal carry two meanings. See ADR-0032.

## Configuration

| Method | Description |
|------|------|
| `GetConfig()` | `{config: map, scan_roots: []}` |
| `UpdateConfig(key, value)` | allowed keys: `daily_code_standard` / `scan_depth` / `git_author` / `auto_import` (numeric keys are validated as numbers); update-check keys `update_check_enabled` (0/1) / `update_proxy` (free-text proxy address); backup keys `backup_enabled` (0/1) / `backup_retain` (integer) / `backup_copy_dir` (free-text absolute path, the off-disk copy directory) / `backup_remote_enabled` (0/1) / `backup_remote_endpoint` / `backup_remote_bucket` / `backup_remote_prefix` / `backup_remote_access_key` / `backup_remote_secret_key` (free text; `backup_remote_secret_key` is returned masked as `********` by `GetConfig`) |
| `UpdateScanRoots(roots[])` | atomically replace the scan root list |
| `CheckBackupCopyTarget(path)` | inspects a candidate off-disk copy directory (ADR-0025 L3) and returns `{path, exists, writable, same_device, device_known, code, blocking}`. An empty `path` means "the target that is configured right now". `code` is a machine-readable reason (`not_configured` / `relative_path` / `not_a_directory` / `unwritable` / `no_ancestor` / `ancestor_unwritable` / `stat_failed` / `same_device`; empty = usable) — the sentence is rendered by the frontend in the user's language. `blocking` separates "a copy could never land there" from "the copy works but sits on the same disk as the live database (advice only)". On Windows `device_known` is always false: that platform cannot determine a volume serial number reliably, and "cannot tell" must not be reported as "a different disk" |

## Version updates

Desktop-only: headless and MCP users update through their own deployment channel.

| Method | Description |
|------|------|
| `CheckUpdate()` | queries the GitHub Releases `latest` and returns `{current, latest, has_update, release_url, published_at, notes, assets[], download_url, download_name}`. `download_url` points at the app bundle matching the running platform (MCP server / IDE plugin archives are excluded) and is empty when the release ships no bundle for this platform, in which case the UI falls back to the release page |
| `OpenBrowserURL(url)` | opens a URL in the system browser (https only), used by the update card's download button; it never downloads or replaces the binary |
| `StartUpdateDownload(url, fileName, sha256, version)` | downloads the platform's release bundle into the system's download folder and verifies its SHA256. `url` must be https; `version` goes into the file name (`repotory-2.25.0-darwin-arm64.dmg`). The download runs in the background — poll `UpdateDownloadState` for progress |
| `UpdateDownloadState()` | the download's progress snapshot `{phase, version, file_name, downloaded, total, speed, remaining_seconds, path, error, checksum_verified, installer_ejected}`. A finished download survives an app restart (unless the file has since been deleted) |
| `CancelUpdateDownload()` | aborts an in-flight download |
| `OpenDownloadedUpdate(path)` | hands the downloaded bundle to the OS installer (macOS mounts the dmg, Windows and Linux reveal it in the file manager). It never replaces the running program |
| `RevealDownloadedUpdate(path)` | selects the file in the OS file manager (macOS `open -R`, Windows `explorer /select,`, Linux `xdg-open`) — the answer to "where did that download go?" |
| `EjectInstallerVolume()` | unmounts the installer volume on macOS and reports whether one was actually ejected. Only a volume holding this app's own bundle is touched |
| `GetInstallInfo()` | returns `{version, platform, install_path, executable_path, download_dir}` — the install path and the download folder, reported by the running binary itself |

## AI export and plugins

| Method | Description |
|------|------|
| `GenerateLLMsTxt()` | LLM-oriented Markdown overview of the knowledge base |
| `ExportNoteAsMarkdown(noteID)` | note Markdown with YAML frontmatter |
| `GetKnowledgeSources()` | knowledge source list (built-in claude + plugin-registered) |
| `TriggerKnowledgeImport(name)` / `TriggerAllKnowledgeImports()` | trigger imports (emits the `import.completed` frontend event) |
| `ImportClaudeMemory()` | one-click Claude memory import |
| `Health()` | `{status, version}` |

## AI client wiring (desktop-only)

Connects Cinder to the MCP clients of Claude Code / Cursor / VS Code / Windsurf. **No MCP counterpart**: these two bindings write files into the user's project directory — that is a "set up the machine" action, not an agent action.

| Method | Description |
|------|------|
| `ListAgentClients()` | the list of connectable client names (`claude` / `cursor` / `vscode` / `windsurf`). The interface renders its options from this list instead of hardcoding them |
| `SetupAgentClients(projectID, clients[], withHook)` | writes the MCP registration into the target project directory (`.mcp.json` / `.cursor/mcp.json` / `.vscode/mcp.json`; **Windsurf is the exception** — its config is the user-level `~/.codeium/windsurf/mcp_config.json`) and optionally installs the Claude Code SessionEnd hook. Returns `{code, blocking, binary, files[], hook?, hook_settings?, warnings[]}` |

`code` is a machine-readable reason (empty string = success): `no_client` / `bad_client` / `project_not_found` / `virtual_project` / `auto_grouped_project` / `missing_directory` / `binary_missing`; the wording is rendered by the frontend in your language, and `blocking` separates "cannot be done" from "done, with something to say" — the rule across the whole repository: the backend never returns a hardcoded sentence.

A few deliberate refusals: the default project (`repotory://memory/default`) and the scan-time auto-grouped container are not directories you can connect a client to; ticking no client at all is refused (rather than "writing to all four"); a missing target directory is reported as missing rather than created on the fly.

It and the terminal `repotory-init` path are **two implementations of one protocol** (the Go `internal/agentinit` and the node `scripts/repotory-init/index.mjs`), and the files both produce are pinned to one fixture. See [AI Integration](../features/ai-integration.md#desktop-one-click-connect-no-terminal).

## Events (Wails → frontend)

| Event | Payload |
|------|------|
| `import.completed` | `{source, created, updated, skipped, error?}` |

---

## OpenAPI

For now, the tables on this page serve as the machine-readable contract (the hand-maintained openapi.json was removed in D10 and will return once CI generates it automatically). The desktop app does not expose a public HTTP service (see [TODO](https://github.com/sky-jiangcheng/cinder/blob/master/TODO.md)).
