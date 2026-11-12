# ADR-0060: Split `internal/service` by domain — from one struct to independently evolving packages

Status: **Accepted (raised by the 2026-11-12 engineering assessment; the owner chose option A, strangler by domain, the same day; step one `review` → `internal/service/reviewrun`, step two, the wiki read side, → `internal/service/wikipage`, step three, the model-driven wiki write side (compile / merge / community), → `internal/service/wikigen`, and step four, lint, → `internal/service/wikilint`, all landed 2026-11-12; step five, evidence, was **decided not to move** after discussion — judged a cross-domain orchestrator — and the series closes; tracked as TODO 13.3)**
Date: 2026-11-12
Related: [ADR-0005](0005-service-layer.md) (service / app / domain layering), [ADR-0055](0055-agent-surface-contract-and-observability.md) (explicit non-goal: "do not rewrite the service layer"), [ADR-0057](0057-write-idempotency-and-version-history.md) (write-path contract)
Starting schema: v31 → **v31 (no schema change)**; `CoreVersion` unchanged

## Background

`internal/service` measures **33,886 lines across 117 files and 158 exported `*Service` methods** as of 2026-11-12. A single `Service` struct holds the database, config, LLM, vectors, scanner and logger (`internal/service/service.go:31-140`), and the same package also carries notes, wiki compilation, review, importers, backups, health checks and AI tool calls. On the storage side, `internal/db` exposes 211 package-level functions with no repository interface; the service calls them by name.

ADR-0005 settled `service / app / domain` as three layers, not "all business logic in one package". ADR-0055 then stated an explicit non-goal: **do not rewrite the service layer** — that ADR was about the exit contract, budgets and observability, not a layering refactor. Together the effect is that **layering is acknowledged while splitting is postponed indefinitely**, and every cross-domain change lands on the same package and the same struct.

Measured cost (2026-11-11 assessment): `go test ./internal/service` takes 13.9s for one package; changing a search ordering requires reading the wiki, importer and handoff paths together; answering "what does this change affect?" means reading the whole package. The surface keeps growing (review, importers, chat and observability all arrived within two months), so the cost only rises.

## Decision (owner, 2026-11-12)

**Option A, strangler by domain**: move code out of `internal/service` into `internal/<domain>` one domain at a time, keep `Service` as the facade plus cross-domain orchestration, and make every step independently mergeable and revertible.

Extraction order (each step requires `go test ./...` green, with the 22-tool fixture and `CoreVersion` untouched):

1. **`review`** (the rule layer already lives in `internal/review`; only orchestration remains in service) and **`importers`** (parsing and persistence have a clean boundary and their own tests). These have the narrowest side effects and the fewest dependencies, and they are what proves the "no cycles after extraction" property itself.
2. **`wiki`** (compilation, approval, versions) — the most entangled piece, moved only after step one has shown the pattern holds.
3. **`ai`** (the three HTTP primitives plus eight business entry points) — the closing move.

Three hard constraints, so "extraction" cannot become "the same monolith in a different package":

- **Sub-packages never take `*Service`.** Dependencies arrive as arguments (`*sql.DB`, config values, plain data), otherwise the cycle merely moves from inside a package to between packages.
- **`Service` keeps the facade**: `internal/app` bindings and the 22 `cmd/mcp` tools keep calling `Service` methods, with zero call-site changes.
- **Cycles are prevented by a gate, not by discipline**: `go list -deps` asserts that `internal/<domain>` never depends back on `internal/service`.

Costs, recorded: a thin but real forwarding layer during the transition; `git blame` readability drops once when code moves; "which domain first" is judgement rather than derivation, and choosing wrong costs ordering, not irreversibility — which is exactly why A was chosen over C.

## Open questions (answered above; original text kept)

1. Split or not: accept the single package, or start paying the split cost now?
2. What is the boundary: MCP tool domain, data ownership, or "pure logic vs side effects"?
3. Introduce repository interfaces, or keep sub-packages taking `*sql.DB` directly?
4. Does the split have to keep one binary and one MCP tool surface? (Yes — but that must be written down.)

