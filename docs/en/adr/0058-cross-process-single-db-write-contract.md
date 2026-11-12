# ADR-0058: Cross-process write contract — who owns writes when three entry points share one SQLite file

Status: **Accepted (raised by the 2026-11-12 engineering assessment; the owner chose option A, single-writer proxy, the same day; not implemented — tracked as TODO 13.1)**
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
