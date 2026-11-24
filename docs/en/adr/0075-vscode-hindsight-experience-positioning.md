# ADR-0075: VS Code / Hindsight experience benchmark — same-layer competition, full-stack divergence and layout subtraction

- Status: **Accepted (2026-11-24, added at the user's request; this ADR only records conclusions — no code and no product commitment changes)**
- Date: 2026-11-24
- Related: [ADR-0065](0065-hindsight-absorb-not-replace.md) (overall route: absorb, do not replace), [ADR-0007](0007-session-memory-protocol.md) (handoff protocol), [ADR-0015](0015-w3-minimal-compile-loop.md) (human approval gate), [ADR-0034](0034-desktop-three-column-shell.md) / [ADR-0038](0038-desktop-information-architecture.md) / [ADR-0039](0039-in-page-tabs-over-stacked-sections.md) / [ADR-0042](0042-page-shell-pinned-header-and-scrolling-body.md) / [ADR-0044](0044-ide-panel-windsurf-shape.md) / [ADR-0050](0050-family-design-standard.md) (existing layout-subtraction decisions), `docs/product-review/2026-11-02-vscode-session-memory.md`, `docs/product-review/2026-11-23-hindsight-experience-gap.md`

## Background

The 2026-11-02 VS Code study and the 2026-11-23 Hindsight benchmark each produced a product-review document. ADR-0065 recorded only the overall route — absorb selectively, do not replace the foundation. It did not record three finer conclusions: (1) which layout simplifications remain reasonable against a VS Code-grade experience; (2) whether Hindsight is merely a storage layer; (3) why the heaviest parts of Hindsight's full stack diverge so sharply from Cinder, and whether the reason is commercial cloud productization.

This ADR lifts those three conclusions from the review notes and conversation to decision level, so benchmarking does not turn back into a feature arms race. **No new functionality, no scheduling, no schema / MCP surface / CoreVersion changes.**

## Facts

### 1. VS Code has three layers; they are not one thing

- **Session storage**: local SQLite, on by default, but IDE-private. `microsoft/vscode#318855` is still a proposal — extensions have no supported API to read other sessions.
- **Session sync / Copilot Memory**: cloud, GitHub-account bound, opt-in sync, Repository/User scopes, 28-day inactivity deletion.
- **Local memory files / Chronicle**: the first 200 lines of User memory are injected at session start; `/chronicle:*` searches a local session index and produces standups.

VS Code's "Hand off a session" means **switching harness / execution environment / role at runtime**, carrying the full conversation. Cinder's `repotory_handoff` is a **persisted handoff record**. Same word, different thing — a recurring product-language hazard (recorded in the 2026-11-02 review).

### 2. Only three real same-layer competitions

The first-intuitive entry for "what did we do last time", Copilot Memory's Repository scope, and discovering/opening Claude Code / Codex sessions. All three reduce **user recall** cost, but none takes over Cinder's position: **agent-neutral, protocol-readable, agent-written structured conclusions, local and account-free, human-default**. Transcript is not a conclusion; discovery is not reading; an IDE internal detail is not a knowledge asset any MCP client can consume.

### 3. The layout conclusion is "confirm subtraction", not "add panels"

Against a VS Code-grade experience, the reasonable layout moves match decisions already made in ADR-0034 / 0038 / 0039 / 0042 / 0044 / 0050: hierarchy rebalancing, contextualizing the persistent right rail, keeping body text at `max-width:72ch`, keeping the floating bot off the body, in-page tabs instead of stacked sections, pinned header with a scrolling body, and chrome reduction.

**No new layout decision.** This benchmark only confirms the existing subtraction direction; it does not open another UI round to match one IDE's layout. Any future layout action needs its own ADR with real-device evidence and the correction-count metric — screenshots alone do not justify a project.

### 4. Hindsight is not a storage layer

Hindsight is a **full-stack memory product with extraction, retrieval, injection, wiring and enterprise operations**: Postgres + pgvector is only its storage choice. The weight sits in mandatory LLM extraction on `retain`, four memory layers (facts → experiences → observations → mental models/pages), automatic consolidation with evidence revision, four-way recall + RRF + cross-encoder reranking, three in-house small models, Memory Defense, one-click wiring for 22 coding agents and `fs mount`. Reading it as "a better database" misclassifies the gap — the difference is a product stack, not a db selection.

### 5. The three heavy segments diverge because the objective function differs

The three heaviest segments — automated ingestion plus LLM extraction, consolidation / triage plus belief revision, and retrieval reranking plus full-harness wiring and cloud operations — diverge because **Hindsight is a full-time commercial team pursuing cloud, multi-tenancy and an enterprise surface**: broader harness coverage, zero-config ingestion and full-stack experience are its growth path; Postgres / Docker / Helm / Oracle / Prometheus are that path's operational shape.

Cinder's objective function is **a single local file, no account, protocol neutrality, a human-default gate and a zero-model capture path**; a solo maintainer ranks agents actually used on the machine and does not chase 22 integrations. The divergence is not a backlog of missing abilities — it is two product targets. Copying the other stack means entering its home field and abandoning ADR-0006's scope freeze and ADR-0065's moat.

## Decisions

1. **Keep ADR-0065 route A**: absorb selectively, no replacement and no hybrid dual store. This ADR adds no code and no new scheduled absorption item.
2. **Classify benchmarks as same-layer / different-layer**: VS Code's session storage and retrieval are a layer to benchmark against but not to fight; Hindsight's ingestion / consolidation / reranking / wiring belong to a full product stack and go only through ADR-0065's selective-absorption process, item by item in later ADRs (landed: 0066–0074; uncommitted items stay unscheduled).
3. **Layout: confirm existing subtraction only.** No new UI round. Real-device data plus a correction-count delta are prerequisites for any future layout ADR.
4. **Explicitly do not copy the commercial surface**: cloud / multi-tenant / Helm / Oracle / Prometheus operations, the 22-harness coverage race, disposition traits, and deep `reflect` — all orthogonal to the single-file promise and too heavy for a solo maintainer. `repotory_ask` (ADR-0033) already owns "answers with provenance".
5. **The experience north star stays fixed**: reduce manual corrections — not solving rate, not recall@10.

## Consequences

- Gained: competition signals are not misread as a feature checklist; the moat (human-default review + single local file + zero-model path + cross-agent protocol neutrality) is not diluted by benchmarking; already-landed ADRs 0066–0074 have a clear place in the story.
- Lost: accepted gaps in coverage, cloud sync, enterprise operations and automatic consolidation. These are chosen differences, not pretending to catch up.
- Cost containment: only items that directly reduce corrections without breaking the three load-bearing boundaries get their own ADR; the rest stay in the review notes as observation signals.

## Reversal triggers

- An external requirement makes "Hindsight ecosystem compatibility" a selling point → reopen ADR-0065 route C (hybrid dual store); this ADR grants no such authority.
- Real-device data shows the existing layout subtraction still significantly raises corrections → open a new layout ADR with before/after measurements.
- VS Code Chronicle moves from transcript standups to structured session conclusions and becomes the first entry for recall → rerun the signal evaluation in section 4.3 of the 2026-11-02 review before changing `repotory_context` presentation.

## Landing record

- 2026-11-24: user confirmed "add it". This ADR, both ADR indexes and cross-references in both product reviews land together; no code changes.
