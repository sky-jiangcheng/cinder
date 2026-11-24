# ADR-0073: The approval gate relaxed — from "humans only" to "human by default, optional auto-approval"

- Status: **Accepted (2026-11-24, after the TODO conflict review the owner chose "add an ADR acknowledging the change" rather than rolling back the v2.43.0 feature)**
- Date: 2026-11-24
- Related: [ADR-0015](0015-w3-minimal-compile-loop.md) (Decisions 3 and 6 narrowed by this one), [ADR-0065](0065-hindsight-absorb-not-replace.md) (definition of the "human gate" load-bearing boundary changed), [ADR-0012](0012-semantic-search.md) (privacy authorization unchanged), `docs/product-review/2026-11-23-hindsight-experience-gap.md` (gap 2.6), `TODO.md` item 1.4 (no automatic path to L3)

## Context

v2.43.0, released 2026-11-23, shipped AI-assisted review: three config keys `wiki_review_ai` / `wiki_review_auto` / `wiki_review_timeout_hours`, two of which provide an **automatic approval** path (AI says approve + rules pass; or timeout + rules pass).

Reviewing the TODO on 2026-11-24 surfaced a direct conflict with three established positions:

| Established position | Original constraint | What v2.43.0 actually does |
|---|---|---|
| ADR-0015 Decision 3 | "approval becomes a transition **only a human can trigger**, not something a model can accomplish itself" | `autoApprovePage` can approve with no click |
| ADR-0015 Decision 6 | "**no scheduler** ... compilation must be triggered explicitly by a human" | the `wiki_review` ticker processes pending pages every 5 minutes |
| TODO 1.4 (L3 global pages) | "there is **no automatic path** to fill L3" | timeout auto-approval can let a page into search and L3 evidence |

This is not an implementation drift but a **product direction change**: human review moved from "the only entry" to "the default entry". This ADR records the change instead of hiding an already-released feature.

## Facts (conflict review, 2026-11-24)

- All three config keys are **off by default**: without opt-in, review behaviour is exactly as ADR-0015 specifies (human clicks approve, no ticker action).
- Both automatic paths run behind a deterministic veto: empty title / empty slug / body too short / placeholder markers (TODO / 待补充) — no AI verdict or timeout can override it.
- Auto-approval reuses `ApproveWikiPage`; mentions, `page_versions` snapshots and the disk projection are identical to a human approval — **no second write path**.
- The `wiki_review` ticker touches only the review chain; compilation still has no scheduler (ADR-0015 Decision 6 remains in force for compilation).
- A human edit clears the stale AI verdict, so an automatic decision about old content cannot linger.

## Decision

1. **The gate definition is relaxed**: from "only humans can approve" to "**human by default, auto-approval by explicit opt-in**". The three config keys stay off by default; with them off, every ADR-0015 constraint applies unchanged.
2. **Exactly two narrow auto-approval paths**: `wiki_review_auto=1` with an explicit AI approve and rules passing; or `wiki_review_timeout_hours>0` with the page past the human window and rules passing. The deterministic veto covers both, including timeout.
3. **No new write path**: auto-approval goes through the same `ApproveWikiPage`; `review_source` only records `ai` / `auto` / `timeout` / `rule` — no content semantics are changed and no approval side effect is skipped.
4. **ADR-0015 text preserved + a banner**: Decisions 3 and 6 stay verbatim with a pointer banner to this ADR; constraints on the compilation chain (no scheduler, not in auto_import, not an MCP tool) **remain verbatim in force**.
5. **ADR-0065 moat definition synced**: the "human gate" changes from a load-bearing boundary to "**the human-default shape** is the moat; auto-approval is a deliberate user deviation". The zero-model capture and single-file local distribution constraints are unchanged.
6. **TODO 1.4 narrowed**: "no automatic path can fill L3" becomes "no **default-on** automatic path can fill L3"; opt-in auto-approval is an explicit, source-recorded exception.

## Cost

- **Moat dilution risk**: the default-off setting holds the line, but once auto/timeout is enabled a wrong page can enter search with no human signature — risk moves from "architecturally impossible" to "the user consciously accepted it". Review annotations (`review_verdict/reason/source`) are the only retrospective audit trail.
- **Documentation reversal cost**: ADR-0015's "only a human can trigger" was a core argument at the time; this ADR must declare it overturned or later readers will inherit a wrong product constraint.
- **Timeout approval is a product judgement, not a technical conclusion**: "the human window expired" is taken as implicit trust; nothing re-evaluates correctness at that moment. It is a partial convergence with Hindsight's "auto consolidate" route, deliberately without its LLM extraction or cloud shape.

## Reversal triggers

- Auto/timeout-approved pages are repeatedly rolled back by human review (reject / body edit) → withdraw auto-approval, restore ADR-0015 (disable both keys and remove the paths in a later version).
- Once gap 2.6's evidence revision (observations-style pending form + proof count) lands → the basis for auto-approval upgrades from "rules + AI suggestion" to "evidence citations and proof counts"; Decision 2 must be re-ratified.

## Landing record (v2.43.0, 2026-11-23)

- Policy and judgement: `internal/service/wikireview/review.go` (config, hard veto, AI verdict parsing, AI call).
- Scheduling and persistence: `internal/service/wiki_review.go` (5-minute ticker, `autoApprovePage`), `internal/service/service.go` (startup hook).
- Storage: schema v32 adds `review_verdict/reason/source` to `wiki_pages`; `ExpectedSchemaVersion=32`.
- Config: `internal/service/config.go`, three keys off by default; negative or >8760 timeout hours normalize to zero.
- UI: Settings → AI → `WikiReviewSection`; verdict badge on review cards; zh/en copy shipped.
- Verification: `go test ./...` green; 432 frontend cases green; govulncheck 0 called vulnerabilities.
