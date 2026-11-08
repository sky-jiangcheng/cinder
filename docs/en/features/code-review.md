---
title: Incremental code review
order: 9
---

# Incremental code review

Every day, look at what **yesterday's commits** introduced in the collected projects — the kind
of problem that compiles, passes integration, and then breaks in production. The canonical example
is a cross-project Feign contract: A calls B, A's method parameter is missing `@RequestBody`,
everything compiles, and the parameter silently disappears at runtime.

Judgement has two layers: **deterministic rules sift candidates, the model only confirms them**
([ADR-0053](../adr/0053-incremental-commit-review.md)). It is **off by default** — it reads your
source, and with AI on it sends the relevant excerpts off the machine.

## Why not "let the model read the diff"

Three measurements came first:

| Fact | Consequence |
|------|-------------|
| A collected project's daily increment is 41–94 commits, 14k–47k lines of diff | Feeding that to a model is 100k+ tokens a day, and the real problem is not cost but **irreproducibility**: three findings today and two tomorrow for the same commit, and the user stops looking |
| A's line `X foo(Req req);` looks harmless in a diff | The mismatch only exists at B's `@RequestBody`, and B is not in that day's diff. A model reasoning freely can only guess |
| Of 113 Feign POST/PUT methods measured, 38 had no `@RequestBody` on the parameter | Rules do find things, but **false positives decide whether this feature lives**: three of them and it gets turned off |

So: rules establish facts, the model answers "does this code have a legitimate reading", and the
human decides.

## Only what *that day* introduced

Only `added` lines count — a method that has been wrong for three days but was only re-indented
today is not reported. **Existing debt is deliberately out of scope**: dumping dozens of old
problems at once is the same as having no report.

It is the blast radius that matters, not the diff line: once a signature changes, untouched lines
in the body can be consequences of this commit too.

## First rule set (Java)

| Rule | Catches | Default |
|------|---------|---------|
| `feign_missing_body` | A Feign method takes a POJO without `@RequestBody` (cross-project contract mismatch) | on |
| `feign_get_body` | A request body on `@GetMapping` — the other side never receives it | on |
| `route_renamed` | The provider renamed a path, the consumer still points at the old one | on |
| `feign_arity` | Declared parameter count disagrees with the path variables / request params in the annotation | on |
| `swallow_exception` | A `catch` block that neither logs nor rethrows | on |
| `tx_self_call` | A `@Transactional` method called from inside the same class — the proxy never runs | on |
| `sql_injection` | SQL built by concatenating parameters with `+` | on |
| `shared_date_format` | A static shared `SimpleDateFormat` (not thread safe) | on |
| `remote_in_loop` | A remote call inside a loop (N+1 round trips) | on |
| `sensitive_log` | Phone numbers / ID numbers / card numbers written to logs | **opt-in** — logs may legitimately carry them, so the choice is yours |

Rules only recognise **explicit contracts** (Spring Cloud Feign annotations); they do not guess at
in-house wrappers. Rules go stale as frameworks move, which is why each one documents the framework
behaviour it assumes.

## Reading a report

Five fields per finding are worth your attention:

- **`severity`**: `high` / `medium` / `low`.
- **`verdict`**: `confirmed` means the model checked and the problem stands; `unverified` means a rule fired and **nobody checked** — the code may well have a legitimate reading. **Do not treat `unverified` as broken.**
- **`evidence`**: the actual lines that matched, expandable. Without evidence there is no judgement.
- **`skipped`**: how many files exceeded the diff budget (2000 lines by default) and were skipped. **A skip is counted, never silently dropped.**
- **`ai_calls`**: how many model calls this run cost. Zero means rules only (no model configured, or "no AI" ticked).

Zero candidates means zero model calls. That is a hard constraint, not an optimisation.

## Three entry points, one report

| Surface | How |
|---------|-----|
| Desktop "Code Review" page | Findings grouped by file, filterable by severity, evidence expandable; "Run now" in the toolbar |
| MCP | `repotory_review_latest` (read the latest), `repotory_review_run` (run a day, with `dry_run` / `no_ai` / `force`) |
| CLI | `cinder-review --since yesterday [--dry-run] [--no-ai] [--json]`, for launchd or an OS scheduler (build from source: `go build -o bin/cinder-review ./cmd/review`) |

The same day and scope is reviewed **once**: idempotency is enforced by `UNIQUE(run_date, scope)`,
a completed run returns the stored report, and only `force` recomputes. That is not a performance
trick — re-running would send the same candidates to the model again and again.

The report is **data, not notes**: it lives in `review_runs` / `review_findings` and never enters
the knowledge base. Mixing machine output into "my notes" destroys what that phrase means.

## Turning it on

Settings → **Review**:

| Setting | Default | Meaning |
|---------|---------|---------|
| Enabled | off | Master switch. While off, neither the schedule nor the dot runs |
| Time | 09:30 | When to review yesterday's commits |
| Scope | all collected projects | Or pick specific projects |
| AI review | on | Off means rules only, zero external calls |
| Notify | high and confirmed only | The dot on the Code Review icon. `all` / `off` also available |
| Per-file budget | 2000 diff lines | Beyond that a file is skipped and counted in `skipped` |

The dot counts `high AND confirmed` only: `unverified` never raises it, and a zero count draws
nothing at all.

## Explicitly out of scope

- **No PR comment bot.** Cinder is not CI; findings are for a human and never block a merge.
- **No scheduled full audit.** A full sweep is a different problem (clearing existing debt), not a switch here.
- **No cron expression parser.** A ten-minute ticker asking "is it past the time today, and is there no done row" is enough, and behaves the same on every platform.
- **No pretence of covering every language.** The first set is Java, because the reviewed repos are Java. Go / TS rule packs come when needed.
