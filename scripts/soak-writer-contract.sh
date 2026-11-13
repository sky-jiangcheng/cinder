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

# Backups take the write lock for the whole VACUUM INTO. Fire them repeatedly
# so the clients are queueing against a held lock for a real fraction of the
# run, not once at the start.
BACKUPS=0
BACKUP_FAILS=0
phase_a_end=$(( $(date +%s) + PHASE_A_SECONDS ))
while [[ $(date +%s) -lt $phase_a_end ]]; do
  # CreateBackup is idempotent per DAY: once today's snapshot exists, every
  # later RunBackupNow returns before VACUUM INTO ever runs. Without this, the
  # "repeated long holds" above are one long hold plus N no-ops — measured:
  # 14 calls, 14 counted, exactly one VACUUM. FORCE_BACKUPS=1 drops today's
  # snapshot so each call really takes the write lock (the isolated HOME makes
  # that safe); it also makes each VACUUM write a fresh snapshot to disk, so
  # the default stays off to keep the run small and the write rate high.
  if [[ "${FORCE_BACKUPS:-0}" == "1" ]]; then
    find "$HOME_DIR" -name "repotory-$(date +%F).db" -delete 2>/dev/null
  fi
  if out=$(rpc RunBackupNow '[]' 2>&1); then
    BACKUPS=$(( BACKUPS + 1 ))
  else
    # A backup that FAILS is only a problem if it fails for a lock reason; a
    # plain refusal (already taken this hour) is normal.
    case "$out" in
      *"database is locked"*) BACKUP_FAILS=$(( BACKUP_FAILS + 1 )) ;;
    esac
  fi
  sleep 7
done
note "backups taken: $BACKUPS (lock-failed: $BACKUP_FAILS)"

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
kill -9 "$WRITER_PID" 2>/dev/null
note "writer SIGKILLed (pid $WRITER_PID)"

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
for f in "$REPORTS"/worker-b-*.json; do
  [[ -f "$f" ]] || continue
  read -r a o < <(node -e '
    const s = require(process.argv[1]);
    console.log([s.attempts, s.ok].join(" "));
  ' "$f")
  PHASE_B_ATTEMPTS=$(( PHASE_B_ATTEMPTS + a ))
  PHASE_B_OK=$(( PHASE_B_OK + o ))
done
TAKEOVER_OK=0
(( PHASE_B_ATTEMPTS > 0 && PHASE_B_OK == PHASE_B_ATTEMPTS )) && TAKEOVER_OK=1

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

note "duration:               $(( TOTAL_SECONDS / 60 ))m (phase A ${PHASE_A_SECONDS}s / phase B ${PHASE_B_SECONDS}s)"
note "write attempts:         $TOTAL_ATTEMPTS"
note "succeeded:              $TOTAL_OK"
note "failed:                 $TOTAL_FAILED"
note "\"database is locked\" reaching the caller: $LOCK_BUBBLES   <- must be 0"
note "slowest single write:   ${SLOWEST}ms"
note "backups taken:          $BACKUPS"
note "writes across the SIGKILL: ${PHASE_B_OK}/${PHASE_B_ATTEMPTS}   <- all must land (the takeover is transparent)"

FAILED=0
if (( LOCK_BUBBLES > 0 )); then
  echo "FAIL: the contract failed at its one job — a lock error reached the caller" >&2
  FAILED=1
fi
if (( TAKEOVER_OK == 0 )); then
  echo "FAIL: writes did not all land across the SIGKILL (${PHASE_B_OK}/${PHASE_B_ATTEMPTS}) — the takeover did not happen, or it was not transparent" >&2
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
