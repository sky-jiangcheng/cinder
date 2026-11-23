# ADR-0030: AI call auditing — making "who sent what out there" a checkable fact

Status: **Accepted (implemented 2026-10-25)** — schema v28 adds the `ai_call_log` table with append-only guards; the review hook sits in front of the three HTTP primitives in `internal/service/ai.go`; every configuration key defaults to off
Date: 2026-10-25
Related: [ADR-0002](0002-c-end-repositioning.md) (C-end repositioning — this ADR is an **additional capability** of that positioning, not a change to it), [ADR-0006](0006-scope-freeze.md) (scope freeze — this ADR explicitly declares that it does not expand the product boundary), [ADR-0021](0021-temporal-graph-page-versions.md) (the append-only audit pattern — `ai_call_log` copies its "single writer + tamper guards + no foreign key" structure), [ADR-0029](0029-in-app-update-download.md) (the contemporaneous trust-surface argument style)
Starting schema: v27 → v28 (this ADR carries one schema change)
The owner's original request (2026-10-25): "could our app do AI content gating and review… if every outbound AI call in the company went through Repotory, then AI usage traces would already be controllable; and if we added keyword blocking and control, wouldn't that give us group-level AI usage gating?"

## Background

A natural idea: if AI call traces all pass through Repotory, then content gating and keyword blocking at the exit would yield "controllable AI usage".

Let the verifiable parts be verified first (these are the factual basis of this ADR, all taken from the code):

**1. The LLM exit is highly converged.** The whole repository has exactly three HTTP primitives that talk to an external model:

- `internal/service/ai.go:244` `aiGet` — `GET /models` probing
- `internal/service/ai.go:270` `aiPostJSON` — non-streaming `POST /chat/completions`
- `internal/service/ai.go:305` `aiPostJSONStream` — streaming `POST /chat/completions`

All eight business paths funnel through `testAIChatWithTimeout` / `streamChatWithTimeout` into these three primitives: `AskAI` (`ai.go:578`), `AskAIWithEvidence` (`wiki_evidence.go:592`), `AskAIWithEvidenceStream` (`wiki_evidence.go:621`), `askCompilePlan` (`wiki_compile.go:210`), `askMergeBatch` (`wiki_merge.go:613`), `askCommunitySummary` (`wiki_community.go:446`), `runWikiLintLLM` (`wiki_lint.go:430`), `OrganizeNote` (`note_organize.go:60`). **There is no bypass** — which means one gate placed at the primitives covers all LLM traffic.

**2. `internal/service` is the one business layer all three front ends depend on.** The desktop app (`main.go`), the headless server (`cmd/server/main.go`) and the MCP server (`cmd/mcp/main.go`) all go through it, so a single policy implementation naturally serves all three shapes.

**3. But the premise of "controllable traces" does not hold yet.** Prompts and responses **are never persisted** (`compile_jobs` records counts, not content), and there is no audit / activity / call_log table at all. More fundamentally there is **no identity**: `internal/httpapi/server.go:10` states plainly *"this API has NO authentication"*, `cmd/server/main.go:117` hardcodes the listen address to `127.0.0.1`, and the database has no user or tenant table. So even if we recorded "at some moment someone called some model with this content", we would not know **who**.

**4. "Group-level" is not something Repotory decides.** Covering a whole company's AI calls requires traffic to go through Repotory **by mandate** — that is the job of the network layer and an identity system, not something a local application can guarantee.

## Decision

**1. This is a security plugin — not a product upgrade, and not a separate product.**

Three shapes compared:

| Shape | Approach | Effect on the existing value anchor | Verdict |
|---|---|---|---|
| Product upgrade | Repotory becomes "the company AI gateway" | **the anchor is overwritten** | rejected |
| **Security plugin (this decision)** | the memory layer stays the protagonist; compliance is a switchable extra | **the anchor is unchanged** | **adopted** |
| Separate gateway product | a new project for the group-wide AI exit | a clear anchor but an effort wildly out of proportion to Repotory's size | rejected (and by the time it came to that, the shared code would approach zero — it should not live in this repository) |

