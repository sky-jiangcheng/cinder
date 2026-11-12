# ADR-0053: Incremental review after commits — rules sift candidates, the model only confirms them

Status: **Accepted (2026-11-08, implemented the same day; deviations in the Implementation record at the end; the acceptance-item-2 labelling result and the five rule fixes in the Addendum (2026-11-09))**
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

Desktop: a ten-minute ticker asks "is it past `review.at` today and is there no `done` row for today", and runs once if so. CLI: `cinder-review --since yesterday`, called by launchd or the OS scheduler — **no cron-expression parser** (over-engineering, and behaviour differs across platforms).

### 6. The report is data, not a note

Findings do not go into notes or the wiki. Notes are the human's; mixing machine output into them destroys the meaning of "my notes", and one note per day buries the list. The report stays in `review_findings` with two readers: the desktop review page and the MCP tool `review_latest`. Asking the conversation "what did CR find yesterday" makes the model call the same tool — **one source of truth, two renderings**.

### 7. Delivery surface

| Surface | Content |
|---|---|
| CLI | `cinder-review [--since yesterday] [--dry-run] [--no-ai] [--force] [--json] [--severity]` |
| MCP | `review_latest {days?, severity?}`, `review_run {date?, dry_run?}` (two new tools → `CoreVersion` 1.2.0) |
| Desktop | a "daily review" section in settings (on/off, time, scope, AI, notification level); a review page grouping findings by file, with the evidence line one click away |
| Notification | only `severity=high AND verdict=confirmed`; silence otherwise. The channel is a **dot on the left rail**, not an OS notification |

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
- `internal/service/review.go`: collect → rules → persist (AI off) (since 2026-11-12 implemented in `internal/service/reviewrun/`; this file is only the facade — see ADR-0060)
- a `review` subcommand
- gates: one fixture test per rule, idempotence test, skip-counting test

**Phase 2 — model confirmation**
- `service/review_ai.go`: per-file grouping, structured output, `ai_calls` counting; routed through the ADR-0030 audit (since 2026-11-12 the grouping and JSON handling live in `internal/service/reviewrun/refine.go`; this file only holds the model call and the audit)
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

## Implementation record (2026-11-08)

Shipped P1 → P3 in one pass: `CoreVersion` 1.2.0, schema v30, MCP 20 → 22 tools.
Three places diverge from the design above.

**① The CLI is its own binary, `cinder-review`, not a subcommand of the main one.** The
design says `repotory review --since yesterday`, but since ADR-0049 the main binary
dispatches no CLI subcommands at all (`cinder` only starts the desktop app); adding one
would squeeze "double-click to launch" and "command-line tool" into a single entry point.
A separate binary also lets launchd or an OS scheduler call it directly instead of going
through `open -a`.

**② "Notification" is a rail dot, not an OS notification.** This repo has no system
notification infrastructure, and every platform's permission prompt asks the user to
decide before they have seen any value. The dot reuses what the update check already does
(`updateDot` in `SideRail`): `high` counts `high AND confirmed`, `all` counts everything,
`off` draws nothing — and **zero or a failed fetch also draws nothing**, because
navigation must not depend on it. The threshold lives in `service.ReviewNotifyCount`
rather than the UI: anyone can forget a rule, the backend cannot.

**③ `--full` was not built.** D7's CLI row lists it while the same ADR's "what this
explicitly does not do" item 2 says "no scheduled full audit" — the two contradict each
other, and the second wins: a full audit is not a switch on incremental review, it is a
different problem (clearing existing debt), to be decided in its own ADR when someone
actually needs it.

Two hard constraints, written into the code comments and repeated here:

- **Zero candidates must mean zero model calls.** `refineWithCandidates` only sends a
  request when there are candidates, and a counting `httptest` handler pins that down.
  The cheaper-looking implementation — "ask the model whether this file has problems" —
  would invite the volume problem from §Background① straight back in.
- **`unverified` does not mean broken.** A candidate a rule fired on but no model
  confirmed may appear in the report; it must not raise the dot and must not be worded as
  "confirmed".

**Still open (out of scope here)**: acceptance item 2 — hand-label twenty candidates,
reach precision 0.7 — was not done. That is why the first version only raises the dot at
the most conservative threshold; do not use `review.notify=all` before the labelling
happens. A note to a future self: rules are not a quantity game. When precision misses,
change the rules, do not add more.

