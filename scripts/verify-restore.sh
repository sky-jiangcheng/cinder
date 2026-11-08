#!/usr/bin/env bash
# scripts/verify-restore.sh — one command that answers the only question a
# backup exists for: "if the live database dies, will this snapshot bring it
# back?"
#
# Why this exists. ADR-0025 requires a REAL restore drill on a real machine
# before the backup feature can be called done, and until now that drill was
# performed by hand: copy a snapshot, spin up a headless server against it,
# poke at it, tear it down. Every rehearsal cost a person an afternoon and
# left nothing behind that could be repeated. A backup nobody has ever
# restored is a hypothesis, not a backup — and the hypothesis that breaks
# first is the boring one: the server starts, but the FTS index is empty, or
# the page count is zero, or the data dir resolution quietly points at the
# LIVE database instead of the copy.
#
# So the drill becomes a script:
#
#   1. pick a snapshot (the one given, or the newest in the backup dir);
#   2. record its sha256;
#   3. COPY it into an isolated home directory;
#   4. check the copy's integrity (sqlite quick_check, read-only);
#   5. start a headless server against that copy, with HOME pointing at the
#      isolated directory;
#   6. ask it for /health, the note count, the approved-page count, and run
#      one full-text search for a term taken FROM the snapshot itself;
#   7. stop the server, drop the isolated home, and re-read the snapshot's
#      sha256 to prove that nothing wrote to it.
#
# The server is built from THIS checkout when Go is available, so the drill
# exercises the code under test and not whatever release happens to be
# installed. A stale installed binary can fail a drill for reasons that have
# nothing to do with the snapshot — that is how a good snapshot gets
# condemned. Pass --server to pin a specific binary instead.
#
# The discipline (ADR-0025 promotion condition 1): the live database is never
# opened. The server runs with an isolated HOME, so the data dir it resolves
# is inside the temp directory — even a bug in the server cannot reach the
# real data dir. The snapshot is copied, never moved; the copy is opened
# read-only, the snapshot file itself is only ever hashed.
#
# Usage:
#   scripts/verify-restore.sh                       # newest snapshot
#   scripts/verify-restore.sh /path/to/snapshot.db  # a specific one
#   scripts/verify-restore.sh --query "登录"         # a specific search term
#
# Exit codes: 0 = the snapshot can bring the knowledge base back,
#             1 = it cannot (or the drill could not be run at all).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

SNAPSHOT=""
QUERY=""
PORT=""
SERVER_BIN=""
KEEP=0

usage() {
  sed -n '2,/^set -euo/{
    /^set -euo/d
    s/^# \{0,1\}//
    p
  }' "${BASH_SOURCE[0]}"
  cat <<'EOF'

Options:
  --snapshot PATH   the snapshot file to verify (default: newest in the backup dir)
  --query TEXT      the full-text query to run (default: a term taken from the snapshot)
  --port PORT       the port for the isolated server (default: a free one)
  --server PATH     the headless server binary (default: built from this checkout,
                    else cinder-server on PATH)
  --keep            keep the temporary directory for inspection
  -h, --help        this text
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --snapshot) SNAPSHOT="${2:-}"; shift 2 ;;
    --query) QUERY="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --server) SERVER_BIN="${2:-}"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    *)
      # A bare path is the snapshot to verify — the usage text promises it.
      [ -z "$SNAPSHOT" ] || { echo "only one snapshot may be given" >&2; usage >&2; exit 2; }
      SNAPSHOT="$1"; shift ;;
  esac
done

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

command -v python3 >/dev/null 2>&1 || fail "python3 is required (it parses the server's JSON)"

# --- where the live data lives (mirrors platform.GetDbPath) ------------------

# os.UserConfigDir() is $HOME/Library/Application Support on macOS and
# ${XDG_CONFIG_HOME:-$HOME/.config} elsewhere; the data dir is that plus
# "repotory". The same rule is applied to the ISOLATED home below, so the
# server finds the copy where the script put it.
data_dir_for() {
  local home="$1"
  case "$(uname -s)" in
    Darwin) printf '%s/Library/Application Support/repotory' "$home" ;;
    *) printf '%s/repotory' "${XDG_CONFIG_HOME:-$home/.config}" ;;
  esac
}

LIVE_DATA_DIR="$(data_dir_for "$HOME")"

digest() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "no sha256 tool found (sha256sum / shasum)" >&2
    return 1
  fi
}

# --- pick the snapshot ------------------------------------------------------

if [ -z "$SNAPSHOT" ]; then
  SNAPSHOT="$(ls -t "$LIVE_DATA_DIR"/backups/repotory-*.db 2>/dev/null | head -1 || true)"
  [ -n "$SNAPSHOT" ] || fail "no snapshot found in $LIVE_DATA_DIR/backups — pass --snapshot PATH"