The reason for rejecting "product upgrade" is a clash of temperament, not engineering effort. The README positions Repotory as *"The local-first memory layer for AI coding agents"* / *"Local-first, no cloud, no account. One SQLite file on your disk."*. The inner demand of "group compliance gating" is the **opposite** — it wants identity, central policy, and the ability to govern others. **A private asset is worth more the more private it is; a public facility is worth more the more centralized it is.** Merging them leaves private users feeling watched and enterprise users feeling under-governed.

The reason for rejecting "separate gateway product" is a capability gap: `cmd/server` today is a "local agent", not a "central service" (`docker-compose.yml:5` states *"The API has no authentication and must not be exposed to the public internet directly; put it behind an authenticating proxy"*), and SQLite is a single-writer embedded database (`internal/db/db.go:23` `SetMaxOpenConns(1)`). A group gateway needs an identity system, multi-tenancy, a shared database, central policy distribution and audit aggregation — that is **another product's worth of work**, not another module's worth.

**2. Three layers must be kept apart: memory layer / AI audit / a possible future gateway.**

- **Memory layer** (the protagonist): locally private, knowledge shared across agents, admitted only after human review. **This ADR changes none of its promises.**
- **AI audit** (this ADR): a **default-off** additional capability, sitting between knowledge and model.
- **Gateway** (the future, if ever): something centralized that needs identity. **This ADR explicitly does not promise it** and reserves no implementation for it.

The test is recorded here for later self-checking: **if a new capability makes the definition of "memory layer" fail, it must become a separate product.** By that test the audit plugin passes (it only adds an optional gate to an exit that already exists), while "Repotory becomes the company gateway" does not.

**3. Hard constraints of the plugin shape: the "locally private" promise must not break.**

Concretized into four rules:

- **Everything defaults to off.** The three configuration keys (audit on/off, input review, output review) all default to off. With them off, behavior is **byte-for-byte identical to today** — existing users notice nothing.
- **No accounts are introduced.** The single-machine build needs no identity at all. Identity is an extension point of the deployed variant, not an implementation item of this ADR.
- **Audit records store hashes and metadata by default, never full text.** Writing prompt text into the local database would pollute the knowledge base and pull secrets into the database, which runs against "locally private". By default what is recorded is the evidence of *what happened* (time, endpoint, model, call surface, prompt hash, token count, verdict), not the full text of *what was said*. **Storing full text is an option that must be turned on explicitly, and turning it on must state plainly that it writes into the knowledge base.**
- **No new outbound network exit.** Audit data lands in the local SQLite only and is never sent anywhere.

**4. Review happens in front of the three HTTP primitives, and the streaming path needs separate treatment.**

- Input review runs **before** `aiPostJSON` / `aiPostJSONStream` construct the request; a hit blocks it and no request goes out.
- Output review does **incremental** checking at the SSE parsing point: `parseSSEStream` (`ai.go:351`) is a pure function that hands each chunk to `onDelta` as it is decoded, so the check can be made at chunk boundaries — but **a chunk already delivered to the frontend cannot be taken back**. That is the inherent cost of streaming review, and the implementation must treat it honestly (see "Implementation corrections").
- `aiGet` (`/models` probing) carries no prompt, so it gets a URL policy check only, not content review.

**5. The audit table is append-only, and copies ADR-0021's structure.**

`ai_call_log` reuses the three structural decisions already argued for `page_versions`: **a single writer + append-only trigger guards + no foreign key**. The reason for the missing foreign key is spelled out in ADR-0021 and applies just as well here: SQLite fires row triggers for foreign-key CASCADE actions, so a CASCADE foreign key on an append-only table makes "delete the thing that was recorded" fail outright — and an audit row that vanishes along with its subject is not an audit row.

**6. Clear the boundary with the skill mechanism.**

The two mechanisms point in opposite directions, so they must be described in one document or their responsibilities will inevitably overlap later:

- **The skill mechanism** (ADR-0007 session memory protocol + `skill/repotory`) = the entry side, where an agent actively **reads** knowledge.
- **AI audit** (this ADR) = the exit side, controlling data **leaving** the machine.

They are complementary: **the memory layer encourages agents to read more; the audit layer guarantees that what was read does not leak out.** Together they cover exactly the gap current AI coding tools lack — memory across sessions without leakage.