## Addendum (2026-11-09): acceptance item 2 is done, the gate did not pass — five rules were fixed before measuring

The labelling ran. The method: forty days of real commits against a `.backup` copy of the
live database (**the live DB was never touched**), every candidate opened against the real
code. Three parts; the first is bad news.

**1. First-version precision = 0.17 (4 of 20 held), far below 0.7.** But the cause was not
"the rules imagine things" — **a code bug was making the rules lie**. When `matchBrace`
reaches a line `} catch (Exception e) {` it counts the try's `}` and the catch's `{` as a
net-zero pair and **returns that line as its own end**, so the body reads empty —
and `swallow_exception` therefore reported "catch 块为空" on **every multi-line catch**.
Real code is nearly all multi-line catch, so 17 of the 20 came from this one line. The
fixture happened to write `try { run(); } catch (Exception e) {` on **one line** (net +1,
exactly dodging the bug), which is why **every test was green**. Without this fix any
precision number is just metering a broken counter.

**2. Five changes under "change the rules, do not add more" (no new rule):**

| What changed | Why |
|---|---|
| `matchBrace` starts counting depth only after the first `{` on the start line | The above; it also lets `catchBody` read a real body |
| `swallow_exception` recognises three "handled" shapes: rethrow / out-of-band alarm / returned failure | 11 labelled candidates were the log-then-alarm job convention — exactly the class D3 reserved for the model pass to dismiss |
| All rules skip test sources (`src/test/`, `*Test.java`) | 3 came from `try { fail(); } catch (Biz expected) { assertTrue(); }` — that is **asserting a throw**, not a swallow |
| `sql_injection` requires the SQL keyword at the **start of a literal**, and the concatenation to have **a non-literal side** | 2 candidates were `"select ... " + "where not exists(...)"` — two string constants, folded by javac, **not injectable**; 2 more were the word "update" inside English notification copy |
| `route_renamed` compares `canonicalPath` and counts **declarations**, not text | One 30-line style sweep `"checkModify"` → `"/checkModify"` **produced 30 high findings**. Spring's `PatternParser` treats the leading slash as optional — **the route never moved** — and `GrepJava(old)` hits because `checkModify` is a substring of `"/checkModify"`, so a consumer that **was already migrated** still counted as "still referencing" |
| `remote_in_loop` requires the call to **touch the loop variable**, and reads the whole statement rather than one line | Batch pagination (`while(true)` + the whole list per round) is not N+1; while builder line-wrapping puts the loop variable on a continuation line, so reading only the callee's line **loses a real N+1** |

Also added: the **`rule_rev` comments** D3 required but the implementation never had, and one
**contract violation** fixed — `sql_injection` and `route_renamed` used to emit `confirmed`,
while `rule.go` reserves `confirmed` for rules **without** a known false-positive boundary. A
boundary-carrying rule that ships `confirmed` slips past the `high AND confirmed` threshold
and lights the rail dot, which is precisely the "three false positives and it's switched
off" the Background measured. Both are `unverified` now.

**3. Re-measured: precision = 0.44 (16 of 36 hold) — the 0.7 gate still misses.**
The dot path is clean: in forty days there was **not one `high AND confirmed`**, so no
notification ever fired in error. What is not clean is the report page: `remote_in_loop`
is 9 for 9, `swallow_exception` is 20 false out of 27.

Those 20 are not random noise, they are **one shape**: "a side-effect failure is
deliberately tolerated" — a notification email failing must not break the main flow (the
source says so in a comment), a batch task leaves a failed row unmarked for retry, UA/IP
parsing falls back to the raw value, a validation degrades to an empty return. **That is
exactly the class D3 reserved for the model pass to dismiss**, not something the rule layer
should keep chasing: the next step would be regexes reading comments and judging intent,
which turns the rules back into the unreproducible guesser this design exists to avoid.
**So the road to 0.7 runs through `review.ai`, not through more rules** — reopening the
gate means letting a model actually run over those 20 and dismiss them, which needs a
reachable inference endpoint (this machine's LM Studio was not reachable), and what gets
measured is "how many rows survive the review into the report are real", not the rules'
number alone.

**Conclusion and wording discipline**: acceptance 2 **was done and the gate missed**, so
`review.notify=all` still must not be used; `high` (the only path that can light the dot)
is safe to switch on because its reading in this window is 0. The rule layer alone sits at
0.44 and **that number must not be written as "the feature works"** — it is "no longer
lying", not "sees accurately".
