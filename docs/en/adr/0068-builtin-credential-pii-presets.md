# ADR-0068: Built-in credential/PII preset library — structured format is the signal, keywords are not

- **Status**: Accepted (2026-11-23)
- **Date**: 2026-11-23
- **Related**: [ADR-0065](0065-hindsight-absorb-not-replace.md) (the Hindsight absorption route, first P1 item), [ADR-0030](0030-ai-call-audit.md) (AI call audit and content review; this ADR revises the scope of its "no built-in blocklist" stance), [ADR-0012](0012-semantic-search.md) (the default-off privacy authorization stance — defaulting on would make the authorization decision for the user)

## Context

ADR-0065's gap list §2.3: Hindsight's Memory Defense ships 45 secrets/PII patterns out of the box and redacts before storing; Cinder's ADR-0030 review mechanism has only user-written regexes and ships zero rules. The evaluation baseline confirmed it too: when a real key shape shows up in a handoff or an import, no layer catches it.

ADR-0030 rejected a built-in blocklist because "keyword matching is a weak control" — that judgment still holds for **keywords**. But it did not distinguish another class of target: **structured credentials**. `AKIA` + 16 uppercase alphanumerics, a `-----BEGIN ... PRIVATE KEY-----` block, a `postgres://user:pass@host` connection string, an 18-digit national ID — for these, **the format itself is the signal**. It survives no semantic rewording, and a regex can be precise with almost no false positives. Folding both classes into one "we ship nothing" stance was stretching a correct rejection past its boundary.

## Decision

1. **A shipped preset library, structured formats only.** `internal/aiaudit/presets.go`: 20 named presets (private key block/header; AWS/GitHub/GitLab/Slack/Anthropic/Stripe/OpenAI-shaped/Google/npm/HuggingFace/SendGrid/Twilio keys; JWT; database connection strings with passwords; secret assignments; national ID, mobile and bank card numbers). **Emails and person names are deliberately excluded**: legitimate emails are dense in development memory (maintainer contacts, git authors), so including them would guarantee false positives — the residual boundary where ADR-0030's keyword rejection still applies inside the structured domain.
2. **Two independent gates, all default-off.** `ai_review_presets=1` **appends** the presets after the user's rules (concatenated inside `Resolve()`; the three review call sites in `ai.go` are untouched). `memory_redact=1` masks matched spans on the **entry side**, replacing them with `[redacted:<name>]` markers before anything reaches the database. Exit review and entry masking are separate switches — some users want auditing without altered writes, others want clean storage without blocked calls. With everything off, behaviour is byte-for-byte what it was before (ADR-0030's promise inherited verbatim).
3. **The entry side covers every machine write path.** The three service creation functions (`CreateNote` / `CreateNoteWithMeta` / `createNoteWithKey`, covering MCP notes, handoff and capture) plus the plugin runtime's `upsertDoc` (the direct-write funnel of all eight importers). The import path redacts **before any lookup**, so both sides of the content-hash skip and the idempotency key compare masked text — the 4-hour sync cannot produce phantom updates because of masking. **Boundary**: existing notes are not retroactively scrubbed, and manual desktop edits are not masked — what a user writes by hand is the user's own responsibility; machine bulk writes are the leak surface.
4. **Markers are idempotent and self-describing.** `[redacted:aws-access-key-id]` matches no preset, so a second pass counts 0; the marker carries the rule name, so a reader knows what class of thing used to be there instead of a black hole. Bounds are validated **after** masking — a marker can be longer than the span it replaces, and the stored text is what must fit the limit.
5. **Compatible with MatchRule's lowercasing.** The exit review path lowercases before matching (the ADR-0030 implementation), so every preset carries `(?i)` and character classes covering both cases, guaranteeing the same rule fires identically on the entry side (original text) and the exit side (lowercased). A test pins this dual-form compatibility.
6. **No tool-surface change.** `repotory_ai_review_rules` only changes its description and result fields (`presets_enabled` / `preset_count` / `redact_enabled`); the tool name set is unchanged (the fixture pins names only) and CoreVersion stays put. `rule_count` means the total active rule count (user + presets); `preset_count` says how many of those come from the library.

## Costs (accepted)

- 20 patterns against Hindsight's 45: the narrower coverage is deliberate — every entry must pass the "does not harm development memory" negative tests (git SHAs, UUIDs, epoch-ms timestamps, ordinary prose and emails must all pass through). Fewer, precise rules beat more, noisy ones.
- Sequential replacement means overlapping spans get only the first matching category (base64 inside a private key block is not re-tagged by a key shape) — the goal is eliminating the leak, not precise classification.
- `secret-assignment` is the broadest entry (the `password = "..."` shape) and may hit example dummy passwords in documentation. It is the one precision/coverage trade that leans toward coverage, because the assignment shape is how humans actually write secrets.
- Each note write costs three extra config reads plus (when enabled) 20 regex replacements; when disabled, only the config reads — microseconds.

## Reversal triggers

- Presets proven in practice to harm legitimate memory (north star: human correction count goes up) → narrow or remove the offending entry, rather than adding a whitelist mechanism.
- A reliable local small-model PII detector arrives (the P3 triage route) → re-evaluate whether the regex library demotes to its fallback.

## Landing record (2026-11-23)

- Code: `internal/aiaudit/presets.go` (20 presets + `PresetRules` / `Redact` / `RedactIfEnabled`), `aiaudit.go` (`Policy.PresetsEnabled` + `Resolve` appending), `internal/service/config.go` (two new keys), `note.go` (`redactForStore` on three paths), `runtime.go` (`upsertDoc` pre-masking), `ai_audit_query.go` (three status fields), `cmd/mcp/tools_audit.go` (description + result fields).
- Tests: 7 in aiaudit (per-preset positives, negative pass-through, marker idempotency, lowercase compatibility, default-off gate, Resolve appends without replacing) + 3 in service (default unchanged, three write paths masked, status reporting); frontend AiAuditSection dual toggles + preset count tests, vitest 431 green, tsc clean.
- Frontend: two new settings toggles (built-in presets / mask before storing) + hint copy + stats row, bilingual locales synced; the "ships no blocklist" copy became "ships no KEYWORD blocklist".
- Docs: ADR-0030 addendum, `features/ai-integration.md` review section revised in both languages.