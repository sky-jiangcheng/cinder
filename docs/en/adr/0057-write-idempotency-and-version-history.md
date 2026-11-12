# ADR-0057: Write idempotency, optimistic locking and complete version history — schema v31

Status: **Accepted (design settled and shipped 2026-11-12)**. The v31 migration, all four write paths, the rolling snapshots and `cinder restore` are in code; see "Implementation status" for the three places where implementation corrected the design.
Date: 2026-11-12
Related: [ADR-0055](0055-agent-surface-contract-and-observability.md) (the schema half of D5), [ADR-0025](0025-backup-recovery.md) (local snapshot tiers), [ADR-0015](0015-w3-minimal-compile-loop.md) (model output lands as pending first), [ADR-0054](0054-rolling-checkpoint-handoff.md) (milestones vs. the idempotency key)
Starting schema: v30 → **v31 (executed)**; `CoreVersion` 2.0.0 → 2.0.0 (**MCP tool surface unchanged**; the `UpdateNoteFull` binding gains one `expectedVersion` argument — see "Implementation status")

## Background

Half of ADR-0055's D5 could be fixed without touching the schema, and that half shipped on 2026-11-12: the explicit `RestoreBackup` entry, `FileAnswerAsPage` landing as pending, the partial-success marker on compile jobs, import failures bubbling up, and the drainer's exponential backoff. The four items below all require structural changes, so this ADR settles the design only.

### 1 · No idempotency key — a replay is a second record

`CreateHandoffNote` at `internal/service/handoff.go:84` goes through `CreateNoteWithMeta`, is append-only, and has no dedupe key. The SessionEnd hook, a retry of `repotory_handoff`, and ADR-0054's rolling milestone checkpoints can all write twice for the same session; `handoff_capture.go`'s auto-capture writes yet another one next to the human handoff. The result is two near-identical handoffs side by side in `BuildProjectContext`, with no way for a reader to tell which is final.

### 2 · Writes are last-write-wins, with no cross-process protection

`UpdateNoteFull` at `internal/db/notes.go:253` is `UPDATE … WHERE id = ?` with no precondition; `internal/db/notes.go:265` only touches `updated_at`. The desktop app (Wails) and the IDE's `cinder-mcp` are two processes with two connections, and WAL plus `busy_timeout` only guarantee "no SQLITE_BUSY error" — **not "no overwrite"**. Conflict detection at the note level is entirely absent: the later write wins and the earlier one leaves no trace (version history only records the version it itself wrote).

### 3 · Version history misses the created state, and snapshots hold the post-edit value

`note_versions_snap` at `internal/db/migrate.go:169` is `AFTER UPDATE`, and v11 (`migrate.go:204-215`) narrowed its trigger condition to "one of the four business columns actually changed". Two things did not change:

- **Creation never enters history**: `CreateNoteWithMeta` does not fire it, so a note's v1 exists only in `project_notes` itself; once it is edited or deleted, the initial content is unrecoverable.
- **Snapshots hold `new.*`**: updating to v2 inserts v2's content, so the history is a series of post-edit states that duplicate the current value in `project_notes` and lack the first version. Rolling back to "before the edit" therefore needs reconstruction, which is precisely what row-level history exists to avoid.

### 4 · Snapshot granularity is still "the first one each day"

`internal/db/backup.go:57-60` returns early when today's snapshot exists and is readable (deliberate daily idempotency). With a restore entry point in place, that granularity becomes a problem: if something is broken mid-day, the nearest point is "before the app first opened today", and the intervening hours can only be picked back note by note from version history — which is itself missing the created state (item 3).

## Decision

All four land in **one schema v31 migration**, not three batches (a migration is not free; three of them means three table rewrites and three rollback risks).

### D1 Idempotency key: `project_notes.idempotency_key`

New column `idempotency_key TEXT DEFAULT ''` plus a partial unique index `UNIQUE(project_id, idempotency_key) WHERE idempotency_key <> ''` (the empty string stays out of it). Writers derive a stable key:

| Writer | Key |
|---|---|
| `CreateHandoffNote` | `handoff:<project_id>:<session_id>`; without a session id, `handoff:<project_id>:<sha256 of summary>` |
| Auto-capture (`handoff_capture.go`) | Same, sharing the namespace with the human handoff — an auto and a human record for one session must not both exist |
| Plugin import (`runtime.upsertDoc`) | `<source>:<doc external id>:<content sha256>`; changed content means a new key, complementing 12.12's content-hash skip |