## Options and trade-offs

| Option | Approach | Upside | Cost / risk |
|---|---|---|---|
| A Strangler by domain | Extract `review`, then `importers`, then `wiki`, then `ai`; `Service` keeps the facade and cross-domain orchestration | Incremental and reversible; tests move with code; every step can stay green | Transitional forwarding layer; import cycles to guard; pace depends on discipline |
| B Extract only pure domains | Move `review` rules and `importers` parsing (no side effects) out first | Lowest risk, immediate benefit (independently testable) | The core monolith stays; cross-domain cost remains |
| C Repository interfaces + full rewrite | Introduce repository / CQRS and redraw boundaries by aggregate | Cleanest long-term boundaries | Most expensive; directly contradicts ADR-0055's non-goal; no user-visible benefit now; largest regression surface |
| D Keep it, add conventions | Segmented file naming, per-domain test entry points, ownership comments | Zero change | Does not reduce review surface, compile time or entanglement |

## Explicitly undecided

- ~~No option is chosen and no timeline is promised~~ — **option A chosen on 2026-11-12**; no timeline is promised, but the order is fixed (review/importers → wiki → ai).
- No MCP tool surface, binding, schema or binary split changes.
- No big-bang rewrite, and no repository interfaces (C): the 211 package-level functions in `internal/db` stay as they are — extraction does not change how data is accessed.

## Acceptance criteria

- Step one (`review` + `importers`): `internal/service` line count and exported method count drop **measurably**, `go test ./internal/service` gets faster, and existing tests pass **untouched apart from import paths**.
- `go list -deps` shows no import cycles (sub-packages never depend back on `internal/service`); the `internal/app` surface registry tests are unchanged.
- Behaviour is unchanged: the 22-tool fixture and `CoreVersion` do not move; call sites in `internal/app` and `cmd/mcp` are unchanged.

## Implementation record (2026-11-12, step one)

What landed: `internal/service/reviewrun` (5 implementation files plus tests). `internal/service` keeps only the facade, the refiner and the ticker.

**Only `review`; there was no `importers` to move.** Checking the code first paid off: service has no importer domain — importing moved into `internal/core/plugin/runtime` during the 12.x batch, and the only thing left behind, `auto_import_test.go`, pins a config predicate (`auto_import` must be an explicit opt-in), not parsing or storage. The "two blocks in step one" judgement was half right; this records what actually happened.

Three acceptance criteria did not hold, and the honest numbers:

| Criterion | Result | Why |
|---|---|---|
| `internal/service` line count drops | **Met**: 33,886 → 33,075 (−811) | 1,013 implementation lines and 888 test lines moved out; about 1,090 lines of facade and comments came back |
| Exported method count drops | **Not met**: 158 → 158 | The facade is deliberate, and all twelve `Review*` methods have external callers (8 in `internal/app`, 4 in `cmd/mcp`); removing any of them changes the binding surface |
| `go test ./internal/service` gets faster | **Not met**: 13.9s → 13.8s | Those 13.9s are spent in scan and wiki fixtures (git checkouts, embeddings), not in review; moving 0.37s of review tests out cannot move the reading |

Conclusion: **method count and in-package test time were the wrong metrics for this step**, and writing them into the criteria was wishful. What is actually measurable is that review can now be constructed without a `*Service`, and that the dependency direction is pinned by one `go list -deps`. Later steps (wiki, ai) should be judged by "the sub-package is independently constructible" and "a cross-domain change touches how many packages", not by a total line count used as a progress bar.

The only cross-package dependency is the model call. `reviewrun.Refiner` has exactly one method, `Verdicts(ctx, path, content, chunk)`, implemented by the service-side `reviewRefiner` (endpoint, key and the ADR-0030 audit row stay in service). `reviewrun.ErrUnavailable` separates "no endpoint configured" from "called and failed": the first logs one skip and keeps the run rule-only, the second counts toward `ai_failures` and writes the count into `review_runs.error`. That split was moved over verbatim from the old implementation, not newly decided here.

