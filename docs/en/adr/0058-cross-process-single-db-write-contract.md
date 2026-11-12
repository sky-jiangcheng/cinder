# ADR-0058: Cross-process write contract — who owns writes when three entry points share one SQLite file

Status: **Accepted (raised by the 2026-11-12 engineering assessment; the owner chose option A, single-writer proxy, the same day; implemented 2026-11-12 with one owner revision recorded in the "Implementation record" below — election-based writes rather than refuse-when-desktop-absent; tracked as TODO 13.1)**
Date: 2026-11-12
Related: [ADR-0002](0002-c-end-repositioning.md) (local-first single database), [ADR-0025](0025-backup-recovery.md) (VACUUM INTO snapshots), [ADR-0055](0055-agent-surface-contract-and-observability.md) (problem four: data-layer fault tolerance), [ADR-0057](0057-write-idempotency-and-version-history.md) (row-level optimistic locking)
Starting schema: v31 → **v31 (no schema change)**

## Background

Three entry points can open the same `repotory.db` at once:

| Entry point | Process shape | Typical writes |
|---|---|---|
| Desktop `cinder` | Wails, single instance | note CRUD, wiki approval, settings |
| `cinder-mcp` | stdio, one per IDE | handoff / notes_create / chat / review |
| `cinder-server` | HTTP / headless | import sync, compile jobs, backup ticker |

Every existing protection is either in-process or after the fact:

- `db.SetMaxOpenConns(1)` (`internal/db/db.go:24`) serializes connections inside one process only; the sole cross-process mechanism is `PRAGMA busy_timeout=5000` (`internal/db/db.go:35`), after which the caller gets `SQLITE_BUSY`.
- Backups run `VACUUM INTO` on the live connection (`internal/db/backup.go:86`), holding a lock for the whole snapshot; v31 added 30-minute rolling snapshots on top.
- The MCP process deliberately does not run `Startup()`, with a comment saying it would race the desktop tickers (`cmd/mcp/main.go:68-74`). That reduces contention; it does not define priority.
- ADR-0057 D2's optimistic lock is **row-level** and only covers the `UpdateNoteFull` family; ADR-0057 itself records that "the agent side is still last-write-wins (no `expected_version` on the tool surface)".
- 12.9 made MCP reuse `OpenWithRetry` (`internal/db/open_retry.go`), which fixes opening, not holding a write lock.

Conclusion: **write ownership has no contract**. Today it is "whoever gets the lock first writes; everyone else fails after five seconds". Long writes (backup, batch compile, embedding backfill) surface that failure to agent tool calls.

## Decision (owner, 2026-11-12)

**Option A, single-writer proxy**: the desktop app owns writes; `cinder-mcp` and `cinder-server` forward their writes to it over a local channel.

The reason is that it does not invent a second concurrency primitive: a row-level conflict (two people editing one note) is already expressed as a decidable `conflict` by ADR-0057 D2's `version` optimistic lock. Re-stating the same question at database level with a lease (B) would put two "who may write first" semantics in one file, neither aware of the other. And "exactly one writer" is already the desktop app's shape — it is a single-instance application and the other two processes are its clients.

The decision answers the four open questions:

| Original open question | This ADR's answer |
|---|---|
| 1. Is "MCP writes only while the desktop runs" acceptable? | **Yes** — it is the direct consequence of A and its only real cost |
| 2. How long may a long write block other processes? | **Never**: write requests queue inside the single writer, so the client sees waiting rather than `database is locked`; bounded wait vs immediate rejection is below |
| 3. Must backups leave the live database? | **No**: the single writer takes them and no other process runs `VACUUM INTO` any more — the risk of snapshotting a live database came from a second concurrent writer, which no longer exists |
| 4. Is a write lease worth it? | **No**: crash residue, clock skew and fairness buy nothing here (see the first paragraph) |

## Still to settle at implementation time

1. **Write behaviour when the desktop is absent.** A means "no writer, no writes". Implementation must pick one and write it down: **reject with an actionable error** (recommended — two coexisting write paths is the same as no contract), or let MCP degrade into a temporary writer when the desktop is missing (which is option C stacked on A, and then the switch condition has to be part of the contract). **Implement the former by default.**
2. **Transport shape.** Loopback HTTP (reusing `internal/httpapi` and the existing `loopbackGuard`) or a Unix domain socket. The test is "who can forge this channel": a loopback port is reachable by any local process, while socket file permissions are a real boundary.