**7. Keyword blocking is explicitly a weak control, not the headline feature.**

Keyword and pattern matching is simple to build and fragile in effect: bypass cost is very low (rewording, splitting, encoding), false positives are expensive (an internal system name or a normal business term being blocked immediately breeds the urge to work around it), and above all it **does not stop the real leak paths** — the actual risk is a prompt carrying customer data, source code or keys out, and those often contain no keyword at all.

So this ADR positions the feature as **"self-attestation plus early warning"**, not "leak prevention". Real enforcement needs a deployed server and identity attribution, which belongs to the future gateway.

## Option matrix (what shape should this capability take)

| Option | Value | Main risk | Verdict |
|---|---|---|---|
| **Security plugin, default off** (this decision) | the anchor is unchanged; standalone value; existing users notice nothing | the single-machine build cannot attest identity — a stated boundary, not a bug to fix | **adopted** |
| Product upgrade into "the company AI gateway" | theoretically the widest coverage | the anchor is overwritten; temperament clash; existing users are driven off | rejected |
| Separate gateway product | complete group-level capability | effort out of proportion; shared code approaches zero | rejected |
| Do nothing at all | zero risk | giving up the one naturally converged chokepoint; AI calls stay a black box | rejected |

## Consequences

**Positive**:

- AI calls stop being a black box and become a locally checkable fact — with no central service to connect to.
- The convergent shape of the three primitives is cashed in: the gate is placed once and covers all eight business paths and all three runtime shapes.
- The positioning boundary is pinned down in a document, so that a future proposal to "turn Repotory into the company gateway" has a stated criterion to argue against.

**Negative (not glossed over)**:

- **Audit proof power is limited on a single machine.** Without identity, "who called" can only be answered from the local vantage point. This is a boundary this ADR explicitly accepts, not a bug awaiting a fix.
- Keyword review errs in both directions, and the user may feel "it blocked the wrong thing". Defaulting to off is precisely so that the user turns it on only once the rules look right to them.
- Streaming output review cannot take back a chunk already delivered, so it can **only truncate what follows**.
- Enabling full-text auditing writes AI interaction content into the knowledge base, mixed in with notes and pages — a cost this ADR explicitly requires to be surfaced to the user at the moment it is turned on.

## Implementation corrections (facts that differed from the design)

1. **The writer of `ai_call_log` is the service layer, not a trigger.** ADR-0021 used a trigger for `page_versions` because "every write path funnels through internal/db"; but the trigger point for AI call auditing is the HTTP primitives in `internal/service/ai.go` — not in the db layer, where a trigger could not be hung. So the service layer writes the append-only table explicitly here, and append-only is still guaranteed by **BEFORE UPDATE / BEFORE DELETE triggers on the table**. The difference: the writer is not a trigger, but the fact of "one single primitive" guarantees it — all three business paths go through `aiPostJSON` / `aiPostJSONStream` / `aiGet`, and there is no second road.
2. **The prompt is hashed with SHA-256 rather than summarized.** A text summary would need the original text stored in order to compute it, which is storing the full text all over again. The hash answers "is this the same prompt" but not "what was in it" — exactly what the default tier wants.
3. **The call surface is passed in explicitly through context.** The primitives themselves do not know whether the call came from MCP, HTTP or the desktop, so each surface sets it before entering the primitive. The default is `unknown` rather than a guess.
4. **Streaming output review judges at chunk boundaries; a hit stops further delivery** (`onDelta` is not called again) and marks the already-delivered portion as incomplete — because at that point it can no longer be taken back.

## Addendum (2026-11-23, ADR-0068)

As of [ADR-0068](0068-builtin-credential-pii-presets.md), the "no built-in blocklist" stance narrows to "no built-in **KEYWORD** blocklist". For structured credentials (API key shapes, private key blocks, connection strings with passwords, ID/card numbers) the format itself is the signal and regexes can be precise — that class of target now ships as a **default-off** preset library (`ai_review_presets`) appended after the user's rules, with optional entry-side masking before storage (`memory_redact`). Every promise of this ADR is inherited verbatim: four default-off gates, hash-only trail, append-only, user rules first; presets never replace user rules.