fi
[ -f "$SNAPSHOT" ] || fail "no such snapshot: $SNAPSHOT"
SNAPSHOT="$(cd "$(dirname "$SNAPSHOT")" && pwd)/$(basename "$SNAPSHOT")"

echo "== Cinder restore drill =="
echo "snapshot : $SNAPSHOT"
echo "size     : $(wc -c < "$SNAPSHOT" | tr -d ' ') bytes"
echo "written  : $(stat -c %y "$SNAPSHOT" 2>/dev/null || stat -f %Sm "$SNAPSHOT")"
BEFORE_DIGEST="$(digest "$SNAPSHOT")"
echo "sha256   : $BEFORE_DIGEST"
echo

# --- the isolated copy ------------------------------------------------------

TMP="$(mktemp -d "${TMPDIR:-/tmp}/repotory-verify-restore.XXXXXX")"
ISO_HOME="$TMP/home"
mkdir -p "$ISO_HOME"
ISO_DATA_DIR="$(data_dir_for "$ISO_HOME")"
mkdir -p "$ISO_DATA_DIR"
cp "$SNAPSHOT" "$ISO_DATA_DIR/repotory.db"

# The probe talks to the isolated server over HTTP and turns its answers into
# the report. It lives in the temp dir because it only exists for this run.
cat >"$TMP/probe.py" <<'PYEOF'
#!/usr/bin/env python3
"""Probe the isolated headless server started by verify-restore.sh.

Modes:
  check <db-path>           — open the copy read-only and quick_check it
  health <port>             — exit 0 as soon as /health answers
  verify <port> [query]     — run the checks, print the report, exit non-zero
                               when the snapshot cannot do its job
"""
import json
import sqlite3
import sys
import urllib.error
import urllib.parse
import urllib.request

MODE = sys.argv[1] if len(sys.argv) > 1 else "health"
PORT = sys.argv[2] if len(sys.argv) > 2 else "0"
BASE = "http://127.0.0.1:" + PORT


class RpcError(RuntimeError):
    """An RPC that the server refused. status 404 means "no such method"."""

    def __init__(self, message, status=0):
        super().__init__(message)
        self.status = status


def get(path):
    with urllib.request.urlopen(BASE + path, timeout=30) as resp:
        return json.loads(resp.read().decode("utf-8"))