## Open questions (answered above; original text kept)

1. Is "MCP can write only while the desktop app is running" acceptable? That decides whether option A exists.
2. How long may a long write block other processes: 5s, 30s, or never?
3. Must backups leave the live database (copy or read-only snapshot)?
4. Is a write lease worth its crash residue, clock skew and fairness problems?

## Options and trade-offs

| Option | Approach | Upside | Cost / risk |
|---|---|---|---|
| A Single-writer proxy | Desktop owns writes; MCP / server forward writes over local HTTP or a Unix socket | One writer, cross-process lock disappears; aligns with row-level optimistic locking | Agents cannot write without the desktop, conflicting with "start a local service from the IDE"; extra hop; a desktop crash stops writes |
| B Cross-process lease | Lease table or file lock with a short TTL acquired before writing | Keeps three independent processes | Crash residue, clock skew, fairness; non-lease writers still hit `busy_timeout`; most complexity |
| C Backoff + shrink long writes | Raise `busy_timeout`, exponential backoff on write failure, backups on a copy, compiles committed in batches | Smallest change, can start now | Postpones failure without bounding it; no fairness; latency spikes remain |
| D Read/write split | MCP reads via a read-only connection and WAL snapshots; writes still go through desktop / server | Reads are never blocked by writes | Write tools (handoff / notes_create) still need a write path, so this collapses into A; read-only connection vs `busy_timeout` must be verified |

## Explicitly undecided

- ~~No option is chosen~~ — **option A was chosen on 2026-11-12**; `busy_timeout`, backup paths and MCP open behaviour get rearranged around A at implementation time, and no code moves in this ADR.
- No external database or distributed lock service (local-first, zero-account is a product premise).
- No write lease (B), and no read/write split (D) in this round — D can follow later as an independent "reads are never blocked by writes" optimisation; it does not change who owns writes.

## Acceptance criteria

- Two processes (desktop + MCP) writing concurrently while a backup and a compile round run, 30-minute soak: `database is locked` never surfaces as a tool error.
- After either process is `SIGKILL`ed, the other regains write access within 10s; **with a proxy, additionally assert that requests queued across a writer restart are either replayed or explicitly rejected — never silently dropped**.
- A test that can go red: hold a long write and assert the chosen contract behaviour (queue / explicit rejection / proxying) rather than a random timeout; plus an assertion that "MCP writes fail with an actionable error when the desktop is absent", because that is now part of the contract rather than an accident.

## Implementation record (2026-11-12)

**One owner revision first, because it changes a semantic of the decision above** (the original text stands; this section wins where they disagree): at landing the write-owner rule became **election-based single writer** — ownership binds not to "the desktop process" but to an OS file lock at `<datadir>/repotory.writer.lock`: **whoever holds it is the one writer**. The desktop still preempts on startup, as the original decision wanted (it asks a self-elected agent to yield and takes over; if the agent is mid-write the desktop degrades to a client and the contract still holds). The reason is the README's first-screen promise — "`cinder-mcp` … **zero desktop app required**": the write tools (handoff / notes_create / chat) are that product's reason to exist, and under the original refuse-by-default a headless install would be write-dead on day one. Election keeps every structural gain of choosing A (one writer at a time, no second concurrency primitive, only the writer snapshots) and turns "desktop absent" from "no writes" into "take over in place". The two "before implementing" questions therefore resolve as: **① absence = takeover; only when takeover is impossible, refuse with an actionable error (`writer_unavailable`, retryable); ② transport = loopback HTTP + a per-start token** (the token lives in a 0600 endpoint file with the same boundary as repotory.db itself: any process that can read it can already read the database, so the channel adds no attack surface; UDS is not portable to Windows, and `x/sys/windows` was needed for the lock primitive anyway — one semantic on both ends is cheaper).

**Shipped** (`go build ./...` + full `go test` + both gates proven red on injected violations):

