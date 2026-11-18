# 0064 Splitting the `ai` cluster — transport sinks to `internal/aiclient`, the tool loop and orchestration stay in `Service`

- Status: **Accepted (drafted, reviewed and landed the same day, 2026-11-18)** — the first cut is implemented and verified; measurements are in "Landing record"
- Related: [ADR-0060](0060-service-domain-split.md) (strangler extraction by domain; closing rule "move only what has a small, stable dependency surface", which named `ai` as the next compression target), [ADR-0055](0055-agent-surface-contract-and-observability.md) (tool-surface contract and observability, untouched here)

## Background

ADR-0060's closing note judged `evidence` to be orchestration and kept it in `Service`, then pointed at the next step: compress the `ai` cluster. Measured this round:

| File | Lines | Contents |
|---|---|---|
| `internal/service/ai.go` | 1,042 | AI config, HTTP primitives, SSE, provider probing, the Ask orchestration |
| `internal/service/aitools.go` | 1,073 | tool registry, tool implementations, two tool-calling loops |

Those 2,100 lines are not homogeneous. Sort them by "does it reference `Service`" before deciding what to cut.

## Fact one: the transport half is s-free, but not "seven types"

Counting by whether the function body references `s.`, **20 functions / 611 lines (comments included)** never touch the receiver:

```
aiCandidateURLs / nextAICandidate / aiLocalhostFallback /
aiProxyFunc / aiProxyFuncWith / isLoopbackHost     ← URL and proxy resolution
aiGet / aiPostJSON / aiPostJSONStream / parseSSEStream /
aiNetworkHint                                       ← HTTP primitives and SSE decoding
parseAIModels / ListAIModels / TestAIChat /
TestAIChatWith / TestAIChatWithTimeout /
testAIChatWithTimeout / streamChatWithTimeout       ← provider probing and audited-free direct calls
aiFinishReason / chatResponse.providerError         ← response and error envelope
```

**After reviewing symbol by symbol, the travel list is bigger than the first estimate**:

| Kind | Symbols | Source | Why it must travel too |
|---|---|---|---|
| Types ×10 | `chatMessage`, `chatRequest`, `chatResponse`, `chatErrorEnvelope`, `chatStreamRequest`, `streamDelta`, `finishReasons`, `toolCall`, `AIModelListResult`, `AITestResult` | `ai.go:113/124/134/150/183/199/484/231/237`, `aitools.go:33` | `chatMessage.ToolCalls` is `[]toolCall`, and `toolCall` lives in `aitools.go` — leaving it behind creates an import cycle; the two Result types are the return values of `ListAIModels` / `TestAIChat*` |
| Package-level symbols ×3 | `aiChatTimeout` (90s), `aiProbeTimeout` (15s), `aiHTTPClient` | `ai.go:38/72/324` | the transport functions reference them directly; all five references to `aiHTTPClient` sit inside the transport section |
| Helper ×1 | `truncateBytes` | `handoff_capture.go:235` (`wikigen/seam.go:162` holds another copy) | `providerError` and the 4xx messages truncate the body; `aiclient` carries its own six-line implementation rather than depending back on `service` |

**Not in the slice**: `truncatedHint` is only a wrapper around `wikigen.TruncatedHint`. Moving it would make the transport package depend back on wiki vocabulary, contradicting the dependency-direction record in ADR-0060.

## Fact two: the loop's coupling is not in the loop, it is in the tool implementations (corrected on review, 2026-11-18)

The first draft described the tool loop as "consumes six domains that have not moved yet". Line by line, that claim is inaccurate:

| Loop | Lines | `s.` references |
|---|---|---|
| `askWithTools` | 116 | **one** (`s.toolRegistry.call`) |
| `streamAskWithTools` | 141 | **one** (same) |

Auditing (`recordAICall` / `aiReviewInputText` / `resolveAuditConfig`) happens in `chatWithAudit`, **before** the loop; memory (`BuildLayeredMemory`) likewise; retrieval appears only inside the `toolSearchNotes` **handler** (one reference to `SearchAll`). In other words, four of the six dependencies belong to handlers, not to loop mechanics.

What the loop actually lacks is a boundary it can own, and that boundary is inside `aitools.go` — **a third copy of the transport and a third copy of the wire types**: `aiPostJSONToolStream` + `parseSSEToolStream` (64 lines, s-free) duplicate the job of `aiclient.PostJSONStream` / `ParseSSEStream`, while `toolChatRequest` / `toolChatResponse` / `streamToolDelta` duplicate OpenAI's request/response shapes just to add one `tools` field and the `tool_calls` delta.

So the order is corrected too: **merge the transport and the wire types first (no behaviour change), then move the registry and the loops** — rather than waiting for `search` / `context` / audit to become domains first. Measured as "domains", those three cost:

| Candidate domain | Lines | `Service` field dependencies |
|---|---|---|
| `search.go` | 109 | `db`, `fuseSemantic` (nearly ready) |
| `context_layers.go` | 458 | nine: `db`, `SearchNotes`, six `layer*` helpers, `aiProjectBaseContext` |
| `ai_audit.go` | 309 | seven: `db`, three switches, `auditForwarder`, `auditSurface` |

Each of those is its own cut-sized job. Making them prerequisites for a 257-line loop inverts the benefit/cost ratio.

## Fact three: the transport layer already has a duplicate elsewhere

`internal/httpapi/guard.go:42` carries its own `isLoopbackHost`, semantically identical to `ai.go:375` (the former parses a bare `::1` whole, the latter `SplitHostPort`s first — same result). Once the transport sinks to the `internal/` top level, `httpapi` can reuse it directly and the duplicate should be deleted — **one of this cut's most concrete gains, and the criterion for the package's location**.

## Decision

**Location: `internal/aiclient` (internal top level), not `internal/service/aichat`.**

`reviewrun` / `wikipage` / `wikigen` are business domains; `aiclient` is not — it is an HTTP/SSE client. Under `internal/service/` it would compile without a cycle, but it would force `httpapi` to import a *sub-package of service* merely to reuse the loopback check, which reads as a layering violation. At the top level, `service` and `httpapi` are sibling consumers.

1. **Move in 20 functions + 10 types + 3 package-level symbols + a `truncateBytes` copy**, exported as `CandidateURLs` / `NextCandidate` / `Get` / `PostJSON` / `PostJSONStream` / `ParseSSEStream` / `FinishReason` / `Models` / `TestChat*` / `StreamChatWithTimeout`, with types `Message` / `ToolCall` / `Request` / `Response` / `ErrorEnvelope` / `StreamRequest` / `StreamDelta` / `FinishReasons` / `ModelListResult` / `TestResult`.
2. **`providerError` becomes the exported method `Response.ProviderError()`**. Go does not allow defining methods on an alias of a foreign type, and unexported methods are invisible across packages — so the service side keeps only a free function `providerError(cr chatResponse)` that forwards. **This is the only existing test that has to change** (`ai_provider_error_test.go:56`, one line).
3. **`internal/service` keeps only one-shot scaffolding, torn down the same day**: during the move, ten aliases such as `type chatMessage = aiclient.Message` plus 19 same-name forwarding functions let every call site and every `ai_*_test.go` stay untouched; then `ai_url_test.go` / `ai_stream_test.go` / `ai_provider_error_test.go` (the pure transport part) moved into `aiclient`, the scaffolding was deleted wholesale, and service calls `aiclient.X` directly. The two references to `aiChatTimeout` / `aiProbeTimeout` likewise became `aiclient.ChatTimeout` / `aiclient.ProbeTimeout`.
4. **Config, auditing, the Ask orchestration, the tool loop and `truncatedHint` stay in `Service`**: `aiChatConfig` reads the `ai_*` keys from `s.db`, and `chatWithAudit` / `streamChatWithAudit` write the ADR-0030 audit rows — both are `Service`-shaped; the transport entry points take only `(baseURL, apiKey, model, messages, timeout)`.
5. **Delete the `isLoopbackHost` duplicate in `httpapi/guard.go`** and call `aiclient.IsLoopbackHost` instead, leaving a single definition in the repository.

## Line accounting (corrected)