A conflict returns the existing note's id with `Deduped=true` instead of erroring or inserting. **Keys are generated server-side only**; MCP tools do not accept a client-supplied key — letting an Agent invent its own key is the same as having none.

### D2 Optimistic locking: `project_notes.version`

New `version INTEGER NOT NULL DEFAULT 1`. `UpdateNoteFull` gains an `expectedVersion` parameter, the SQL becomes `WHERE id = ? AND version = ?` with `SET version = version + 1`, and `RowsAffected() == 0` returns `ErrVersionConflict` (classified by `internal/errcode` under a new `conflict` code, **not retryable** — the caller must re-read first). The UI submits the version it read when opening the editor; non-interactive writers (bulk import, compile jobs) pass `0` meaning "do not check" — an explicit opt-out, never the default.

### D3 Complete version history: `AFTER INSERT` + store `old.*`

v31 rebuilds both triggers:

- `note_versions_snap_ins AFTER INSERT`: inserts the initial snapshot.
- `note_versions_snap_upd AFTER UPDATE` (keeping v11's four-column `WHEN` condition): inserts **`old.*`**, the pre-edit state.

`note_versions` thereby becomes the complete series of past states (v1 … vN-1), with the current value in `project_notes`; rolling back to version N means writing history row N back. `note_versions` gains `version INTEGER` aligned with D2, so "go back to a version" also restores the version number.

### D4 Rolling snapshots: several per day, retained by count

`CreateBackup`'s daily idempotency stays as the "daily snapshot", and a rolling `snapshot-<YYYYMMDD-HHMM>.db` is added, triggered when "at least N minutes since the previous rolling snapshot and the database has been written to" (`MAX(created_at)` from `note_versions` is a cheap write marker; no new table). Retention is by count (6 rolling + 14 daily), and the filename remains the idempotency key.

### D5 Restore stays explicit and verifiable

`db.RestoreBackup(src, dst)` (shipped in this batch, `internal/db/backup.go:306`) writes only the caller-supplied destination path, renames atomically, cleans up the temporary file on failure, and **never replaces the live `repotory.db`**. v31 only adds a CLI entry `cinder restore --from <snapshot> --to <path>`; still no UI wiring and no automatic execution — restore is destructive, so it does not get a "just happened to run" path. Which rolling snapshot to use is the user's call; the tool only verifies and reports the destination.

## Consequences

- **The migration is a full table rewrite**: two columns, a partial unique index and a `version` backfill; the bigger the database the slower it gets. It must stay single-transaction and re-entrant (as `migrate.go` already is), and must log its duration.
- **Optimistic locking creates a new failure surface**: concurrent edits go from "silent overwrite" to "explicit conflict", so the UI needs actionable wording (re-read / merge / overwrite), otherwise the first encounter reads as a regression. `conflict` is not retryable and that must be stated in the MCP tool descriptions, or Agents will retry blindly.
- **Idempotency keys change history semantics**: a repeated handoff for one session goes from two rows to one, so "count the rows to see how many times it was written" stops working; existing duplicates are not cleaned up (deduping user data is not a migration's job).
- **Storing `old.*` reverses the meaning**: any code or UI reading `note_versions` as "snapshot = state at that time" must change, and **created states already lost cannot be backfilled** — v31 only guarantees completeness from then on.
- **Rolling snapshots cost IO and disk**: six extra full copies; for large databases (>1 GiB) the retention policy needs recomputing.

## Alternatives

- **Use `updated_at` as the optimistic lock**: millisecond precision, so same-millisecond concurrent writes slip through, and `updated_at` is already refreshed unconditionally by `UpdateNoteFull`, which makes it self-inconsistent. Rejected.
- **`BEGIN IMMEDIATE` to serialise every write**: solves lock contention, not overwrites; the read-modify-write window remains across processes and read paths get slower. Rejected.
- **Idempotency via "select then insert"**: a TOCTOU window — two processes both miss and both insert. A unique index is the only reliable form. Rejected.
- **No `version` column; detect conflicts from the newest `note_versions` row**: history lacks the created state (item 3), so "newest row" is unreliable, and comparing content hashes misreads "changed back" as no conflict. Rejected.
- **Leave rolling snapshots to manual user backups**: breaks ADR-0025's "a local copy exists by default" premise, which is exactly how the 2026-10-14 corruption happened. Rejected.

## Implementation status (2026-11-12)

**Shipped in full.** One migration (`EnsureNoteWriteContract` in `internal/db/migrate.go`, v31), all four write paths, the rolling snapshots and the restore entry, each with an assertion that can go red.

- **D1 idempotency key** — `project_notes.idempotency_key` plus the partial unique index `(project_id, idempotency_key) WHERE <> ''`; `db.CreateNoteIdempotent` inserts and recovers from the unique violation instead of selecting first; keys are server-generated and the MCP tool **does not accept** one. The three key shapes live in `service.handoffIdempotencyKey`: session-scoped `handoff:<project>:<session>`, milestone-scoped `handoff:<project>[:<session>]:milestone:<digest>`, and `handoff:<project>:sha256:<digest>` when there is no session; plugin imports use `import:<project>:<source>:<title digest>:<content digest>`.
- **D2 optimistic lock** — `project_notes.version`; `UpdateNoteFull(..., expectedVersion)` compares inside the same UPDATE (no read-then-write in Go), and on `RowsAffected()==0` re-checks existence: present means `errcode.ErrVersionConflict` (code `conflict`, not retryable). `UpdateNote` / `UpdateNoteMeta` / `RestoreNoteVersion` bump the counter too — they change the very columns the snapshots store.
- **D3 version history** — v11's `note_versions_snap` is dropped for `note_versions_snap_upd`, which inserts `old.*`; `note_versions.version` records which version a snapshot is the state of (0 = recorded before v31, unknown).
- **D4 rolling snapshots** — `db.CreateRollingBackup` + `service.rollingBackupOnce`, hooked onto the existing hourly tick and the startup point: `minGap = 30min`, `retain = 6`, rotating separately from the daily files. The write marker is `note_versions.created_at` **plus** `project_notes.updated_at` (see correction ②).
- **D5 restore** — `cinder restore --from <snapshot> --to <path>` (`cli.go`). The subcommand is dispatched before Wails boots, **refuses** the live database path, and fails when the target already exists.

### Implementation corrections (three places the design was wrong)

**① D3 ships one `AFTER UPDATE` trigger, not an `AFTER INSERT` one too.** The design wanted both: store v1 on insert, store `old.*` on update. But once the snapshot holds `old.*`, the first edit already writes the created state (`old.version = 1`), so an insert trigger would put the same content in history twice and spend half of the 50-row budget on duplicates. The version-keyed dedupe that would fix that is worse: a script plugin holding `ctx.DB()` can UPDATE without bumping `version`, and the dedupe would silently drop real snapshots. The shipped semantics are "history = v1..vN-1, current value in `project_notes`" — exactly what the design's closing sentence says — with the created state entering history the moment it becomes history.

**② D4's write marker cannot be `note_versions` alone.** It only gains a row when an EXISTING note is edited, so a day of writing brand-new notes looks completely idle and the rolling snapshot never fires. `project_notes.updated_at` is what covers "created + edited". Both are compared against `datetime(?, 'unixepoch')` inside SQLite: `CURRENT_TIMESTAMP` writes UTC while the file's mtime is local time, so comparing them in Go is off by the timezone — on a UTC+8 machine the "was there a write" question would answer "no" every single time.

**③ Restoring does not rewind the version number.** D3 said `note_versions.version` aligns with D2 "so restoring a version can rewind the counter too". Actually rewinding makes every client holding a newer version look up to date, and the next write becomes the silent overwrite this counter exists to prevent. Restoring is therefore a FORWARD write (n → n+1), which also leaves the pre-restore state in history.

### Remaining differences from the design

- **Rolling snapshot filenames** are `repotory-<YYYYMMDD-HHMM>.db`, not `snapshot-<YYYYMMDD-HHMM>.db`: rotation safety rests on "only delete file names this product wrote", and sharing the daily prefix with its own full-name regex is easier to audit than adding a second prefix.
- **`CoreVersion` stays put and the MCP tool surface stays put.** `repotory_notes_update` gains no `expected_version`: adding a tool argument means touching the fixture, the docs and the version gate, while an agent has no "version when I opened it" — it wrote what it just read. Cross-process protection lands on the HUMAN side (the desktop editor posts its version). The cost is recorded honestly: agent writes are still last-write-wins.
- **The desktop editor's conflict handling is "refuse clearly + actionable wording"** (`errors.noteConflict`): no automatic overwrite and no in-place merge — the editor has no diff surface, and adding a merge view is a separate UI decision.
- **The inverted history semantics get no migration compensation**: rows written before v31 hold post-edit content and cannot be turned back into pre-edit content, so their `version` stays 0 to say "unknown".

Tracking item: TODO `12.14` (checked).
