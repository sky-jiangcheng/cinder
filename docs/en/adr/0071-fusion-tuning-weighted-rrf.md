# 0071 Retrieval fusion tuning — weighted RRF + sharper k + wider vector recall; kind noise demotion rejected

- Status: **Accepted (2026-11-23, owner approved the first half of ADR-0065 option a — "the two zero-dependency experiments"; landed after the experiments cleared the gate)**
- Related: [ADR-0012](0012-semantic-search.md) (semantic default-off = privacy authorization), [ADR-0018](0018-knowledge-graph-foundation.md) (retrieval foundation / A/B gate discipline), [ADR-0065](0065-hindsight-absorb-not-replace.md) (selective absorption; the prerequisite experiment for option b reranking), [ADR-0070](0070-temporal-recall-channel.md) (the temporal third arm)

## Background

After ADR-0070 landed, the 70-case production path scores hybrid+temporal **recall@10=0.986 / ndcg@10=0.808**. Recall is near saturation (only 2 partial misses); the residual gap is entirely **ranking depth**: 10 cases with rank1≥4, per-class ndcg knowledge 0.753 / memory 0.687 / untagged 0.792. This is the prerequisite experiment for ADR-0065 option b (RRF tuning → rerank model): run the zero-dependency fusion-parameter experiment first, then decide whether to add a reranker.

Both experiments are designed as **capture once, replay locally**: `.annotation-prep/fusesweep/capture` records each case's raw ordered id lists from the lexical / vector / temporal arms (the vector arm needs only one remote query embedding per case, ~4 minutes for 70), after which the parameter sweep is entirely local with zero remote cost. The offline replay reproduces production's two-stage RRF (semantic fusion then temporal fusion); the baseline config (k=60 / embedK=20 / tempK=20 / equal weights) reproduces **recall=0.9857 / ndcg=0.8097**, matching the live gate — the replay is faithful.

## Decision

1. **Weighted RRF**: `hybrid.FuseRRFWeighted(k, lists...)`, each list carrying a weight, score = Σ weight_i/(k+rank_i). `FuseRRF` is refactored into the equal-weight (weight=1) special case — its signature and behavior are unchanged. Its only production caller is note search's `fuseRankList` (`db/pagevec.go` / `wiki_evidence.go` mention it in comments only and actually use round-robin, so they are unaffected).
2. **Vector arm weight 1.5, lexical / temporal arms 1.0**: vector recall speaks louder in the fusion. The temporal arm's weight is untouched (the temporal class is already saturated; reweighting yields nothing).
3. **Note-search fusion constant k=10** (`noteRRFK`), sharper than the generic `DefaultRRFK=60`. **Scoped to note search only** — the global constant is not changed. k=10 is what removes the "raising the vector weight alone drops one case's recall" regression.
4. **embedRecallK 20→30**: wider vector recall. This is the **necessary condition** for zero recall regression at wv=1.5 — embedK=20+wv=1.5 drops one case, embedK=30 restores it.
5. **Kind noise demotion: REJECTED**. The BookStore case (top-10 crowded by 6 `<environment_context>` logs) looks like a noise problem, but the library-wide tally: **52 of 86 relevant labels are kind=log, 47/70 cases have a log-kind relevant note, and 37 relevant notes are themselves environment_context logs**. The "noise" and the "answer" are the same kind; demoting log necessarily demotes the answers too. Measured log demotion (any factor 0.1–0.7): **recall 0.986→0.736, 19 regressions, ndcg −0.365**. There is no clean kind lever; this route is closed.

## Evidence (70 cases, offline sweep + split-half validation)

| Config | recall@10 | ndcg@10 | Δndcg | recall regressions |
|---|---|---|---|---|
| baseline k=60 / embedK=20 / wv=1.0 | 0.9857 | 0.8097 | — | — |
| k=10 only | 0.9857 | 0.8135 | +0.004 | 0 |
| wv=1.5 only (k=60 / embedK=20) | 0.9786 | 0.8515 | +0.042 | **1** |
| **k=10 / embedK=30 / wv=1.5** | **0.9857** | **0.8710** | **+0.061** | **0** |
| kind=log demotion 0.5 | 0.7357 | 0.4443 | −0.365 | **19** |

Chosen config **k=10 / embedK=30 / wv=1.5 / wt=1.0**:

