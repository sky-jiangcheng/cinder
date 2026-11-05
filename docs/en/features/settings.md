---
title: Settings
order: 5
---

# Settings

The Settings page has eight tabs: Scan Directories, Code Target, Author, Appearance, Plugins, AI, Actions, and Updates.

## Scan Directories

- Add / remove scan roots (a rescan is required after saving for changes to take effect)
- For the default roots seeded automatically on first launch, see [Getting Started](../getting-started.md)

Only directories added here get scanned — which is exactly what step "① Configure scan directories" in [Getting Started](../getting-started.md#2-run-a-scan) is for.

## Code Target

| Setting | Description | Default |
|---------|-------------|---------|
| Daily goal (lines) | The workday check threshold; drives the progress ring and card alerts | 500 (range 100-10000) |
| Maximum scan depth | Directory levels to descend below each scan root | 2 (range 1-2) |

## Author

**Git author name**: the author filter for personal statistics (same semantics as `git log --author`). When unset, it is read automatically from `git config user.name`.

## Appearance

Light / Dark / Follow system — pick one; changes apply immediately and are remembered.

## Plugins

- **Auto-import toggle**: whether to run **auto-eligible** knowledge sources at startup — only "curated" sources (Claude memory) run in that pass; the other four stay manual. **Off by default**, and it only runs when explicitly enabled (turning it off does not stop you from importing any single source by hand on this page). Note this is a **behavior change**: upgrading normalizes an existing "on" to "off", because the old version could not distinguish "the user enabled it" from "the program seeded the default" — we would rather everyone click once again than have the app make that privacy decision on their behalf.
- **Claude session auto-capture (M1, off by default)**: a `claude_session_capture` toggle plus a "capture latest session by project ID" action — on-demand capture of a project's newest Claude Code session transcript into a handoff note. Nothing is read until you enable it; B-end users get a `repotory-capture` SessionEnd hook for automatic capture (see [ADR-0010](../adr/0010-session-auto-capture.md)).
- **Global-memory source target project**: OpenClaw / Hermes memory is **cross-project global**, so you pick which Cinder project to attach it to (`openclaw_project` / `hermes_project` — name or ID); unset → that source skips, never mis-attached.
- **Knowledge import sources**: built-in + plugin-registered importers, each with an **Import Now** action. Six built-ins now — **claude** (eligible for startup auto-import, which only runs when the switch above is explicitly on) and **codex / opencode / openclaw / hermes / cursor** as **opt-in manual** sources (transcripts / global memory are sensitive → excluded from startup auto-import, triggered here or via config). `cursor` is best effort: an undocumented format, first prompt + last reply only, and it fails the whole source loudly rather than silently importing nothing when the layout is unrecognized. Remote vector stores (Qdrant / Weaviate) and the embedding provider are advanced config; see [ADR-0012](../adr/0012-semantic-search.md) / [ADR-0013](../adr/0013-vector-database-selection.md).
- **Semantic search in AI settings**: the AI tab exposes `semantic_search`, the embedding endpoint/model/key, the vector store and “Rebuild index”. It remains off by default; enabling it means you consent to sending note text to the configured endpoint. A real labeled A/B gate still has not passed, so the default stays off and evaluation remains `cmd/abeval` (see [ADR-0012](../adr/0012-semantic-search.md)). Four switches have **no control on this page yet**: the model-backed lint checks (`wiki_lint_llm`), ingest-time compilation (`wiki_compile`), merge judging (`wiki_merge_suggest`, which also needs `wiki_lint_llm`) and community synthesis (`wiki_community_summary`); enable them via the config keys described in [Knowledge Source Import](../plugins/overview.md) and [AI Integration](ai-integration.md). A standalone Review tab now lists pending pages (approve / mark unqualified / delete permanently) plus compile-job progress, and it mounts the merge-suggestions area and a "Run synthesis" button; but **the switches that enable compilation / merge judging / synthesis themselves are still absent here** — those review-tab actions send nothing out and write nothing until the matching config key is set to `1`, so turn it on via the config keys above first.
- **Loaded plugins**: load status and error messages per plugin directory; **Reload** hot-reloads.

> Privacy posture: transcript / global-memory sources are **off by default, explicit opt-in, directory-allowlisted** (never traversing parent dirs that hold keys/private material); semantic search is off by default and gated behind an A/B eval.

For plugin development, see the [Plugin Handbook](../plugins/overview.md).

## Actions

- **Rescan all projects now**: triggers a full scan (equivalent to the dashboard button)
- **Import Claude memory**: triggers one import manually; once it finishes, jump to the knowledge base to review
- **Connect AI clients**: pick a project and tick the clients (Claude Code / Cursor / VS Code / Windsurf) — one press of "Connect" writes the MCP registration, optionally installs the SessionEnd hook, and can kick off a scan, so the client works right after you open it. See [AI Integration](ai-integration.md#desktop-one-click-connect-no-terminal)

## Updates

- **Check for updates**: asks GitHub Releases for the newest stable version and compares it against the running build, then reports "you are on the latest version" or shows a new-version card (publication date plus release notes). **Advisory only** — Cinder never downloads or replaces itself; it hands you the direct link to the bundle for your platform and you decide when to install it.
- **Download new version**: opens the bundle matching your machine (macOS `.dmg` / Windows `.zip` / Linux `.tar.gz`) in the system browser. When the release ships no bundle for this platform (e.g. linux/arm64, which CI does not build), the release page is the target instead. The MCP server and IDE plugin archives in the same release are **never** offered as "the new version of the app".
- **Check for updates on startup** (`update_check_enabled`, on by default): one background check after launch; a newer release lights a small dot on the "Settings" icon in the left rail. Turning it off means GitHub is contacted only when you click "Check for updates" here. A failed check never pops an error at startup — the machine that cannot reach GitHub should not see a red banner on every launch.
- **Update proxy** (`update_proxy`): `api.github.com` is unreachable on some networks, so a local proxy can be set here (e.g. `http://127.0.0.1:7890`); leave it empty to connect directly.

> This tab only makes sense on the desktop: headless and MCP users update through their own deployment channel.
