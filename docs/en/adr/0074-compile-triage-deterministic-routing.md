# ADR-0074: Pre-compile triage — structural signals instead of a small model, against draft cost and the 40% rejection rate

- Status: **Accepted (2026-11-24, the owner chose ADR-0065 gap 2.6 "do the small-model routing first"; the landed shape is deterministic rules — see Decision 1 for why)**
- Date: 2026-11-24
- Related: [ADR-0065](0065-hindsight-absorb-not-replace.md) (gap 2.6 candidate), [ADR-0015](0015-w3-minimal-compile-loop.md) (compile writes pending only / no scheduler — untouched here), [ADR-0071](0071-fusion-tuning-weighted-rrf.md) (the kind-demotion rejection), `docs/product-review/2026-11-23-hindsight-experience-gap.md`

## Context

Hindsight routes ingest through a 0.8B decision model, sending only "changed/new" material to the large model for drafting — about 40% fewer calls. Cinder's wiki layer has the same root problem: on 2026-10-20, 10 of 12 pending pages were rejected at human review, almost all session-log restatements, import wrappers and bare source attributions; 18 of 45 pages were rejected (40%). The "output [] for pure logs" line in the compile prompt never fixed it — **the model was already called, the money already spent**; emitting `[]` only avoided writing a page.

On 2026-11-24 the owner chose to "do the small-model routing first" to fix cost and the rejection rate. This ADR records the landed shape and its reasoning.

## Facts (read-only live-library check, 2026-11-24)

- 162 notes: `log` 94 / `knowledge` 67 / empty 1. **Transcripts are 58%.**
- Only **1 of 94** log notes carries an explicit decision marker; titles cluster tightly around `<environment_context> · <id>`, `# AGENTS.md instructions for ...` and `Session Handoff`.
- The prompt's soft rule cannot save a call, and the model does not always honour it.

## Decisions

1. **No second model — deterministic rules instead.** Cinder has one AI endpoint (ADR-0065's landing record settled "local models are weak; use a compliant remote API"). A local small model breaks zero-CGO single-file distribution; using the same remote endpoint for classification adds one call per note — doubling cost for notes that pass and saving little net for notes that are refused. **Structural signals are already enough**: 58% of live transcripts share one `kind` and title shape; rule checks spend zero tokens, add zero latency, need no endpoint, and are unit-testable. The owner's goal was "routing to fix cost and rejection rate"; the small model is Hindsight's mechanism, not the goal.
2. **Two signals, both overridable by a decision marker**: (a) the title starts with `<environment_context>` / `agents.md instructions for` / `session handoff`; (b) `kind=log`. Either skips; an explicit decision/rule marker in the title or body (决策: / 决定: / 规则: / decision: / convention: / 架构决策 / 设计决策, etc.) keeps it — the one log with real content is not lost. **No body-length rule**: ADR-0071 already measured that kind/length heuristics demote genuine answers with the noise; this round does not repeat it.
3. **Triage runs before truncation** (before `CompileNoteTargets`): if the newest 20 notes are all transcripts, truncating first would burn the whole budget on them. The synchronous `CompileProjectNotes`, asynchronous `runCompileJob` and single-note `CompileNote` share one predicate and one switch — a note cannot bypass routing by choosing another entry point.
4. **On by default, kill switch `wiki_compile_triage=0`**: unlike other model gates (off by default because sending text out needs authorization, ADR-0012), routing sends no text, writes nothing and only saves a call — the safe state is the useful one. No new settings control (`wiki_compile` has no UI switch either); disable via the existing `UpdateConfig('wiki_compile_triage','0')`.
5. **Skips must be visible**: the skipped count goes into `Stopped` (async job) or `LLMNote` (sync report); a single-note compile gives the specific reason — no silent partial success, following ADR-0015's visibility discipline.
6. **No write semantics change**: triage only decides "which note is worth one model call", never what a page says or whether it is approved. The human gate (ADR-0015), ADR-0073's optional auto-approval, schema and the MCP tool surface are all unchanged.

## Cost

- A `kind=log` note that looks nothing like a transcript, has no decision marker, but genuinely holds knowledge will be skipped (none seen in the live library, but theoretically possible). Recovery is a one-off human edit — change `kind` or add an explicit decision marker — cheaper than disabling routing wholesale.
- Decision markers are a keyword list: easy to circumvent ("we settled on X" without "decision: X"), but they only **rescue** logs, never admit other content, so mistakes err conservative.
- The signals are tied to importer title/`kind` conventions; a changed importer shape can let transcripts through. See reversal triggers.

## Verification

- Nine pure-function rule cases, including two anti-false-positive assertions ("a log with a decision is kept", "a short but real note is kept").
- Seam-level assertion: compiling a transcript via `CompileNote` gives **`chat.calls == 0`** — the cost benefit is proved on the data path, not inferred from report text.
- Kill switch assertions: absent / empty / garbage all mean ON, explicit `0` means OFF; with it off the same note makes 1 call again.
- Full `go test ./...`, `go vet`, frontend tsc/vitest/build: see the release record.

## Reversal triggers

- Consistent reports of "log but real knowledge" being skipped → extend the decision markers, or turn that class into an explicit rule via a manual `kind` change; do not revert to "send everything".
- Importer title/`kind` changes that let transcripts through → update `transcriptTitleMarks` in the same batch as the importer, with a real-sample assertion.
- If Cinder ever ships a zero-CGO local small model → `CompileTriage` may become an optional model predicate, but the deterministic rules stay as the default fallback and cost floor.
