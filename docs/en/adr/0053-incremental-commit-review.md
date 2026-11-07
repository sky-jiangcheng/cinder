# ADR-0053: Incremental review after commits — rules sift candidates, the model only confirms them

Status: **Proposed (2026-11-08, design draft, awaiting the user's call before any code)**
Date: 2026-11-08
Related: [ADR-0016](0016-async-batch-jobs.md) (reuses "submit returns, progress is queryable"), [ADR-0030](0030-ai-call-audit.md) (every model call goes through the audit log), [ADR-0051](0051-conversation-as-interface.md) (the conversation is the interface: findings must be askable, not another UI language), [ADR-0048](0048-core-contract-version.md) (new MCP tools bump the contract version)
Starting schema: v29 → **v30** (two tables); `CoreVersion` 1.1.0 → **1.2.0** (two tools)

## Background

The user asked whether Cinder, which already indexes collected projects and their commits, could look at yesterday's commits once a day and list the things that break in production while compiling fine. The example is a cross-project Feign contract: project A integrates with B, A's Feign method is missing `@RequestBody`, it compiles, integration tests pass, and the parameter is lost only in production.

Two hard constraints sit between that request and "let a model read the diff and write a review", and both were measured first:

**1. Volume rules out sending everything to a model.** The collected projects on this machine (20, including CBiPay with 11 repos, CBiUnion with 14, lushan with 13) show 41–94 commits, 350–1035 files and 14k–47k diff lines per day. At roughly four characters per token that is over 100k input tokens a day. Cost is the lesser problem; **irreproducibility** is the real one — the same commit reported three findings today and two tomorrow is a feature nobody keeps reading.

**2. A model reading the diff cannot catch this example.** The line `X foo(Req req);` in project A looks harmless in a diff. The mismatch only exists because of `@RequestBody` on B's side, and B is not in that day's diff. Left to itself the model can only guess that a parameter "looks like it should have an annotation".

**3. Rules do find things, but the false-positive rate decides whether the feature lives.** Measured on the `cbi-operation-common` feign directory: 55 Feign files, 113 POST/PUT methods with parameters, and **38 whose parameters carry no `@RequestBody`** — all of them real Spring Cloud (`org.springframework.cloud.openfeign.FeignClient`, not an in-house wrapper). Those 38 **cannot be pushed at a person as-is**: some declare `consumes=application/json` and may work by accident, some are dead interfaces with no callers, and some providers really do take a single field via `@RequestParam`. **Three false positives and the feature gets switched off.**

The conclusion: decide with deterministic rules, let the model only confirm, and report only what this change introduced.

## Decision

### 1. Incremental only, and "incremental" means blast radius, not diff lines

Report only what yesterday's commits (00:00–24:00) introduced; historical debt never enters the daily report. But the definition has to be one notch wider than "look at the changed lines":

| Definition | How it decides | What it misses |
|---|---|---|
| A: changed lines only | does a changed line contain the pattern | "one side changed, the other didn't" — the provider adds a parameter, the consumer file never changed, production returns 400 tomorrow |
| **B: blast radius (adopted)** | the **files/symbols** the diff touched are the entry point; look up every reference in the library and report any reference whose contract no longer matches | covers both directions, still reports only what this change caused |
| C: full audit | scan every collected project daily | drowns in false positives and duplicates A; kept as a manual `--full` only |

The cost is explicit: **historical debt never appears in the daily report**; clearing it means running `--full` by hand. That is a deliberate trade, not an oversight.

### 2. Two layers: rules sift candidates, the model only confirms

```
collected projects → yesterday's commits → hunks → rules → candidates (a dozen) → model → report
                                              ↑ free, reproducible        ↑ only when candidates > 0
```

The rule layer is a **pure function**: input `(path, file content, set of changed lines)`, output `[]Finding`. No database, no network, one fixture test per rule. The model layer only answers "does this candidate hold + one-line cause + how to fix" as structured JSON; with zero candidates **it is never called at all** (asserted with a fake server's call count).

### 3. First rule set (Java first, because the reviewed repos are Java)

| id | Decision | Severity | Known false-positive edge |
|---|---|---|---|
| `feign_missing_body` | in a `@FeignClient` interface, a `@Post/@Put/@PatchMapping` method has parameters, **none of them annotated**, and at least one is not a simple type | single param medium / several high | with `consumes=application/json` some Spring Cloud versions work by accident; the provider may take one field via `@RequestParam` → model confirmation downgrades it |
| `feign_get_body` | a `@Get/@DeleteMapping` method has a `@RequestBody` parameter | high | none — gateways and proxies drop the body; this one is deterministic |
| `route_renamed` | the diff changed a path literal in `@RequestMapping` (class or method) and something in the library still references the old path | high | the old path may only appear in comments/docs → only count `.java` references |
| `feign_arity` | the diff changed a Feign method's parameter count/type and the provider's same-path method has a different count | medium | when the provider is not local, mark `unverified` rather than guess |
| `swallow_exception` | an empty `catch`, a `printStackTrace()` only, or `log.error` followed by returning success | medium | intentional ignores in idempotent cleanup → the model can dismiss |
| `tx_self_call` | a same-class `this.foo()` call to a `@Transactional` method | high | none — the Spring proxy does not intercept self-invocation |
| `sql_injection` | an added line concatenates SQL, or MyBatis `${}` | high | dynamic table/sort names are legitimate → medium |
| `sensitive_log` | a log statement concatenates `idCard｜phone｜mobile｜cardNo｜password｜token` variables | medium | off by default; reported only when the user turns it on |
| `shared_sdf` | a `static SimpleDateFormat` | medium | older code that only calls `format()` is also caught → report only on added lines |
| `remote_in_loop` | a Feign/Dubbo call inside `for/forEach/while` | medium | the loop may be tiny → attribute as "possible N+1", not "certain" |

Go and TS rules are **out of scope here**: let the Java set prove its precision on real repositories first; language coverage is phase 4.

### 4. Two tables; idempotence comes from a unique key

```sql
CREATE TABLE review_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_date DATE NOT NULL,          -- the day being reviewed (yesterday)
  scope TEXT NOT NULL,             -- 'collected' | 'project:3,7'
  status TEXT NOT NULL,            -- running | done | failed
  started_at DATETIME NOT NULL,
  finished_at DATETIME,
  repos INTEGER, commits INTEGER, files INTEGER,
  candidates INTEGER, findings INTEGER, skipped INTEGER,
  ai_calls INTEGER,                -- enough to prove "zero candidates, zero spend"
  error TEXT,
  UNIQUE(run_date, scope)
);

CREATE TABLE review_findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id INTEGER NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
  repo_path TEXT NOT NULL, commit_sha TEXT, file_path TEXT NOT NULL, line INTEGER,
  rule TEXT NOT NULL, severity TEXT NOT NULL,
  verdict TEXT NOT NULL,           -- confirmed | dismissed | unverified
  evidence TEXT NOT NULL,          -- the matched added line plus context
  message TEXT,                    -- rule explanation or the model's cause
  suggestion TEXT
);
```

`UNIQUE(run_date, scope)` turns a re-run into "delete this run's findings, insert again" with no extra lock; `status='failed'` allows a same-day retry, `done` refuses a repeat unless `--force`. **The table is the single source of truth for the report; nothing is written into notes** — see decision 6.

### 5. Scheduling: config in `app_config`, triggering by ticker or launchd, no cron parser

| Key | Default | Meaning |
|---|---|---|
| `review.enabled` | `0` | off by default: it reads source and may send it to a model, which needs an explicit opt-in |
| `review.at` | `09:30` | when yesterday's report runs |
| `review.scope` | `collected` | `collected` or `project:3,7` |
| `review.ai` | `1` | off means rules only, nothing leaves the machine |
| `review.notify` | `high` | `high` / `all` / `off` |
| `review.max_diff_lines` | `2000` | per-file diff above this is skipped and counted in `skipped` |

Desktop: a ten-minute ticker asks "is it past `review.at` today and is there no `done` row for today", and runs once if so. CLI: `repotory review --since yesterday`, called by launchd or the OS scheduler — **no cron-expression parser** (over-engineering, and behaviour differs across platforms).

### 6. The report is data, not a note

Findings do not go into notes or the wiki. Notes are the human's; mixing machine output into them destroys the meaning of "my notes", and one note per day buries the list. The report stays in `review_findings` with two readers: the desktop review page and the MCP tool `review_latest`. Asking the conversation "what did CR find yesterday" makes the model call the same tool — **one source of truth, two renderings**.

### 7. Delivery surface

| Surface | Content |
|---|---|
| CLI | `repotory review --since yesterday [--dry-run] [--no-ai] [--full] [--json]` |
| MCP | `review_latest {days?, severity?}`, `review_run {date?, dry_run?}` (two new tools → `CoreVersion` 1.2.0) |
| Desktop | a "daily review" section in settings (on/off, time, scope, AI, notification level); a review page grouping findings by file, with the evidence line one click away |
| Notification | only `severity=high AND verdict=confirmed`; silence otherwise |

Nothing is added to the home screen or the dashboard — the panel trim in the ADR-0051 amendment is exactly that lesson.

### What this ADR explicitly does not do

1. **No PR-comment bot.** Cinder is not CI; findings are for a person, they do not block merges.
2. **No scheduled full audit.** `--full` is manual only.
3. **No cron-expression parser.**
4. **No CI gate changes.** Review runs locally and has nothing to do with GitHub Actions.
5. **No attempt to cover every language and framework.** Only explicit contracts (Spring Cloud Feign / Dubbo interface annotations) are recognised.

## Consequences

- **False positives are the only metric that decides whether this survives.** How many of those 38 existing cases are real bugs is currently unknown, which is why decision 2 requires model confirmation and why the first release notifies only on `high AND confirmed`.
- **Incremental misses three classes**: historical debt, a provider that is not local (`unverified`), and oversized diffs (skipped and counted, never silently dropped).
- **Rules expire with frameworks.** Spring upgrades and in-house wrapper changes will retire individual rules; every rule carries a `rule_rev` comment stating the framework behaviour it assumes.
- **One more daily task**: git processes plus a possible model call, run while the machine idles, negligible in cost — but "one more report to read every day" is attention cost, which is why it ships off by default.

## Open questions (decide while implementing)

1. **Grouping for model confirmation**: per file (one call sees all candidates in it) or per candidate (one call each)? The former is cheaper, the latter more precise. Leaning per file, splitting only when a file has more than five candidates.
2. **Can a human edit `verdict`?** If yes, a dismissal must feed back into the rule, otherwise the same false positive returns daily. First release offers a single "dismiss all" action, not per-finding editing.
3. **Trigger from a commit hook?** It interrupts the flow and makes noise immediate; leaning no.
4. **Provider-side index**: `route_renamed` and `feign_arity` currently fall back to grepping the old string library-wide; a `route_decls` table for two-way comparison is phase 4.
5. **Historical-debt entry point**: does `--full` also produce a "debt list" todo? Deferred until someone actually wants it.

## Landing plan (batched; run the gates after each batch)

**Phase 1 — skeleton, no UI**
- `internal/review/`: `diff.go` (hunk extraction), `rules_java.go`, `rule.go` (Finding shape and severity)
- `internal/db/review.go` plus migration v30
- `internal/service/review.go`: collect → rules → persist (AI off)
- a `review` subcommand
- gates: one fixture test per rule, idempotence test, skip-counting test

**Phase 2 — model confirmation**
- `service/review_ai.go`: per-file grouping, structured output, `ai_calls` counting; routed through the ADR-0030 audit
- gates: with zero candidates, the fake server's call count is 0

**Phase 3 — delivery**
- `cmd/mcp/tools_review.go` with both tools, `CoreVersion` 1.2.0, tool-list test
- desktop settings section and review page (Chinese and English together)
- notification: high + confirmed only

**Phase 4 — extension (on demand)**
- Go / TS rule packs
- `route_decls` index and two-way contract comparison

## Acceptance (all testable)

1. Run yesterday against the real local library: `done`, `skipped` counted, and a report a human can read (five or fewer high-severity items).
2. Hand-label twenty candidates; precision must reach 0.7 before notifications are allowed. Below that, fix rules rather than add rules.
3. Run the same day twice: the second run refuses or overwrites, and `review_findings` does not double.
4. With `review.ai=0` and with zero candidates, `ai_calls` is 0 in both.
5. `go test ./...`, `npm test`, `check-i18n-keys`, `check-docs-mirror`, `check-version-drift` all green.