def rpc(method, args):
    body = json.dumps({"method": method, "args": args}).encode("utf-8")
    req = urllib.request.Request(
        BASE + "/api/rpc",
        data=body,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", "replace")
        message = raw
        try:
            message = json.loads(raw).get("error", raw)
        except ValueError:
            pass
        raise RpcError("%s: %s" % (method, message), status=exc.code)
    if isinstance(payload, dict) and payload.get("error"):
        raise RpcError(method + ": " + str(payload["error"]))
    return payload.get("result")


def check_file(path):
    """Open the copy read-only and make sure it is a usable SQLite file.

    A snapshot that is truncated or corrupt fails here rather than halfway
    through the drill, and the message says which file is broken.
    """
    uri = "file:" + urllib.parse.quote(path) + "?mode=ro"
    try:
        conn = sqlite3.connect(uri, uri=True)
    except sqlite3.Error as exc:
        print("integrity: FAILED (%s)" % exc)
        return 1
    try:
        row = conn.execute("PRAGMA quick_check").fetchone()
        verdict = row[0] if row else "no answer"
        if verdict != "ok":
            print("integrity: FAILED (%s)" % verdict)
            return 1
        print("integrity: ok (sqlite quick_check)")
        return 0
    except sqlite3.Error as exc:
        print("integrity: FAILED (%s)" % exc)
        return 1
    finally:
        conn.close()


def pick_query(notes):
    """Take a search term FROM the snapshot, so a hit proves the index works.

    The FTS index is a trigram index: a term shorter than three characters
    cannot match it, so the term has to be long enough.
    """
    for note in notes:
        title = (note.get("title") or "").strip()
        if not title:
            continue
        for token in title.split():
            token = token.strip(".,;:!?()[]{}<>\"'")
            if len(token) >= 3:
                return token[:12]
        # A CJK title has no spaces: take a run of characters instead — the
        # tokenizer indexes it as overlapping trigrams.
        cleaned = "".join(c for c in title if c.isalnum() or ord(c) > 0x2E80)
        if len(cleaned) >= 3:
            return cleaned[:4]
    return ""


def verify():
    problems = []

    health = get("/health")
    print("health : %s (version %s)" % (health.get("status"), health.get("version")))
    if health.get("status") != "ok":
        problems.append("/health answered with status %r" % health.get("status"))

    counts = rpc("GetNoteCounts", []) or []
    notes = sum(int(c.get("count", 0)) for c in counts)
    print("notes  : %d" % notes)
    if notes == 0:
        problems.append("the snapshot holds no notes — there is nothing to bring back")

    # The page count is a bonus, not the point of the drill: a server older
    # than the wiki feature simply does not have the method, and that says
    # nothing about the snapshot.
    try:
        pages = rpc("BrowseWikiPages", [0, 500]) or []
        print("pages  : %d approved page(s)" % len(pages))
    except RpcError as exc:
        if exc.status == 404:
            print("pages  : skipped (this server has no wiki pages: %s)" % exc)
        else:
            raise

    all_notes = rpc("ListAllNotes", []) or []
    query = sys.argv[3] if len(sys.argv) > 3 else ""
    if not query:
        query = pick_query(all_notes)
    if not query:
        print("search : skipped (no note title to take a term from)")
    else:
        hits = rpc("SearchNotes", [query]) or []
        print("search : %r -> %d hit(s)" % (query, len(hits)))
        if not hits:
            problems.append(
                "the full-text index returned nothing for %r — the FTS tables did not survive" % query
            )

    if problems:
        print()
        for p in problems:
            print("problem: " + p)
        return 1
    return 0


if MODE == "check":
    sys.exit(check_file(sys.argv[2]))
if MODE == "health":
    try:
        get("/health")
    except Exception:
        sys.exit(1)
    sys.exit(0)

sys.exit(verify())
PYEOF

SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [ "$KEEP" = "1" ]; then
    echo "kept: $TMP"
  else
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT

# --- the copy has to be a usable SQLite file --------------------------------
# Checked on the COPY (never on the snapshot itself) and read-only, so the
# drill cannot damage what it is verifying. It runs before the server is
# built: a corrupt snapshot is the cheapest failure to report.

if ! python3 "$TMP/probe.py" check "$ISO_DATA_DIR/repotory.db"; then
  fail "the copy of the snapshot is not a usable database"
fi
echo

# --- the server binary ------------------------------------------------------

if [ -z "$SERVER_BIN" ]; then
  # Prefer a server built from THIS checkout: the drill then exercises the
  # code under test (and the current schema) rather than whatever release
  # happens to be installed on the machine.
  if command -v go >/dev/null 2>&1; then
    echo "building the headless server from this checkout..."
    if ( cd "$ROOT" && go build -o "$TMP/cinder-server" ./cmd/server ); then
      SERVER_BIN="$TMP/cinder-server"
    else
      echo "the build failed; falling back to the installed server" >&2
    fi
  fi
  if [ -z "$SERVER_BIN" ] && command -v cinder-server >/dev/null 2>&1; then
    SERVER_BIN="$(command -v cinder-server)"
  fi
  [ -n "$SERVER_BIN" ] || fail "no headless server available (install Go, install cinder-server, or pass --server PATH)"
fi
[ -x "$SERVER_BIN" ] || fail "the server binary is not executable: $SERVER_BIN"
echo "server  : $SERVER_BIN"
echo

if [ -z "$PORT" ]; then
  PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
fi

# --- run it against the copy, never against the live database ---------------

echo "starting an isolated server on 127.0.0.1:$PORT (HOME=$ISO_HOME)"
HOME="$ISO_HOME" XDG_CONFIG_HOME="$ISO_HOME/.config" \
  "$SERVER_BIN" --port "$PORT" >"$TMP/server.log" 2>&1 &
SERVER_PID=$!

READY=0
for _ in $(seq 1 60); do
  if python3 "$TMP/probe.py" health "$PORT" >/dev/null 2>&1; then
    READY=1
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "--- server log ---"
    cat "$TMP/server.log" || true
    fail "the server exited before it answered /health"
  fi
  sleep 0.5
done
[ "$READY" = "1" ] || {
  echo "--- server log ---"
  cat "$TMP/server.log" || true
  fail "the server did not answer /health within 30s"
}

# --- the checks -------------------------------------------------------------
# The probe talks to the server over HTTP and prints the report; it exits
# non-zero when the snapshot cannot do its job.
set +e
python3 "$TMP/probe.py" verify "$PORT" "${QUERY:-}" >"$TMP/report.txt" 2>"$TMP/report.err"
PROBE_STATUS=$?
set -e
cat "$TMP/report.txt"
if [ -s "$TMP/report.err" ]; then
  echo "--- probe stderr ---"
  cat "$TMP/report.err"
fi

# --- prove nothing was written ----------------------------------------------

AFTER_DIGEST="$(digest "$SNAPSHOT")"
echo
if [ "$BEFORE_DIGEST" != "$AFTER_DIGEST" ]; then
  echo "FAIL  the snapshot changed during the drill ($BEFORE_DIGEST -> $AFTER_DIGEST)"
  exit 1
fi
echo "OK    the snapshot is byte-identical before and after the drill"

if [ "$PROBE_STATUS" != "0" ]; then
  echo "FAIL  this snapshot cannot bring the knowledge base back"
  exit 1
fi

echo "PASS  this snapshot can bring the knowledge back"
