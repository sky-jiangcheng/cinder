---
title: Features Overview
order: 2
---

# Features Overview

> Tier legend: **Core** (completes the discover → understand → record → retrieve → AI loop) | **Support** (makes the loop legible) | **Experimental** (kept, not expanded) | **Paused** (no further investment) — see [ADR-0006](adr/0006-scope-freeze.md). This page holds the full feature tables; per-capability manuals live in the sidebar under Features.

**Positioning**: what this page lists is a **local-first, agent-neutral** memory layer. The memory layer is not bound to a harness — every coding agent that speaks MCP (Claude Code / Cursor / Codex / Copilot and others) reads and writes the same memory, through the protocol rather than through some plugin. **No account, no cloud**: the whole store is one SQLite file on your machine. A neutral layer is needed precisely because each IDE's or agent's own session history stays locked inside its own ecosystem (VS Code's session index is still unreadable by extensions) — the cross-agent layer needs a neutral landing point of its own.

## Features

> Tier legend: **Core** (completes the discover → understand → record → retrieve → AI loop) | **Support** (makes the loop legible) | **Experimental** (kept, not expanded) | **Paused** (no further investment). See [ADR-0006](adr/0006-scope-freeze.md).

### Knowledge Base (Core)

| Feature | Description |
|---------|-------------|
| Markdown notes | Title / tags / categories (knowledge · journal · ideas · other) / pinning / cross-project moves; drafts autosave |
| Block editor | Type `/` to open the block panel and insert structured blocks — callouts, tabs, collapsibles, code, Mermaid, math, tables… drag to reorder; the output is still plain Markdown (**Experimental**: complex blocks on hold) |
| Rich rendering | highlight.js code highlighting, Mermaid diagrams, KaTeX math, GFM callouts and task lists |
| FTS5 full-text search | trigram + bm25 relevance ranking, snippet highlighting, covering notes and todos; short CJK queries automatically fall back to LIKE |
| Version history | Auto snapshot on every save; line-level diff of any version against the current one, one-click restore |
| Global search | The home console's "search" mode (federated: repos / notes / todos) + the ⌘/Ctrl+K command palette |

> **Knowledge is one tab with three views inside** (ADR-0038, decision 4 + ADR-0039, decisions 1 and 3): the top-level segmented control switches "Notes / Wiki pages" (two states of one object), and the notes view switches between "All notes / To file" with a tab (two different things). All three share one search box, one project-scope dropdown and one count bar.

### AI-Ready Interfaces (Core)

| Channel | Description |
|---------|-------------|
| MCP Server | `repotory-mcp` stdio server, 20 tools (repo scan + context injection + session handoff + note CRUD/organize + project queries + search + four self-checks + knowledge-page retrieval + cross-client shared conversations + workspace metrics); plugs into Claude Code / Cursor and more (the only interface AI executes through) |
| `repotory_scan` | One-shot cold start: seeds default scan roots and scans synchronously to discover local Git repos. A pure-MCP install (no desktop app) can bootstrap the knowledge base |
| `repotory_context` | One call at session start loads the project's full context: tech stack / README summary / dependencies / recent commits / open todos / highly relevant notes (handoff notes first) — replaces 3-4 chained queries |
| `repotory_handoff` | Structured handoff at session end: summary / changes / decisions / gotchas / next_steps rendered into a unified template and stored; the next session (any agent) reads it automatically |
| llms.txt | `GenerateLLMsTxt` emits an LLM-oriented knowledge base overview in Markdown |
| Note export | Export any note as `.md` with YAML frontmatter |
| Knowledge importers | Bring other agents' memory in: **Claude**, **Codex**, **OpenCode**, **CodeBuddy** (+ CodeBuddy memory), **Cursor**, **OpenClaw**, **Hermes** — curated memory files auto-imported on startup where applicable; transcript sources are **opt-in, manually-triggered** (allowlist-scoped). Idempotent upsert: re-running updates, never duplicates |
| Session auto-capture (M1) | On-demand capture of a project's latest Claude Code session transcript into a handoff note; a `repotory-capture` CLI covers the B-end SessionEnd-hook automation. **Off by default** (`claude_session_capture`), privacy-gated |
| Portable memory export | `ExportMemoryJSON` emits the knowledge base as [Open Memory Protocol](https://github.com/SMJAI/open-memory-protocol)-style memory objects (provisional; OMP is pre-1.0) |
| Data integrity audit | `repotory_integrity`, 6 read-only checks: FTS index drift, orphan rows, schema shape vs version stamp, scan coverage, knowledge-cache freshness, version-snapshot orphans. Index drift makes search **silently miss results** with no other mechanism to catch it — this is the only way to detect it |

### Knowledge Graph & LLM Wiki (Support — human-gated)

Compiled knowledge pages with a controlled relation vocabulary, and a review queue where the human stays the gate:

| Feature | Description |
|---------|-------------|
| LLM wiki compiler | Compiles notes into structured wiki pages (entity / concept / synthesis …) as **pending** drafts; the compiler never edits an approved page ([ADR-0015](adr/0015-w3-minimal-compile-loop.md)) |
| Review queue | Approve / reject / delete pending pages; rejected pages stay as tombstones (`reject ≠ delete`); the review page is **five tabs with count badges** (pending / merge suggestions / page scope / background jobs / error pages) — one queue at a time |
| Page versions | Every page change is snapshotted by a database trigger into an append-only `page_versions` table; line-level diff and restore ([ADR-0021](adr/0021-temporal-graph-page-versions.md)) |
| Merge suggestions | Structural candidate discovery (shared sources / slug shape / title overlap / existing links — at least two independent signals); the model adds a second opinion, and **only a human can adopt a merge**. Merges leave tombstones and re-point edges ([ADR-0019](adr/0019-entity-resolution-merge.md)) |
| Community summaries | Label Propagation over the approved graph drafts synthesis pages per topic; **opt-in gate** (`wiki_community_summary`), hard budgets, auditable outbound bytes ([ADR-0020](adr/0020-community-synthesis.md)) |
| Graph-aware evidence | `evidencePages` expands retrieval one hop along the whitelisted relation vocabulary with pure-Go PPR re-ranking; **off by default** (`wiki_graph_search`), zero regression when off ([ADR-0018](adr/0018-knowledge-graph-foundation.md)) |
| Semantic search (M3) | Optional hybrid FTS5 + vector recall (RRF-fused) with a **pluggable vector store**: local **sqlite-vec** (default, pure-Go/zero-CGO) or remote **Qdrant** / **Weaviate** (auto-falls-back to local if unreachable). **Off by default**, gated behind an A/B eval (`cmd/abeval`) ([ADR-0012](adr/0012-semantic-search.md), [ADR-0013](adr/0013-vector-database-selection.md)) |

### Built-in Backups (Core)

Because a local-first database that lives on exactly one disk is one bad sector away from a bad day:

| Feature | Description |
|---------|-------------|
| Daily local snapshots | The app snapshots the database once per day (first open of the day) via `VACUUM INTO`, into `<data dir>/backups/`; keeps 14 daily copies, 0600, **on by default with zero configuration** ([ADR-0025](adr/0025-backup-recovery.md)) |
| Restore = copy one file back | A snapshot is a complete standalone SQLite file; recovery is swapping the file. Rehearsed end-to-end on a real database |
| Off-machine (opt-in, planned) | Litestream-style WAL streaming or snapshot upload to S3-compatible storage — shaped in ADR-0025 L2, not implemented yet. Red line: a live database never sits inside a cloud-sync folder |

### Repository Mining (Core)

The project detail page auto-extracts: README summary, tech stack list (20+ manifest formats recognized), language LOC shares, dependency lists (npm / go.mod including block require / cargo), top contributors, activity stats, and the recent commit stream; results are cached in `repo_meta` to avoid rescanning.

### Dashboard & Stats (Support)

> Home is a full-page console (ask + search); the memory ring (recent handoffs) moved into the dashboard's "Sessions" tab, and project counts / added lines have exactly one authority — the dashboard. Nav order home → dashboard → knowledge → review → settings (`/search` and `/wiki` survive as redirects).

| Feature | Description |
|---------|-------------|
| Automatic repo discovery | Configure scan roots and recursively discover all Git repos; platform-adaptive defaults |
| Visual dashboard | Daily-goal progress ring, project cards, trend line charts (7 / 30 days / all), commit heatmap |
| Repo starring | Starred repos show full stat cards; unstarred repos show names only, follow on demand |
| On-demand history backfill | "Backfill history" on a starred card fetches that repo's last 365 days of daily stats |
| Smart project grouping | Auto-detects monorepos vs single repos; manual split/merge runs in a single transaction (notes and todos move with the project) |
| Workday check | Custom daily code target with alerts when unmet |
| Status bar | Live latest-commit display (repo / branch / time, 30s cache) |
| Theme switcher | Light / dark / follow-system, persisted across restarts |

### Other

| Feature | Description |
|---------|-------------|
| i18n | One-click Chinese / English switch (react-i18next, zh-CN + en) |
| Single-file cross-platform | A single Go binary, no runtime dependencies |
| Offline by design | No account, no telemetry; outbound traffic only when you explicitly configure an endpoint |