Test changes: `reviewrun` gained 7 cases (defaults, config validation, `ParseClock`, plus the three white-box `repos` / `ruleSet` cases moved out of service). On the service side `review_test.go` and `review_notify_test.go` are **untouched**, and `review_ai_test.go` changed six references (`reviewrun.Refine` / `ParseVerdicts` / `SystemPrompt` / `KeyAI`).

No cycles, verified: `go list -deps ./internal/service/reviewrun` does not contain `repotory/internal/service`. `go test` is green across 42 packages (41 before plus the new one), and the MCP tool fixture and `CoreVersion` did not move.

## Implementation record (2026-11-12, step two: the wiki read side)

What landed: `internal/service/wikipage` (`page.go` / `versions.go` / `suggest.go` / `scope.go` / `export.go` / `feed.go`, plus 8 test cases).

**wiki did not move as one block, because the coupling inside it is not uniform.** Ranking the twelve wiki implementation files by how much they touch `Service` produced three layers:

| Layer | Files | Dependencies beyond `db` |
|---|---|---|
| Read side | browse / page_versions / suggest / scope / export | almost none |
| Write side | compile / merge / lint / community | all need a model (`aiChatConfig` + `chatWithAudit`) |
| Scheduling and answering | compile_job / evidence | model + retrieval + `bgGo` / `finishJob` / `toolRegistry` |

So this step moved the first layer only. That is not "start with the easy ones": the read side goes without **any** interface, and that fact is itself the reason to start there — if even it needed a seam, the boundary judgement would be wrong.