- **`internal/writer`** — election + transport, Service-agnostic: flock (LockFileEx on Windows; `golang.org/x/sys` promoted from indirect to a direct dependency), `repotory.writer.lock` (its content names the holder pid, so "who is blocking me" is answerable) and `repotory.writer.endpoint` (addr / token / pid / process / contract + schema versions). The lock lives in the open file description, so SIGKILL releases it — recovery does not depend on cleanup code. `TestLockSurvivesProcessDeath` proves that with a real child process that kills itself.
- **`internal/writerapi`** — the **catalogue**: ten named methods (notes.create/update, handoff.create, chat.create/append/rename, scan.run, review.run, capture.claude_session, audit.record), one args struct per method, **no reflection** — the RPC bridge's "anything that exists is callable" is the exact opposite of what a write contract needs. `Manager` routes by role: writer executes locally, client forwards; **one Executor serves both, so the two modes cannot drift**. Forwarded failures keep the writer's classification (`errcode.Remote` carries the code): a `conflict` cannot be downgraded to `internal` by crossing a process hop.
- **The takeover chain**: a client whose target writer is unreachable (connection refused = the request demonstrably never executed, `StaleError.PreExecution`) re-runs the election and may become the writer. **"Never delivered" is strictly separated from "died mid-request"**: the latter is never auto-replayed (a second gate on top of ADR-0057's idempotency keys); the ambiguity is reported upward.
- `repotory_scan`'s two writes (root seeding and the scan's own project / repository / daily_stats rows) merge into one catalogue method — "one tool call = one catalogue method" is the precondition for the leak gate.
- **Audit rows ride the same contract**: `Service.SetAuditForwarder` routes a non-writer's `ai_call_log` INSERT through the channel, and the row keeps the **originating** surface (an mcp call still reads `mcp` in the trail). Audit failures stay swallowed: they must not fail the model call, but neither should they lose rows to lock contention.
- **Entry points**: `main.go` (desktop, AssertWriter, takes over at startup), `cmd/mcp` (six write tools, eight catalogue call sites, all through the Manager), `cmd/repotory-capture` (the SessionEnd hook — previously unarbitrated: it opened its own write at every session end while the desktop and an MCP commonly held the file). **`cmd/server` is not migrated in this batch** and still writes under the old semantics; recorded as a TODO to converge.
- **Gates that can go red** (one per acceptance bullet): `TestNoDirectServiceWritesOutsideTheContract` AST-scans every non-test file in `cmd/mcp` — any `svc.<write>` call that bypasses the catalogue fails (injection-proven); `TestWriteToolsCoveredByCatalogue` pins the other direction (a catalogue method no tool reaches is dead contract surface); `TestLongWriteQueuesInsteadOfFailingLocked` is the **unitised stand-in for the 30-minute soak** — with the writer holding a transaction longer than `busy_timeout=5000`, the client's forwarded write **queues to success** instead of surfacing `database is locked`; `TestUnreachableWriterIsActionableError` pins that the refusal names the remedy (start Cinder Desktop / wait for takeover) and classifies as `writer_unavailable` + retryable; `TestConcurrentForwardedWritesAllLandOnce` proves the queue drops nothing. The ≤10s-after-SIGKILL budget is covered by `TestWriterDeathTriggersTakeover` plus the child-process death test.

**Costs and residue, stated plainly**:
- Local reads still pass SQLite's own locks — the contract arbitrates writes, not reads; `busy_timeout` stays as the read path's backstop.
- **The open-database path is unchanged**: `db.OpenWithRetry` still runs in every process (the WAL pragma, the `Ensure*` re-assertions and migration stamping are file-level writes). They always serialised across processes via busy_timeout, and every reader needs a migrated file to read — a non-writer cannot open read-only and skip them. Untouched in this batch; if 13.6 migrates the server too, revisit 'non-writers open read-only, the writer owns migrations'.
- Minute-scale writes like `repotory_scan` now queue inside the writer; the forwarder's cancellation propagates (ctx crosses HTTP), and two agents scanning concurrently means the second waits — that is the contract working, not a regression.
- The desktop moved from "the only writer" to "the preferred writer": while an agent self-elects, the desktop's backup ticker is not running, so snapshot density dips briefly; the desktop takes over the moment it starts, so the window is bounded.
- **The desktop's own UI binding surface is not catalogue-backed in this batch**: normally the desktop *is* the writer, so nothing changes. Only in the window where the desktop starts while an agent is mid-write and the takeover is refused, the desktop's bindings and the agent-writer run concurrently — degrading to the pre-contract behaviour (busy_timeout as backstop). Not a new hole, but not closed either. It converges with `cmd/server`: route the binding layer's writes through the Manager, or refuse them when `!IsWriter()`.
- **`CoreVersion` unchanged (2.0.0)**: tool names, arguments and the success wire shape are untouched; the new error code travels inside the open code enum 2.0.0 already declares. `scripts/mcp-tools.fixture.json` needs no edit.