- **Zero recall regression**, ndcg +0.061 (well above the 0.02 threshold); 18 cases improve in ndcg, only 2 dip slightly (the Vercel-deployment case 0.631→0.500, one temporal-literal case 1.000→0.993).
- **Split-half robust**: the four independent splits (first 35 / last 35 / odd 35 / even 35) show ndcg gains of +0.071 / +0.051 / +0.073 / +0.050 respectively, all with zero recall regression — the gain is broad and stable, not overfitting to a single case.
- Per-class: knowledge 0.775→0.836, memory 0.687→0.877, untagged 0.792→0.858; temporal unchanged (saturated).
- **Production-path validation (new-binary probe, intact 149-vector snapshot)**: recall@10=0.9857 (matches the offline prediction exactly, zero regression vs the untuned baseline), ndcg@10=0.8728 (baseline 0.8078, +0.065; 20 cases up, 2 slight dips), no 429. **Gate-metric note**: this change is recall-neutral / ndcg-positive, so abeval's recall-gain gate (-min-recall) does not apply — a recall gain of 0 would misjudge FAIL; the correct evidence is the probe's ndcg measurement + zero recall regression + split-half robustness. The official abeval gate's full rebuild hit the provider's 429 quota this run (the new error contract surfaced it faithfully) and is not used as the criterion for this change.

## Costs (accepted)

- **Fusion behavior changes for every query**: this is no longer a pure experiment but a production ranking change.
- **Tuning on the 70-case eval and validating on the same set carries a circular-validation risk**. Mitigation: split-half robustness + the hard zero-recall-regression constraint + the requirement that the gain be broad (no chasing individual legacy cases). A true generalization proof needs newly labeled held-out cases — listed under reversal triggers.
- **2 per-case ndcg dips** accepted (18 up, 2 down, macro strongly positive).
- **The weights are embedding-model-dependent**: wv=1.5 was tuned for the current OpenRouter qwen3-embedding-8b; changing the model requires re-tuning.

## Reversal triggers

- The gain fails to generalize on newly labeled held-out cases → revert to k=60 / wv=1.0 / embedK=20.
- Changing the embedding endpoint / model → re-run the parameter sweep (weights and k are model-dependent).
- A real correction case of "an over-weighted vector arm crowding out an authoritative lexical hit" → lower wv or return to equal weights.
- Rerank model (the second half of ADR-0065 option b): this experiment shows fusion tuning still has +0.061 of headroom; whether a reranker is worth its endpoint dependency is decided separately.

## Landing record (2026-11-23)

- Code: `internal/search/hybrid/hybrid.go` (`WeightedRankList` + `FuseRRFWeighted`, `FuseRRF` refactored to the equal-weight special case), `internal/service/search_semantic.go` (`noteRRFK=10` / `vectorFuseWeight=1.5` / `embedRecallK=30`, `fuseRankList` gains a weight parameter), `internal/service/search_temporal.go` (the temporal arm passes weight 1.0).
- Tests: +3 in the hybrid package (weight scales contributions, weight≤0 treated as 1, FuseRRF equals unit-weighted); `go build` / `go vet` / `go test ./...` green across 52 packages; gofmt clean.
- Experiment tooling (gitignored): `.annotation-prep/fusesweep/capture` (captures the three arms' raw lists), `sweep.py` (local parameter sweep + split-half + kind demotion).
- The kind-demotion rejection and the RRF-tuning evidence are in the same-day process-log section.

## Addendum: rerank probe round 1 — OpenRouter weak-model LLM rerank, negative result (2026-11-23)

- **Context**: the user directed the probe to become backend-configurable instead of hard-depending on a SiliconFlow key. `.annotation-prep/rerank/probe.py` (gitignored) supports `-backend openrouter|siliconflow`, `-model`, `-reasoning`. Round 1 reused the live `ai_chat_*` config (OpenRouter `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free`, zero new keys) for LLM listwise reranking of the top-40 candidates from the tuned-fusion replay. agnes was checked: a closed platform provider with no exposed base URL/key — not borrowable.
- **Instrument calibration**: with reasoning on, this nano model burned 16k tokens / 170s on a 40-candidate prompt without converging (finish=length, empty content); with reasoning off it returns a valid top-10 in ~3s, so the default is `-reasoning off`.
- **Numbers** (70 cases; baseline = tuned-fusion replay on the same candidate pool, recall@10=0.9857 / ndcg@10=0.8710, matching the offline sweep replication): after rerank **recall@10=0.6857 / ndcg@10=0.569 (−0.302)**; 22 per-case recall regressions, ndcg 9 up / 30 down; 10 hard failures (empty ranking, honestly fallen back to baseline order and counted), 2 recovered on retry. Per class: untagged (51 cases) −0.376, temporal-relative −0.500; only temporal-natural +0.369.
- **Gate verdict: FAIL** (the zero-recall-regression hard constraint is broken). Negative result recorded per ADR-0018: **this backend must not ship to production**.
- **Boundary (important)**: the instrument is weak — a free nano LLM is not a cross-encoder. This negative closes only "OpenRouter weak-model LLM rerank shows no gain"; it **does not refute the rerank ceiling** (theoretical headroom +0.058 ndcg, all 7 fixable cases inside top-10). The SiliconFlow `BAAI/bge-reranker-v2-m3` native probe remains an optional instrument (`-backend siliconflow`, needs a key); closing the rerank line altogether awaits the user's call.
- Per-case data: `.annotation-prep/rerank/rerank-openrouter.jsonl` (gitignored, includes a summary line).