`wikiReviewFeed` and `WikiPendingPage` moved with it (`feed.go`). Despite living in `wiki_compile.go`, it is a pure read projection shared by three entry points (the review queue, the scope page, and the compiler's "what did I just produce"). Leaving it on the write side would have made the read side depend on the compile pipeline for a return type.

**Two shared text utilities were exported rather than copied**: `WikiLinkRe` (the `[[link]]` pattern, used in four places) and `FirstLine` (used by `context_layers.go` for summaries). A duplicated copy of these drifts, and the drift shows up as two contradictory renderings of the same page. `clipRunes` (four lines of rune truncation) stays duplicated for now, with a comment saying that the third caller is the moment to extract it — a `textutil` package for four lines would be more indirection than it saves.

Numbers: `internal/service` 33,075 → 32,340 (−735 more); exported method count still 158 (facade, same reason as step one); `go test ./internal/service` still around 13s (the wiki read-side tests were only 1.3s of it, and they still run through the facade). The sub-package is 1,025 lines including tests.

Tests: `wiki_browse_test.go` / `wiki_export_test.go` / `wiki_scope_test.go` / `wiki_suggest_test.go` on the service side pass **untouched** (through the facade, end-to-end behaviour unchanged). The sub-package adds 8 cases, all on an in-memory database, pinning "an empty feed must be `[]` not null", "a pending page is not readable", "promotion is idempotent", and "a CJK snippet never splits a character".

`go list -deps ./internal/service/wikipage` does not contain `repotory/internal/service`. `go test` is green across 43 packages.

**What each remaining wiki block will need** (written down now so the next step is not decided on the spot):

- compile / merge / community: one "messages in, reply out" call. The shape matches `reviewrun.Refiner` but not the semantics (generation, not adjudication), so whether to share one interface type or give each a small one depends on whether the zero-candidates-zero-calls style budget has to be preserved.
- lint: model plus `bgGo`. The job lifecycle belongs to the service; the lint rules can move.
- evidence: the heaviest. Retrieval, streaming answers, the tool registry and `bgCtx` all meet here — whether to move it at all deserves its own discussion rather than being assumed to be part of "finishing the job".

Next, in the fixed order: compile / merge / community as one group, since they share one kind of seam.

## Implementation record (2026-11-12, step three: the model-driven wiki write side)

What landed: `internal/service/wikigen` (`seam.go` / `config.go` / `findings.go` / `compile.go` / `merge.go` / `community.go`, plus 4 test cases; 2,166 lines including tests).

**First, the point left open before the work started — whether to reuse `reviewrun.Refiner`'s interface type. It cannot be reused, literally**: `Refiner.Verdicts(ctx, path, content, chunk)` is typed on `review.Finding` / `Verdict`, so "reuse the same interface type" would really mean lowering it to "messages in, reply out", which is a new interface. The real choice was between one domain-shaped interface per kernel and one narrow transport seam shared by the group; **the transport seam won**, because the difference between generation and adjudication lands on **how a parse failure is disposed of**, not on the verb:

- Adjudication (review) fails to parse **silently**: `ParseVerdicts` returns nothing and the rules' verdicts stay, so the runner never owes the user an explanation. Parse can live behind the bridge, and the interface can be domain-shaped.
- Generation (this group) fails to parse as **part of the output**: each kernel owes the user a different sentence ("本次不写入任何东西" / "本次不采信任何合并建议" / "建档但正文留空"), and telling a truncated JSON array from a malformed one needs the raw `finish_reason`. A domain-shaped interface would either return raw text (a `JudgePairs` that delivers prose is a lie) or drag parse-failure phrasing across the package boundary.

The seam carries exactly two things: `Message{Role, Content}` (**deliberately without ToolCalls / ToolCallID** — a generation kernel structurally cannot express a tool call, the same discipline as `compileOp` having no update/delete) and `Reply{Text, FinishReason}`. The `ErrNotConfigured` sentinel plus `Available(ctx)` let a kernel produce the "AI endpoint not configured" note with zero calls (compile and community return before counting anything; merge skips before reading candidates — the control-flow order was diffed verbatim against the pre-split code). The endpoint, the key and the ADR-0030 audit row stay in `internal/service`: the one bridge, `wikigenChat` (`wikigen_bridge.go`, two methods), resolves config, narrows `[]Message` into `[]chatMessage`, and maps any `aiChatConfig` failure to `ErrNotConfigured`.

**Every budget rule is preserved, and none of them lives in the seam** — they were always control flow inside the kernels: merge's "zero candidates, zero calls" (`len(cand.Candidates)==0 → return`), compile's page and note-byte ceilings, community's wall clock and its gate placed after the free detection half. The control flow moved verbatim, unchanged. The sub-package adds 3 tests built on a **counting fake**, pinning "gate off / no endpoint / zero candidates ⇒ zero calls" at the seam — a proof that only became writable once the transport was the thing being counted.

Dependency direction: `wikigen` does not import `internal/service`; `service` imports `wikigen` one way (bridge, facades, aliases).

**Two "third caller" IOUs were paid in the same batch**:

- `clipRunes`: step two noted "four lines, kept duplicated, the third caller is the moment to extract". `wikigen` was that caller. The definition moved to `wikipage.ClipRunes` (wikipage already hosts the wiki text utilities), `internal/service` keeps a wrapper, and `wikigen` calls it by the old name — three copies collapsed to one.
- `DefaultBatchChatTimeout`: it exists because of this group's calls (a local 27B model measured 173s for one compile), so the definition moved with its callers into `wikigen.BatchTimeout`, and `internal/service` keeps `const DefaultBatchChatTimeout = wikigen.BatchTimeout` as an alias — `cmd/server`'s assertion that its write deadline sits above the batch ceiling, and the test around it, are unchanged.

**Where the shared vocabulary went**: `WikiFinding` / `LintTodoTitle` / `TruncatedHint` moved into `wikigen` with the kernels (merge emits findings; compile and community render revision todos through `LintTodoTitle`; all three need `TruncatedHint` to separate truncated from malformed). The `internal/service` side keeps **aliases or wrappers** throughout: `type WikiFinding = wikigen.WikiFinding`; `lintTodoTitle` / `truncatedHint` / `wikiLintLLMEnabled` / `communitySizeThresholds` forward; `CommunitySynthesisSlug` / `CommunitySynthesisTitle` wrap. Ten const aliases (config keys, source tags, budget constants) are fixed to the same values by the compiler and **cannot drift**. `truncateBytes` / `firstNonEmpty` are used on both sides and keep a second copy each, with a note that a third package is the moment to extract them — the same tolerance this ADR has applied throughout, rather than rebuilding a `textutil` package for a few literals.

**Test changes (recorded honestly; these are the places that differ from "existing tests unchanged apart from import paths")**:

| Change | Why |
|---|---|
| `TestMergeSimilarityHelpers` moved into `wikigen/merge_test.go` | a pure-helper white-box test, no database and no service (the same move step one made for the `repos` / `ruleSet` white-box cases) |
| `TestCompileNoteTargets_SharesSelectionWithSyncPath` changed one reference | it asserts that the job path and the kernel agree on "which notes", a service-side claim, so it stays in place and calls `wikigen.CompileNoteTargets` |
| every other reference unchanged | config keys / tags / constants are covered by const aliases; functions go through wrappers |

Numbers: `wikigen` is 2,166 lines across 8 files (including tests); `internal/service` now has 118 files and 30,746 lines at the top level (**a snapshot** — that directory also carries steps one/two and other uncommitted work in progress, so this is not step three's isolated delta); the exported `*Service` method count is 158 → 158 (facade kept, same reason as the first two steps). `go list -deps ./internal/service/wikigen` does not contain `repotory/internal/service`. `go test ./...`: `wikigen` and `internal/service` pass; 43 of 44 testable packages are green — the red one is `internal/db`'s `TestCreateRollingBackup_WritesIntradaySnapshots`, caused by the uncommitted rolling-backup work in progress (`internal/db/backup.go`, +229 lines), unrelated to this split (`db` does not import `service`, and neither file was touched here). The MCP tool surface and `CoreVersion` are unchanged.

**Two steps left**: lint (its model half has the same shape as merge and can adopt this record's seam directly; the job lifecycle stays in the service) and evidence (retrieval, streaming and the tool registry — whether to move it at all deserves its own discussion).

## Implementation record (2026-11-12, step four: lint)

What landed: `internal/service/wikilint` (`lint.go` / `model.go`, plus 2 test cases; 726 lines including tests).

**The boundary follows this ADR's own words exactly: "the job lifecycle belongs to the service; the lint rules can move".** What moved: the rules and the output — the four structural checks (orphan / dangling-link / missing-ref / thin-page / no-source / no-synthesis / the three relation-shape checks), the model half, the report assembly, and `fileFindings` (which files the todos). What stayed: scheduling — the `bgGo` ticker, the lint job queue in `wiki_compile_job.go` and `lintStopNote`. The `Service` keeps the `RunWikiLint` facade, so the ticker, the job worker and the Wails binding all call the same name and signature; `WikiLintReport` is an alias.

**The model half adopts step three's `wikigen.Chat` seam rather than inventing one.** It has the same shape as merge-suggest (a batch of pages in, findings plus a "why it did not run" note out) and merge-suggest is already a wikigen kernel called from this very pass, so the two model halves naturally share one transport — which also makes "gate off / no endpoint ⇒ zero calls" assertable on the lint side with a **counting fake** (the sub-package adds 2 cases; every gate runs before anything is serialised). Dependency direction: `wikilint` → `wikigen` (`Chat` / `WikiFinding` / `LintTodoTitle` / `LintLLMEnabled` / `SuggestMerges`) plus `wikipage` (`WikiLinkRe`), one way, acyclic.

**`WikiFinding` stayed in `wikigen` rather than following lint.** That is forced by the dependency direction: merge-suggest lives in wikigen and emits findings, so moving the type to wikilint would make `wikigen` import `wikilint` while `wikilint` calls `wikigen.SuggestMerges` — a cycle. The finding type is therefore write-side-kernel vocabulary and lint consumes it, not the reverse.

**The third package needing `truncateBytes` appeared** (step three's second copy in `wikigen` carried a note: "a third package needing it is the moment to extract"): wikilint is that third package. The definition moved to `wikipage.TruncateBytes` (the byte-wise twin of `ClipRunes`), with `internal/service` and `wikigen` each keeping a one-line wrapper — one definition, three users, and the drift risk is gone.

**Test changes (recorded honestly)**: `TestParseLintReply` (the pure reply parser) moved into `wikilint/model_test.go`; every other lint test is unchanged — they run through the `RunWikiLint` facade and name the `wikiLintLLMKey` / `lintTodoPrefix` constants, both of which were already aliased in step three. The sub-package adds 2 counting-fake tests.

Numbers: `wikilint` is 726 lines across 4 files (including tests); `internal/service` 30,746 → 30,163 (snapshot basis as before; the directory still carries earlier steps and other work in progress); the exported `*Service` method count is 158 → 158. `go list -deps ./internal/service/wikilint` does not contain `repotory/internal/service`. `go test ./...`: 44 of 45 testable packages green; the single red is still `internal/db`'s pre-existing failure (rolling-backup work in progress). The MCP tool surface and `CoreVersion` are unchanged.

**Only evidence is left**: retrieval, streaming, the tool registry and `bgCtx`. As this ADR already said, whether to move it at all deserves its own discussion — none of its dependencies is a "messages in, reply out" seam of the same shape, so this record's pattern does not transfer to it.

## Implementation record (2026-11-12, step five: evidence — decided not to move it; the series closes)

**Decision: evidence does not move.** It is not "the next domain"; it is the boundary where this strangler pattern runs out.

**The closing rule** (written for the next person who wants to move something): a unit moves only when its dependency surface is **small and stable**; something that composes N domains that have not moved yet is an **orchestrator**, and orchestrators stay in `Service` — which is this ADR's own decision-section wording ("`Service` keeps the facade and cross-domain orchestration").

evidence (`wiki_evidence.go`, 897 lines; its exported surface is `GatherEvidence` / `AskAIWithEvidence` / `AskAIWithEvidenceStream` / `StartAskStream` / `FileAnswerAsPage` plus a few types) has not one `s.` dependency but **six domains that have not moved**:

| # | Dependency | Symbols | Lives in |
|---|---|---|---|
| 1 | Transport | `aiChatConfig` / `askWithTools` / `streamAskWithTools` / `truncatedHint` / `aiAskTimeout` | `ai.go` / `aitools.go` |
| 2 | Retrieval | `SearchNotes` / `semanticEnabled` / `pageVectorRecall` / `graphSearchEnabled` / `graphNeighborOrder` | `search.go` / `search_semantic.go` / `wiki_graph_evidence.go` |
| 3 | Tool loop | `toolRegistry.definitions()` | `aitools.go` |
| 4 | Context / memory | `BuildLayeredMemory` / `aiProjectBaseContext` / `LayerBudget` | `context.go` / `context_layers.go` / `ai.go` |
| 5 | Time source | `s.git.GetTodayDate()` | scanner provider |
| 6 | Lifecycle | `bgGo` / `bgCtx` / `askStreams` | `service.go` |

Compare the first four steps — step one was one `Refiner` method, step two was almost nothing, steps three and four were one seam each (step four plus wikipage) — and five of evidence's six dependencies are **domains that have not moved**. Moving it would need five or six ports, and every one of those ports would just re-inject `Service`'s existing methods under a new name: **the isolation would be fake** (search / tools / context change and evidence still changes with them, only now through a port), the review surface would not shrink, and there would be one more forwarding layer.

The decision section already foreshadowed this: extraction item 3 was "**`ai` (three HTTP primitives + eight business entry points) — the final close-out**". evidence belongs to that bucket, and that bucket's shape differs from the four wiki steps: it is not "one domain, one seam" but transport + tool loop + context + question answering interlocking as one block, and it needs its own ADR and design rather than being an extension of this series.

**If it is ever to become movable**, its six dependencies must first become domains: `search` (`SearchNotes` + semantic + graph retrieval), the `ai` transport and tool loop (`ai.go` + `aitools.go`, about 2,100 lines — the largest single block left in `internal/service`), and `context` / memory. Until then, moving evidence would only trade one orchestrator for six ports.

**The series closes here**: steps one through four are the incremental, revertible extractions this ADR promised; evidence staying in `Service` by the rule above is part of this ADR's boundary work, not an unfinished item. Any future shrinking of `internal/service` should target the **dependency domains** in that table (especially the `ai` cluster), not this orchestrator.
