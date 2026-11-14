#!/usr/bin/env bash
# The 30-minute soak ADR-0058 accepted but never ran (TODO 13.1 residual ①).
#
# What it proves, and why it cannot be a unit test:
#
#   The contract's acceptance criterion is "two processes writing concurrently,
#   with a backup and a compile in flight, for 30 minutes: `database is locked`
#   never reaches the caller as a tool error". The unitised stand-in
#   (TestLongWriteQueuesInsteadOfFailingLocked) proves ONE 6.5s hold queues ONE
#   write. It cannot see the combination that real time produces: tickers
#   firing on their own schedule, backups taking the write lock for seconds at
#   unpredictable moments, several clients queueing at once, and a writer that
#   dies mid-write.
#
# Roles (the contract binds the lock to a FILE, not to a process — that was the
# 13.1 decision, so the writer being cinder-server rather than the GUI is the
# same code path, not a substitute):
#
#   writer    cinder-server, the single writer, running its tickers
#   clients   N × cinder-mcp, each a real stdio MCP child, writing concurrently
#   long hold RPC RunBackupNow (VACUUM INTO the whole database) — repeatedly
#
# Phases:
#   A. steady state: clients write for most of the run while backups take the
#      lock again and again.
#   B. takeover: the writer is SIGKILLed while the clients are mid-write; the
#      clients must re-elect, take over, and keep writing. A lock left behind by
#      a dead process would show up here as a permanent failure.
#
# Usage:
#   scripts/soak-writer-contract.sh                  # 30 minutes (ADR's number)
#   scripts/soak-writer-contract.sh --minutes 3      # validate the harness
#   scripts/soak-writer-contract.sh --minutes 1 --clients 3 --keep
#
# Exit: 0 = every criterion held; 1 = a contract violation; 2 = setup problem.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MINUTES=30
CLIENTS=4
INTERVAL_MS=200
KEEP=0
PORT="${SOAK_PORT:-19881}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --minutes) MINUTES="${2:?}"; shift 2 ;;
    --clients) CLIENTS="${2:?}"; shift 2 ;;
    --interval) INTERVAL_MS="${2:?}"; shift 2 ;;
    --port) PORT="${2:?}"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) sed -n '2,40p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

say()  { printf '\n=== %s ===\n' "$*"; }
note() { printf '  %s\n' "$*"; }