- `ai.go` loses 611 lines; the service side regains about 70 lines of wrappers + 12 lines of aliases, for a **net reduction of about 530** (the first draft's -600 forgot the wrappers).
- `httpapi/guard.go` loses another 13 duplicated lines; `internal/aiclient` gains about 620.
- These are **file-size and dependency-direction** gains, not API-surface gains: the number of exported service methods does not change (ADR-0060 already recorded the same cost). The real incremental gain is that the transport layer can be reused by packages other than `service` — the loopback duplicate disappears immediately, and SSE parsing becomes reusable later.

## Acceptance criteria

1. `gofmt -l` prints nothing; `go build ./...` / `go vet ./...` / `go test ./...` all green.
2. `go list -deps ./internal/aiclient` contains neither `repotory/internal/service` nor `repotory/internal/db`.
3. **Request-byte equivalence**: before moving, a temporary test captures `method / path / Authorization / Content-Type / body` from the existing implementation into a golden file; after moving, `aiclient` runs the same cases and every field is compared — including the `localhost → 127.0.0.1` retry path.
4. The existing `ai_*` tests pass unchanged, except the one line in `ai_provider_error_test.go`.
5. Only one `isLoopbackHost` definition remains repository-wide; the 22-tool MCP fixture, `CoreVersion`, the binding surface and the schema are all untouched.

## Explicitly out of scope

- Do not move the tool loop, do not move the Ask orchestration, do not introduce a port layer.
- Do not change tool names / parameters / return shapes, do not change `CoreVersion`, do not touch ADR-0055's observability surface.
- Do not move `truncatedHint` or the ADR-0030 audit rows into `aiclient`.

## Landing record (2026-11-18)

Landed as proposed, with exactly one deviation: `testAIChatWithTimeout` is exported in `aiclient` as `ProbeChatWithTimeout` (the service-side name is unchanged); every other symbol maps one-to-one.

| Metric | Measured |
|---|---|
| `internal/service/ai.go` | 1,042 → **358** lines |
| `internal/service/aitools.go` | 1,073 → **1,064** lines (`toolCall` moved out) |
| `internal/service/ai_transport_facade.go` | **95** lines added (10 aliases + 19 wrappers + the providerError free function + 2 consts) |
| `internal/aiclient/aiclient.go` | **715** lines added (comments included) |
| `internal/httpapi/guard.go` | 44 lines, a single `isLoopbackHost` definition (delegating to `aiclient.IsLoopbackHost`) |
| `go list -deps ./internal/aiclient` | no `repotory/internal` dependency at all |

Request-byte equivalence uses the golden captured before the move: `internal/aiclient/testdata/aiclient_golden.json` (method / path / Authorization / Content-Type / body for three requests, the streaming one included); `TestWireShapeMatchesGolden` compares field by field after the move and passes. `ai_provider_error_test.go` changed exactly the expected line, `cr.providerError()` → `providerError(cr)`; `ai_stream_test.go` / `ai_url_test.go` / `aitools_test.go` were green without a single edit.

### Second step: scaffolding teardown (same day)

- The three white-box tests moved into `internal/aiclient` (`url_test.go` / `stream_test.go` / `provider_error_test.go`) with symbols renamed to their exported forms; 13 cases pass.
- The two end-to-end cases in `provider_error_test.go` (`TestAskAISurfacesProviderError`, `TestAskAIKeepsNoChoicesWordingWhenThereIsNoErrorEnvelope`) were **split back into `internal/service`** (`ai_provider_error_e2e_test.go`): they assert `Service.AskAI`'s error wording, and putting them in `aiclient` would drag service back in and close a cycle.
- `internal/service/ai_transport_facade.go` was deleted entirely (95 lines): ten aliases, 19 wrappers, the `providerError` free function and two consts all gone; `ai.go` / `aitools.go` / `ai_audit.go` and the tests call `aiclient.CandidateURLs` / `aiclient.PostJSONStream` / `aiclient.Message` directly.
- The `ListAIModels` / `TestAIChat` bindings in `internal/app` now call `aiclient.ListModels` / `aiclient.TestChat` (App method names and the binding surface are unchanged).

Line counts after the teardown:

| File | Lines |
|---|---|
| `internal/service/ai.go` | **359** |
| `internal/service/aitools.go` | **1,065** |
| `internal/aiclient/aiclient.go` | 715 |
| `internal/aiclient/*_test.go` | 410 |

The two `ai` files went from 2,115 to 1,424 (a net 691 reduction), and no same-name forwarding function for `aiclient` remains inside service.

### A defect fixed while reviewing (2026-11-18)

Both loops were written as `ctx, cancel := context.WithTimeout(context.Background(), timeout)`: that **shadowed the caller's ctx**, and after `cancel()` the same ctx was handed to `s.toolRegistry.call` — so every ctx-aware tool (retrieval included) received an already-cancelled ctx. Only after the previous round's ctx plumbing landed did this latent problem become a real error (the db layer began using `QueryContext`). Both loops now derive a `callCtx` from the caller's ctx for HTTP only, and handlers get the original ctx; `tools_ctx_test.go` adds two cases (the handler sees a live ctx / caller cancellation stops the request from being sent) as the regression gate.

### Second cut (2026-11-18): the tool loops converge

Following the review's conclusion, remove the duplication inside `aitools.go` first, then move the loops:

1. **Transport merged.** `aiclient` gains `ToolDefinition` / `ToolCallDelta`, `Request.Tools`, `Response` choice `ToolCalls`, `StreamDelta.Delta.ToolCalls`; `ParseSSEStream` splits into a `parseSSE(r, onChunk)` core plus a content wrapper plus `ParseSSEToolStream`; `PostJSONToolStream` is added (returning `int, FinishReasons, error`, the same shape as `PostJSONStream`). Service's `aiPostJSONToolStream` / `parseSSEToolStream` / `toolChatRequest` / `toolChatResponse` / `streamToolDelta` / `streamToolChoice` are all deleted.
2. **The loops get their own package.** New `internal/aitools`: `Registry` (`Register` / `Definitions` / `Call`), `Provider{BaseURL,Model,APIKey}`, `MaxRounds = 5`, `Ask` / `AskStream`, inline tool-call parsing (`InlineToolCalls` / `ContentWithoutInlineCalls`). The only seam is a single `*Registry` value — no interface layer invented for handlers.
3. `internal/service/aitools.go` keeps only tool implementations, the registration functions (`registerDashboardTools` / `registerDashboardToolsExtra`) and `QueryProjectStatsBatch`, dropping from 1,065 to 520 lines; `ai.go` goes 359 → 366 (adding `aiChatConfig.provider()`, collapsing the config into the three fields the loop needs).

Equivalence is guaranteed by a request-body golden: `internal/aitools/testdata/tool_loop_golden.json` stores the two request bodies captured before the move, and `internal/service/tools_golden_test.go` replays the same tool round afterwards and compares byte for byte — field order `model → messages → tools` and the `tools` array contents match exactly.

Verification: `gofmt -l` prints nothing, `go build ./...` and `go vet ./...` pass, `go test ./...` is green across 50 packages (including `TestToolLoopRequestBodyGolden`, `TestAskWithToolsHandlersSeeLiveContext`, `TestAskWithToolsRespectsCallerCancellation`).

Left as-is, deliberately: `PostJSONToolStream` still falls back to localhost "on any error" while `PostJSONStream` falls back only on transport errors. That is the pre-move behaviour; unifying the two is a separate decision, recorded in the function's comment.

### Review: the real cost of the `search` cut (2026-11-18, deferred)

The previous version labelled `search.go` "109 lines, depends only on `db` + `fuseSemantic`, nearly ready". By `s.` count it really has a single reference — but **that reference is `fuseSemantic`, and that word drags the entire vector-retrieval chain in**:

| Owner | Lines | Depended on by |
|---|---|---|
| `search.go` (three Search methods + formatting) | 109 | app / httpapi / cmd/mcp / cmd/abeval / cmd/abevaldiag / cmd/searchbench, 12 call sites |
| `search_semantic.go` (`semanticEnabled` / `noteEmbedder` / `vectorStore` + cache / `invalidate` / `RebuildEmbeddings` / `fuseSemantic`) | 240 | `embed_drain.go` (background incremental indexing), `config.go` (cache invalidation on config change), `pageVectorRecall` in `wiki_graph_evidence.go`, four `RebuildEmbeddings` call sites |
| the `vectorStoreCache` field plus its test fixtures | — | `vector_store_cache_test.go` / `embed_drain_test.go` / `page_embed_drain_test.go` read and write `svc.vecStore` directly |

In other words it spans four domains — retrieval, background indexing, page recall, configuration — and touching it means touching **14 test files**. That is not a 109-line displacement but a cross-domain refactor; the benefit today (349 fewer lines in service) does not match the risk, so it is **deferred** until a second domain genuinely needs to share the vector store, at which point the order becomes "make the embedding runtime a package first".

### Third cut (2026-11-18): audit moves to `internal/aiaudit`

Unlike `search`, audit's dependencies are clean — two pieces of process-level state plus one storage handle — so after measuring it landed directly:

- `internal/aiaudit`: `Recorder{db, surface, forwarder}` + `Policy` (`Enabled/ReviewInput/ReviewOutput/Rules`) + pure functions `PromptText/PromptHash/MatchRule/Entry` + `BlockedError` + the `Verdict*` / `Surface*` constants. `Resolve()` reads the four config rows once; `RecordCall()` keeps the original rule that a failed audit write is logged and never interrupts the model call.
- `Service` no longer holds the two loose fields `auditSurface string` and `auditForwarder func`, replacing them with a single `Audit *aiaudit.Recorder` (built in `NewWithDeps`); external call sites move from `svc.SetAuditSurface(...)` to `svc.Audit.SetSurface(...)`.
- Eight files and 55 references: `ai.go` (the two audited entry points), `ai_audit_query.go` (the read side), `writerapi` (write catalogue + forwarder), `main.go` / `cmd/server` / `cmd/mcp` / `cmd/repotory-capture` (surface declaration at startup), and four test files.

Line counts: `internal/service/ai_audit.go` (309 lines) deleted, `internal/aiaudit/aiaudit.go` 333 lines (package comment and the `Recorder`/`New`/`Policy` heads included); `ai.go` 366 → 367. Verification: `go build`, `go vet`, `gofmt -l` clean; 50 packages green.

## Follow-up

The tool loops have landed in the order above; `search` / `context` / audit are no longer their prerequisites but independent, optional cuts. Of the three, `audit` has landed (third cut); `search` measured as a vector chain spanning four domains and is deferred (see above); `context` awaits the same measurement before being scheduled. The transport cut's scaffolding was torn down the same day, leaving no long-term tax of "two packages with two sets of same-name functions".
