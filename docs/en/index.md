---
title: Repotory Documentation
---

# Repotory Documentation

<p class="subtitle"><strong>The local-first memory layer for AI coding agents</strong> — automatically discover local Git projects, understand what changed and what knowledge has been captured, and hand searchable project context to any agent at session boundaries.</p>

<!--NAV_LINKS-->

## Product Positioning

The core value of Repotory: it is **the local-first memory layer for AI coding agents** — it keeps what your agents learned on your machine so the next agent can pick it up, turning local Git projects from things scattered across terminals and memory into searchable, reusable context.

That layer is **agent-neutral and protocol-neutral**: every coding agent that speaks MCP (Claude Code / Cursor / Codex / Copilot and others) reads and writes **the same** memory. Repotory is not a plugin bound to one harness — it is the neutral layer those agents share. **No account, no cloud**: the whole memory is one SQLite file on your machine.

The consequence: each IDE's or agent's own session history lives **inside its own ecosystem** (VS Code's session index is still unreadable by extensions), so the cross-agent layer needs a **neutral home of its own** — which is exactly where Repotory sits.

Current priorities:

1. **Local project understanding and knowledge context** comes first
2. The dashboard and statistics are supporting capabilities, not the main product story
3. Plugin/web-only capabilities are no longer expanded by default (the PWA has been removed from the main desktop build; see [ADR-0008](adr/0008-pwa-removal.md))

The frontend landing page is the **knowledge base**, and the navigation order is **Knowledge base → Dashboard → Settings** (see [ADR-0006](adr/0006-scope-freeze.md)).

The core loop:

```mermaid
flowchart LR
    D[Discover] --> U[Understand]
    U --> R[Capture]
    R --> S[Retrieve]
    S --> A[Hand to AI]
    A -. session boundaries .-> D
```

The five steps map one-to-one onto the five capabilities listed at the end of this section. The dashed edge is what separates Repotory from a plain notes tool: capturing knowledge is not the end state — it is the input of the next session.

The session memory loop (both ends of the protocol):

```mermaid
sequenceDiagram
    participant Agent as AI Agent<br/>(Claude Code / Cursor /<br/>VS Code extension)
    participant RN as Repotory<br/>(MCP / headless /<br/>desktop)
    participant KB as Local knowledge base<br/>(SQLite + FTS5)
    Agent->>RN: repotory_scan (first run, no desktop app needed)
    RN->>KB: seed scan roots, discover<br/>Git repos, mine knowledge
    Agent->>RN: repotory_context (session start)
    RN->>KB: tech stack / README / todos /<br/>notes, handoffs first
    KB-->>Agent: one call, full project context
    Note over Agent,KB: …work happens…
    Agent->>RN: repotory_handoff (session end)
    RN->>KB: structured handoff persisted,<br/>tagged 'handoff'
    Note over KB,Agent: the next session (any agent) reads it automatically
```

How to read it: the three participants are **three separate processes** — the AI side holds no data, Repotory does not schedule agents, and the knowledge base is the only place the two meet. The upper half is cold start (`repotory_scan`) and session open (`repotory_context`); the lower half is session close (`repotory_handoff`). That last Note is the proof the loop closes: once the handoff is persisted, **the next session — possibly run by a different agent — reads it back automatically** via `repotory_context`, with nothing carried over by hand.

The "hand it to AI" step happens at **session boundaries**: `repotory_scan` builds the knowledge base, `repotory_context` injects the full context once when a session starts, and `repotory_handoff` captures a structured handoff when it ends — reusable across agents.

- **Discover**: automatically scans local Git repositories and intelligently groups them (monorepo / single repo)
- **Understand**: project details automatically surface a README summary, tech stack, dependencies, contributors, and activity
- **Capture**: Markdown notes (categories / tags / version history), with Claude memory import
- **Retrieve**: FTS5 full-text search (with a fallback for short CJK queries); hits can be located and explained
- **AI consumption**: the MCP session memory protocol (`repotory_scan` / `repotory_context` / `repotory_handoff`) plus llms.txt, ready for direct consumption by Claude Code, Cursor, and others

Feature tiers (core / supporting / experimental / deferred) and the scope-freeze rules are described in [ADR-0006](adr/0006-scope-freeze.md).

## The Big Picture: two audiences, one set of artifacts

Repotory's artifacts serve two audiences at once: **you, sitting in the IDE** (who need to see it and click it) and **AI agents** (who need to read it and write it). Both paths share one local knowledge base — which is exactly why switching agents never loses context:

```mermaid
flowchart TB
    subgraph HUMAN["👤 Human user — visible in the IDE"]
        direction LR
        DESKTOP["Desktop App<br/>dashboard · knowledge base"]
        VSCODE["VS Code extension<br/>Cursor · Windsurf"]
        BLOG["Blog / ADRs"]
        DESKTOP ~~~ VSCODE ~~~ BLOG
    end

    subgraph AGENT["🤖 AI agent — tool surface"]
        direction LR
        MCP["repotory-mcp<br/>18 tools"]
        DSH["dsh plugin<br/>3 tools"]
        LLMSTXT["llms.txt export"]
        MCP ~~~ DSH ~~~ LLMSTXT
    end

    subgraph CORE["🧠 Local knowledge base — single source of truth"]
        SERVICE["internal/service<br/>one business logic"] --- DB[("SQLite + FTS5<br/>notes · todos · projects")]
    end

    HUMAN ~~~ AGENT ~~~ CORE
    DESKTOP --> SERVICE
    VSCODE -->|"MCP stdio + HTTP"| SERVICE
    MCP --> SERVICE
    DSH -->|"headless HTTP"| SERVICE
    LLMSTXT --> SERVICE

    INIT["⚡ repotory-init · one command<br/>register MCP · install hook"]
    INIT -.->|"writes configs"| MCP

    style HUMAN fill:#f0f7ff,stroke:#4a90d9
    style AGENT fill:#f0fff4,stroke:#4caf50
    style CORE fill:#fffaf0,stroke:#e6a23c
    style INIT fill:#fdf2f8,stroke:#d946a0
```

How to read it: **the top two rows are the shelf** — the desktop App and the VS Code extension meet humans, the MCP toolset, dsh plugin and llms.txt meet agents; **the middle is the vault** — every entry point calls the same service layer with zero logic duplication; **the wiring operator stands to the side** — `repotory-init` connects the agent side in one command (writes `.mcp.json` / `.cursor` / `.vscode`, installs the SessionEnd hook so the handoff always fires at session end). Solid arrows all point inward at `internal/service`: no entry point bypasses it to talk to the database (exceptions noted in [Architecture](architecture.md#layering-backend)), which is why all three surfaces behave identically. The only dashed line is `repotory-init`: it writes only toward the agent side because it is a **run-once-at-install** wiring action. One invisible flow is worth knowing — the Markdown export and version history of `internal/service` are readable artifacts that ultimately serve the person (the full distribution argument lives in [ADR-0009](adr/0009-ide-presence.md)).

## About These Docs

This manual covers Repotory's core features and use cases. The docs are written in Markdown (the single source of content, stored in the repository's `docs/` directory), rendered to HTML by `scripts/build-docs.mjs`, and deployed to GitHub Pages.

- **Read online**: <https://sky-jiangcheng.github.io/repotory/> — updated automatically with the master branch
- **Build locally**: `node scripts/build-docs.mjs` (uses marked from `web/node_modules`)
- **Report issues**: <https://github.com/sky-jiangcheng/repotory/issues>

## Quick Navigation

| I want to… | Go to |
|------|------|
| Install Repotory and run the first scan | [Getting Started](getting-started.md) |
| Back up data / migrate to a new machine / uninstall | [Data and Backup](data-management.md) |
| View commits / goals / heatmap | [Home workbench (Dashboard)](features/dashboard.md) |
| Search notes, todos, and projects across the board | [Global Search](features/search.md) |
| Write notes, search notes, recover old versions | [Knowledge Base and Notes](features/knowledge.md) |
| See a project's tech stack and dependencies | [Project Detail](features/project-detail.md) |
| Let Claude / Cursor read and write my knowledge base | [AI Integration](features/ai-integration.md) |
| Write a plugin or a knowledge source importer | [Plugin Guide](plugins/overview.md) |
| Understand the code layering and key decisions | [Architecture](architecture.md), [ADRs](adr/index.md) |
| Understand the storage layout and "why not let the AI read git directly" | [Storage Optimization and AI Value](storage-optimization.md) |