# Milliseconds since the epoch, WITHOUT forking. `date +%s%3N` costs a few
# milliseconds per call — long enough to matter here: the instant the writer
# is killed has to be pinned tightly, because the client's EOF arrives within
# a millisecond or two of the kill and a sloppy timestamp puts the failure
# before the kill it was caused by.
now_ms() {
  if [[ -n "${EPOCHREALTIME:-}" ]]; then
    # 1794626675.647123 -> 1794626675647
    local sec="${EPOCHREALTIME%%.*}" frac="${EPOCHREALTIME#*.}"
    frac="${frac}000000"; frac="${frac:0:6}"
    echo $(( 10#$sec * 1000 + 10#$frac / 1000 ))
  else
    date +%s%3N
  fi
}

# A file's last-write time in epoch milliseconds — 0 when the file is gone.
# Used for the backup snapshots: their timestamp is the moment the VACUUM
# finished writing, which is the moment the writer's connection came back.
mtime_ms() {
  [[ -f "$1" ]] || { echo 0; return; }
  node -e 'try{console.log(Math.round(require("node:fs").statSync(process.argv[1]).mtimeMs))}catch(e){console.log(0)}' "$1" 2>/dev/null || echo 0
}

WORK="$(mktemp -d)"
HOME_DIR="$WORK/home"
BIN_DIR="$WORK/bin"
REPORTS="$WORK/reports"
mkdir -p "$HOME_DIR" "$BIN_DIR" "$REPORTS"
export HOME_SOAK="$HOME_DIR"

# Never leave a cinder process or a worker behind, however this exits.
PIDS=()
cleanup() {
  local code=$?
  for pid in "${PIDS[@]:-}"; do
    kill -TERM "$pid" 2>/dev/null
  done
  wait 2>/dev/null
  if [[ "$KEEP" == "1" ]]; then
    note "kept: $WORK"
  else
    rm -rf "$WORK"
  fi
  exit "$code"
}
trap cleanup EXIT INT TERM

say "building the real binaries"
(cd "$ROOT" && GOCACHE="${SOAK_GOCACHE:-/tmp/gocache-soak}" go build -o "$BIN_DIR/cinder-server" ./cmd/server) || exit 2
(cd "$ROOT" && GOCACHE="${SOAK_GOCACHE:-/tmp/gocache-soak}" go build -o "$BIN_DIR/cinder-mcp" ./cmd/mcp) || exit 2
note "server: $BIN_DIR/cinder-server"
note "mcp:    $BIN_DIR/cinder-mcp"
note "isolated HOME: $HOME_DIR (the user's real database is never touched)"

# ------------------------------------------------------------------ writer ---
# Start the writer and wait for /health. --web-dist is deliberately omitted: a
# soak of the write contract needs no SPA, and the extra static handler would
# only add noise.
say "starting the writer (tickers on)"
HOME="$HOME_DIR" "$BIN_DIR/cinder-server" -port "$PORT" >"$WORK/writer.log" 2>&1 &
WRITER_PID=$!
PIDS+=("$WRITER_PID")

heal_url="http://127.0.0.1:$PORT/health"
for _ in $(seq 1 200); do
  if curl -sf -m 2 "$heal_url" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$WRITER_PID" 2>/dev/null; then
    echo "writer exited during startup:" >&2
    tail -20 "$WORK/writer.log" >&2
    exit 2
  fi
  sleep 0.1
done
curl -sf -m 2 "$heal_url" >/dev/null || { echo "writer never became healthy" >&2; tail -20 "$WORK/writer.log" >&2; exit 2; }
note "writer healthy on :$PORT (pid $WRITER_PID)"

rpc() {
  local method="$1" args="$2"
  curl -sf -m 30 -X POST "http://127.0.0.1:$PORT/api/rpc" \
    -H 'Content-Type: application/json' \
    -d "{\"method\":\"$method\",\"args\":$args}"
}

# Seed one note so the project exists and FK targets resolve, and turn the
# backup switch on (it is off by default — a soak that cannot take a backup
# never exercises the long hold the contract exists for).
if ! rpc CreateNoteWithMeta '[1,"soak seed","seed","","knowledge","manual"]' >/dev/null; then
  echo "could not seed the writer: is the database new?" >&2
  tail -20 "$WORK/writer.log" >&2
  exit 2
fi
rpc UpdateConfig '["backup_enabled","1"]' >/dev/null || true

# Create the ONE note and ONE shared thread the workers write into.
#
# Bounded data is a property of a trustworthy soak, not a convenience: an
# earlier version created a note per iteration, the file reached 9 MB / 16k
# rows, VACUUM INTO then took minutes per snapshot, and the clients starved for
# the rest of the run. That measures SQLite's vacuum throughput, not the write
# contract. Writing into fixed rows keeps the file at seed size.
#
# The seeding itself runs through a small helper (scripts/soak/seed.mjs) rather
# than a printf pipe: a pipe closes stdin right after the last line, the MCP
# server reads EOF, starts shutting down, and cancels the tool call that has not
# answered — `write chat.create cancelled: context canceled`, which looks like a
# concurrency bug and is really the seeder hanging up.
SEED_JSON=$(node "$ROOT/scripts/soak/seed.mjs" --mcp "$BIN_DIR/cinder-mcp" --home "$HOME_DIR") || exit 2
SOAK_NOTE_ID=$(printf '%s' "$SEED_JSON" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{try{console.log(JSON.parse(d).note_id ?? "")}catch{console.log("")}})')
THREAD_ID=$(printf '%s' "$SEED_JSON" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{try{console.log(JSON.parse(d).thread_id ?? "")}catch{console.log("")}})')
if [[ -z "$SOAK_NOTE_ID" || -z "$THREAD_ID" ]]; then
  echo "seeding failed: $SEED_JSON" >&2
  exit 2
fi
export SOAK_THREAD_ID="$THREAD_ID"
note "seeded: note $SOAK_NOTE_ID, thread $THREAD_ID, backup_enabled=1"

# ------------------------------------------------------------------- soak ----
TOTAL_SECONDS=$(( MINUTES * 60 ))
# Phase B takes the last 20% of the run, but never more than 60s and never less
# than 10s: a takeover check needs the clients to be writing when the writer
# dies, not to spend the whole run recovering.
PHASE_B_SECONDS=$(( TOTAL_SECONDS / 5 ))
(( PHASE_B_SECONDS > 60 )) && PHASE_B_SECONDS=60
(( PHASE_B_SECONDS < 10 )) && PHASE_B_SECONDS=10
PHASE_A_SECONDS=$(( TOTAL_SECONDS - PHASE_B_SECONDS ))

say "phase A: ${PHASE_A_SECONDS}s of concurrent writes + repeated backups"
note "clients: $CLIENTS, interval: ${INTERVAL_MS}ms"

WORKER_ARGS=(--mcp "$BIN_DIR/cinder-mcp" --home "$HOME_DIR" --port "$PORT" \
  --note-id "$SOAK_NOTE_ID" \
  --ms "$(( PHASE_A_SECONDS * 1000 ))" --interval "$INTERVAL_MS")
for i in $(seq 1 "$CLIENTS"); do
  node "$ROOT/scripts/soak/worker.mjs" "${WORKER_ARGS[@]}" \
    --out "$REPORTS/worker-a-$i.json" >"$WORK/worker-a-$i.log" 2>&1 &
  PIDS+=($!)
done

# Backups take the write path for the whole VACUUM INTO. Fire them repeatedly
# so the clients are queueing against a held lock for a real fraction of the
# run, not once at the start.
#
# The mechanism is the POOL, not the file lock: the writer's *sql.DB has one
# connection (db.SetMaxOpenConns(1)), so a VACUUM INTO holds the only
# connection for its whole duration and every other write queues behind it.
# That is exactly the "long hold" the contract promises to queue instead of
# failing.
BACKUPS=0
BACKUP_FAILS=0
REAL_BACKUPS=0
# Each backup's TIME WINDOW, so the gate can tell whether a write was actually
# queued behind it. "A backup ran" is not the claim the contract makes; the
# claim is "a write queued behind the backup instead of failing" — and that is
# only proven if some write's window straddles a backup's window.
BACKUP_WINDOWS=()
# FORCE_BACKUPS defaults to ON, because CreateBackup is idempotent per DAY:
# once today's snapshot exists, every later RunBackupNow returns before
# VACUUM INTO ever runs, so the default soak never exercised the long hold the
# contract exists for (measured: 247 calls, 247 counted, exactly ONE real
# VACUUM). Dropping today's snapshot before each call makes each one a real
# hold. It is safe because HOME is the isolated soak directory, and
# FORCE_BACKUPS=0 turns it off for a run that cares more about write rate than
# about the busy path.
FORCE_BACKUPS="${FORCE_BACKUPS:-1}"
phase_a_end=$(( $(date +%s) + PHASE_A_SECONDS ))
while [[ $(date +%s) -lt $phase_a_end ]]; do
  if [[ "$FORCE_BACKUPS" == "1" ]]; then
    find "$HOME_DIR" -name "repotory-$(date +%F).db" -delete 2>/dev/null
  fi
  # A real VACUUM can take seconds; the default RPC timeout is 30s, which is
  # enough for the bounded soak database but not for a machine in trouble. The
  # call is a backup, not a write under test: a timeout here does not fail the
  # soak, and the writer keeps holding the lock until the VACUUM finishes.
  backup_from=$(now_ms)
  if out=$(curl -sf -m 180 -X POST "http://127.0.0.1:$PORT/api/rpc" \
      -H 'Content-Type: application/json' \
      -d '{"method":"RunBackupNow","args":[]}' 2>&1); then
    BACKUPS=$(( BACKUPS + 1 ))
    # The snapshot exists after the call => a real VACUUM ran (we deleted it
    # before the call). Counted so the summary shows whether the busy path was
    # actually covered instead of assuming it.
    real=0
    if [[ "$FORCE_BACKUPS" == "1" ]] && find "$HOME_DIR" -name "repotory-$(date +%F).db" -print -quit | grep -q .; then
      REAL_BACKUPS=$(( REAL_BACKUPS + 1 ))
      real=1
    fi
    backup_until=$(now_ms)
    # The snapshot's own timestamp is when its VACUUM finished writing — and
    # that is the moment the writer's only connection came back. A write that
    # was still in flight at that instant and came back after it could not
    # have been served: it waited for the backup to release the connection.
    # That is the proof the long-hold gate needs, and it needs no threshold.
    hold_end=0
    while IFS= read -r snap; do
      ts=$(mtime_ms "$snap")
      if (( ts > hold_end )); then hold_end=$ts; fi
    done < <(find "$HOME_DIR" -name 'repotory-*.db' 2>/dev/null)
    BACKUP_WINDOWS+=("$backup_from:$backup_until:$hold_end:$real")
  else
    # A backup that FAILS is only a problem if it fails for a lock reason; a
    # plain refusal (already taken this hour) is normal.
    case "$out" in
      *"database is locked"*) BACKUP_FAILS=$(( BACKUP_FAILS + 1 )) ;;
    esac
  fi
  sleep 7
done
# The backup windows travel with the reports: the gate intersects them with the
# workers' write windows to decide whether the long hold was really exercised.
{
  printf '['
  sep=""
  for w in ${BACKUP_WINDOWS[@]+"${BACKUP_WINDOWS[@]}"}; do
    IFS=: read -r bs be bh br <<<"$w"
    printf '%s{"start":%s,"end":%s,"holdEnd":%s,"ms":%s,"real":%s}' "$sep" "$bs" "$be" "$bh" "$(( be - bs ))" "$br"
    sep=","
  done
  printf ']'
} >"$REPORTS/backups.json"
note "backups taken: $BACKUPS (real VACUUMs: $REAL_BACKUPS, lock-failed: $BACKUP_FAILS)"

for i in $(seq 1 "$CLIENTS"); do
  pid="${PIDS[$(( i ))]:-}"
  [[ -n "$pid" ]] && wait "$pid" 2>/dev/null
done
note "phase A workers finished"

# Every phase-A worker must have run clean. Their exit code is the gate's
# verdict on the lock bubbles; the JSON explains any failure.
PHASE_A_VIOLATION=0
for i in $(seq 1 "$CLIENTS"); do
  if [[ ! -f "$REPORTS/worker-a-$i.json" ]]; then
    echo "worker $i wrote no report — it died before finishing" >&2
    PHASE_A_VIOLATION=1
  fi
done

# ---------------------------------------------------------------- phase B ----
say "phase B: ${PHASE_B_SECONDS}s with the writer SIGKILLed mid-write"
note "this is the takeover: the lock is released by process death (no cleanup code)"

for i in $(seq 1 "$CLIENTS"); do
  node "$ROOT/scripts/soak/worker.mjs" --mcp "$BIN_DIR/cinder-mcp" --home "$HOME_DIR" \
    --port "$PORT" --note-id "$SOAK_NOTE_ID" \
    --ms "$(( PHASE_B_SECONDS * 1000 ))" --interval "$INTERVAL_MS" --kill-phase \
    --out "$REPORTS/worker-b-$i.json" >"$WORK/worker-b-$i.log" 2>&1 &
  PIDS+=($!)
done

# Let the clients get a few writes in, then kill the writer outright.
sleep 3
# The kill instant is bracketed, not sampled: the true instant lies between the
# two reads, and the bracket is what the ambiguity test intersects with.
KILL_FROM_MS=$(now_ms)
kill -9 "$WRITER_PID" 2>/dev/null
KILL_UNTIL_MS=$(now_ms)
# Every write that was in flight across that instant has an UNKNOWN verdict:
# the writer may have applied it before dying, and ADR-0058 forbids replaying
# it (a non-idempotent write applied twice is worse than one that was refused).
# The gate below counts those separately instead of calling them lost writes.
note "writer SIGKILLed (pid $WRITER_PID) at ${KILL_FROM_MS}..${KILL_UNTIL_MS}ms"

# Wait for the workers to ride through it.
for i in $(seq 1 "$CLIENTS"); do
  pid="${PIDS[$(( CLIENTS + i ))]:-}"
  [[ -n "$pid" ]] && wait "$pid" 2>/dev/null
done
sleep 1

# ----------------------------------------------------------------- verdict ---
say "verdict"

# How the takeover is judged, and why NOT by probing /health:
#
# The role moves to one of the CLIENT processes (cinder-mcp), which has no HTTP
# server at all — /health is a property of cinder-server, so it answers "no"
# even when the takeover worked perfectly. Measured the wrong way first: the
# first run reported a 47s "takeover" and FAILED while all 527 phase-B writes
# had succeeded. The caller-visible fact is the one to assert: WRITES KEEP
# SUCCEEDING across the writer's death.
#
# What that proves: the lock is released by process death (no cleanup code runs
# on SIGKILL), a client re-elects itself, and its queued writes land. What it
# does NOT measure is a latency, because the takeover is transparent to the
# caller — the writes never fail, so there is nothing to time. ADR-0058's
# "<10s recovery" is pinned by the unit test TestWriterDeathTriggersTakeover,
# not here.
PHASE_B_ATTEMPTS=0
PHASE_B_OK=0
PHASE_B_AMBIGUOUS=0
for f in "$REPORTS"/worker-b-*.json; do
  [[ -f "$f" ]] || continue
  read -r a o amb < <(node -e '
    const s = require(process.argv[1]);
    const from = Number(process.argv[2]);
    const until = Number(process.argv[3]);
    // A failure whose window OVERLAPS the kill bracket was in flight when the
    // writer died. Its verdict is unknown by construction, and ADR-0058 says
    // such a write is refused, never replayed — so it is NOT a lost write.
    // Everything else is a real failure.
    //
    // The bracket, not a point: the client learns of the death when its
    // in-flight request comes back as EOF, which is necessarily AFTER the
    // kill. A point test would put the failure outside the window and call a
    // correct refusal a lost write.
    const ambiguous = (s.failureWindows ?? []).filter(
      w => w.startedAt <= until && w.endedAt >= from).length;
    console.log([s.attempts, s.ok, ambiguous].join(" "));
  ' "$f" "$KILL_FROM_MS" "$KILL_UNTIL_MS")
  PHASE_B_ATTEMPTS=$(( PHASE_B_ATTEMPTS + a ))
  PHASE_B_OK=$(( PHASE_B_OK + o ))
  PHASE_B_AMBIGUOUS=$(( PHASE_B_AMBIGUOUS + amb ))
done
# The criterion: every attempt either landed, or was REFUSED with an honest
# "unknown verdict". Nothing may vanish silently — a write that is neither
# landed nor refused is a write the contract dropped on the floor.
# The earlier form of this gate (ok == attempts) turned red on correct
# behaviour: the one write that was in flight when the writer was SIGKILLed is
# SUPPOSED to be refused, and counting it as "did not land" made a correct run
# fail (measured 471/472 on a run where every other write landed).
TAKEOVER_OK=0
(( PHASE_B_ATTEMPTS > 0 && PHASE_B_OK + PHASE_B_AMBIGUOUS == PHASE_B_ATTEMPTS )) && TAKEOVER_OK=1
# One write per client can be in flight at the kill instant (each worker drives
# one call at a time). More than that would mean failures are NOT explained by
# the kill — i.e. something else is broken.
PHASE_B_AMBIGUOUS_OK=0
(( PHASE_B_AMBIGUOUS <= CLIENTS )) && PHASE_B_AMBIGUOUS_OK=1

TOTAL_ATTEMPTS=0
TOTAL_OK=0
TOTAL_FAILED=0
LOCK_BUBBLES=0
SLOWEST=0
for f in "$REPORTS"/worker-*.json; do
  [[ -f "$f" ]] || continue
  read -r a o fl lb sl < <(node -e '
    const s = require(process.argv[1]);
    console.log([s.attempts, s.ok, s.failed, s.lockBubbles, s.slowestMs].join(" "));
  ' "$f")
  TOTAL_ATTEMPTS=$(( TOTAL_ATTEMPTS + a ))
  TOTAL_OK=$(( TOTAL_OK + o ))
  TOTAL_FAILED=$(( TOTAL_FAILED + fl ))
  LOCK_BUBBLES=$(( LOCK_BUBBLES + lb ))
  (( sl > SLOWEST )) && SLOWEST=$sl
done

# Phase A is the part where the writer never died: EVERY write there must
# succeed. The lock-bubble and writer_unavailable counters are two specific
# symptoms; this is the general one, so a failure of any other shape (an
# internal error, a refused write) is a red flag too — previously it was
# counted in the totals and never gated.
PHASE_A_FAILED=0
for f in "$REPORTS"/worker-a-*.json; do
  [[ -f "$f" ]] || continue
  fl=$(node -e 'const s = require(process.argv[1]); console.log(s.failed ?? 0);' "$f")
  PHASE_A_FAILED=$(( PHASE_A_FAILED + fl ))
done

# The backup long-hold scenario, ASSERTED instead of assumed.
#
# "A backup ran" is not the claim the contract makes. The claim is "a write
# QUEUED behind the backup instead of failing".
#
# The long-hold gate used to be a threshold argument: a write was "queued
# behind a backup" if its window overlapped the backup's window and it was slow
# enough. That was a guess — the window includes the tail (verify, rotate, the
# HTTP round trip) and the "slow enough" bar was picked from one run's numbers,
# so it both missed the writes it was looking for and could turn red on a
# correct run.
#
# The proof needs no threshold. The backup's snapshot file is stamped with the
# moment its VACUUM finished writing, and that is the moment the writer's one
# connection came back. A write that was still in flight at that instant and
# came back after it could not have been served — it waited. So:
#
#   queued behind a backup  <=>  started before the snapshot's timestamp
#                                AND ended after it
#
# The only number left to calibrate is what counts as a hold worth reporting:
# a hold that outlasted a normal write of this run. LONG_BACKUP_FLOOR_MS is
# the floor, and the run's own mean write latency raises it when the machine
# is slower than that.
LONG_BACKUP_FLOOR_MS="${LONG_BACKUP_FLOOR_MS:-10}"
read -r BACKUP_HOLD_MS BACKUP_LONGEST_MS LONG_HOLDS QUEUED_WRITES SLOW_WRITES MEAN_WRITE_MS LONG_BACKUP_MS LONGEST_WAIT_MS <<<"$(node -e '
  const fs = require("node:fs");
  const path = require("node:path");
  const dir = process.argv[1];
  const floor = Number(process.argv[2]);
  const backups = JSON.parse(fs.readFileSync(path.join(dir, "backups.json"), "utf8"));
  const files = fs.readdirSync(dir).filter(f => /^worker-a-.*\.json$/.test(f));
  const windows = [];
  let totalMs = 0, attempts = 0, slow = 0;
  for (const f of files) {
    const s = JSON.parse(fs.readFileSync(path.join(dir, f), "utf8"));
    for (const w of (s.writeWindows ?? [])) windows.push(w);
    totalMs += s.totalWriteMs ?? 0;
    attempts += s.attempts ?? 0;
    slow += s.slowWriteCount ?? 0;
  }
  const mean = attempts > 0 ? totalMs / attempts : 0;
  const threshold = Math.max(floor, Math.round(mean));
  let queued = 0, holds = 0, longestWait = 0, totalHold = 0, longestHold = 0;
  for (const b of backups) {
    if (!b.holdEnd) continue;
    // The hold itself: from the request going out to the VACUUM finishing.
    const hold = b.holdEnd - b.start;
    totalHold += hold;
    if (hold > longestHold) longestHold = hold;
    let caught = 0;
    for (const w of windows) {
      if (w[0] <= b.holdEnd && w[1] >= b.holdEnd) {
        caught++;
        const wait = b.holdEnd - w[0];
        if (wait > longestWait) longestWait = wait;
      }
    }
    queued += caught;
    if (caught > 0) holds++;
  }
  console.log([totalHold, longestHold, holds, queued, slow,
               Math.round(mean), threshold, longestWait].join(" "));
' "$REPORTS" "$LONG_BACKUP_FLOOR_MS")"
if [[ -z "${BACKUP_HOLD_MS:-}" ]]; then
  echo "FAIL: could not read the backup windows back from the reports — the long-hold check did not run" >&2
  BACKUP_HOLD_MS=0; BACKUP_LONGEST_MS=0; LONG_HOLDS=0; QUEUED_WRITES=0
  SLOW_WRITES=0; MEAN_WRITE_MS=0; LONG_BACKUP_MS=0; LONGEST_WAIT_MS=0
fi

note "duration:               $(( TOTAL_SECONDS / 60 ))m (phase A ${PHASE_A_SECONDS}s / phase B ${PHASE_B_SECONDS}s)"
note "write attempts:         $TOTAL_ATTEMPTS"
note "succeeded:              $TOTAL_OK"
note "failed:                 $TOTAL_FAILED"
note "\"database is locked\" reaching the caller: $LOCK_BUBBLES   <- must be 0"
note "slowest single write:   ${SLOWEST}ms"
note "backups taken:          $BACKUPS (real VACUUMs: $REAL_BACKUPS)"
note "backup long hold:       ${BACKUP_HOLD_MS}ms total, longest ${BACKUP_LONGEST_MS}ms, ${LONG_HOLDS} hold(s) caught a write   <- ${QUEUED_WRITES} write(s) proven queued behind one (longest wait ${LONGEST_WAIT_MS}ms; a hold counts as long at >= ${LONG_BACKUP_MS}ms, a normal write takes ${MEAN_WRITE_MS}ms; ${SLOW_WRITES} slow)"
note "writes across the SIGKILL: ${PHASE_B_OK} landed + ${PHASE_B_AMBIGUOUS} refused as ambiguous / ${PHASE_B_ATTEMPTS}   <- every attempt must be accounted for"

FAILED=0
if (( LOCK_BUBBLES > 0 )); then
  echo "FAIL: the contract failed at its one job — a lock error reached the caller" >&2
  FAILED=1
fi
if (( TAKEOVER_OK == 0 )); then
  echo "FAIL: writes across the SIGKILL were not all accounted for (${PHASE_B_OK} landed + ${PHASE_B_AMBIGUOUS} ambiguous / ${PHASE_B_ATTEMPTS}) — a write failed for a reason the kill does not explain" >&2
  FAILED=1
fi
if (( PHASE_B_AMBIGUOUS_OK == 0 )); then
  echo "FAIL: ${PHASE_B_AMBIGUOUS} ambiguous writes around the SIGKILL, but only ${CLIENTS} clients can have one write in flight each — the failures are not explained by the kill" >&2
  FAILED=1
fi
if (( PHASE_A_FAILED > 0 )); then
  echo "FAIL: ${PHASE_A_FAILED} write(s) failed in phase A, where the writer was alive the whole time" >&2
  FAILED=1
fi
if (( TOTAL_ATTEMPTS == 0 )); then
  echo "FAIL: no writes were attempted — the soak proved nothing" >&2
  FAILED=1
fi
if (( PHASE_A_VIOLATION == 1 )); then
  echo "FAIL: a phase-A worker produced no report" >&2
  FAILED=1
fi
if (( REAL_BACKUPS == 0 )); then
  echo "FAIL: no real VACUUM ran — the backup long-hold scenario was never created, so the busy path stayed untested" >&2
  FAILED=1
fi
if (( QUEUED_WRITES == 0 )); then
  echo "FAIL: ${BACKUPS} backup(s) ran (${REAL_BACKUPS} real) but no write was ever proven to have queued behind one — the backup is no longer holding the connection the writes need" >&2
  FAILED=1
fi
if (( QUEUED_WRITES >= 1 && LONGEST_WAIT_MS < LONG_BACKUP_MS )); then
  echo "FAIL: the longest proven wait behind a backup was ${LONGEST_WAIT_MS}ms, below the ${LONG_BACKUP_MS}ms a hold has to reach to matter — the busy path was touched but not exercised" >&2
  FAILED=1
fi

# Sample any failure that was classified as writer_unavailable while the writer
# was alive in phase A: that is the re-election path firing against a healthy
# writer, which the contract says must not happen.
FALSE_UNAVAILABLE=0
for f in "$REPORTS"/worker-a-*.json; do
  [[ -f "$f" ]] || continue
  n=$(node -e 'const s=require(process.argv[1]); console.log(s.falseUnavailable||0);' "$f")
  FALSE_UNAVAILABLE=$(( FALSE_UNAVAILABLE + n ))
done
note "writer_unavailable against a live writer (phase A): $FALSE_UNAVAILABLE   <- must be 0"
if (( FALSE_UNAVAILABLE > 0 )); then
  echo "FAIL: writer_unavailable was reported while the writer was alive" >&2
  FAILED=1
fi

# Always keep the reports next to the summary: a failing run has to explain
# itself without a re-run.
if [[ "$FAILED" == "0" ]]; then
  say "PASS — every ADR-0058 criterion held for ${TOTAL_SECONDS}s"
  exit 0
fi

echo
echo "failure samples:" >&2
for f in "$REPORTS"/worker-*.json; do
  [[ -f "$f" ]] || continue
  node -e '
    const s = require(process.argv[1]);
    if (!s.failures?.length) process.exit(0);
    console.error(`  ${process.argv[1]}:`);
    for (const f of s.failures.slice(0, 5)) console.error(`    ${f.tool} [${f.code}] ${f.elapsed}ms — ${f.message}`);
  ' "$f"
done
echo "full reports: $REPORTS" >&2
KEEP=1
exit 1
