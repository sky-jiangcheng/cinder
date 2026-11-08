#!/bin/bash
# Start the browser-mode dev stack: headless API + Vite dev server.
#
# Why this exists: running `npx vite` alone gives you a page that renders but
# 502s on every /api/rpc call. Vite proxies /api to the headless server (see
# vite.config.ts, default 18765), and if that process is not up the proxy has
# nothing to talk to — the HTML loads fine and only the data calls fail, which
# reads as "the app is broken" rather than "a backend is missing".
#
# This script starts BOTH and fails loudly if either dies, so the next person
# does not lose time bisecting a 502.
#
# Usage:
#   bash scripts/dev.sh                 # API on 18765, Vite on 5173
#   bash scripts/dev.sh --api 19000     # custom API port (vite proxy must match)
#   bash scripts/dev.sh --no-api        # Vite only (API already running elsewhere)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

API_PORT=18765
WITH_API=1
while [ $# -gt 0 ]; do
  case "$1" in
    --api) API_PORT="${2:?--api needs a port}"; shift 2 ;;
    --no-api) WITH_API=0; shift ;;
    -h|--help) sed -n '2,16p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

# `go` is not on PATH in every shell this runs from.
if ! command -v go >/dev/null 2>&1; then
  for candidate in /opt/homebrew/bin /usr/local/go/bin; do
    [ -x "$candidate/go" ] && export PATH="$candidate:$PATH" && break
  done
fi

if lsof -nP -iTCP:"$API_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "port $API_PORT already in use — reusing whatever is listening there."
  echo "  (if it is an old build, restart it: pkill -f 'cinder-server --port $API_PORT')"
  WITH_API=0
fi

API_PID=""
cleanup() {
  [ -n "$API_PID" ] && kill "$API_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

if [ "$WITH_API" = 1 ]; then
  echo "building headless API..."
  cd "$PROJECT_ROOT"
  go build -o cinder-server ./cmd/server
  echo "starting API on 127.0.0.1:$API_PORT ..."
  ./cinder-server --port "$API_PORT" &
  API_PID=$!
  # Wait for /health instead of a fixed sleep — a slow first build would
  # otherwise race the check and report a false failure.
  for _ in $(seq 1 40); do
    if curl -sf --noproxy '*' "http://127.0.0.1:$API_PORT/health" >/dev/null 2>&1; then
      echo "API ready: $(curl -s --noproxy '*' "http://127.0.0.1:$API_PORT/health")"
      break
    fi
    sleep 0.25
  done
  if ! curl -sf --noproxy '*' "http://127.0.0.1:$API_PORT/health" >/dev/null 2>&1; then
    echo "ERROR: API did not come up on port $API_PORT" >&2
    exit 1
  fi
fi

cd "$PROJECT_ROOT/web"
echo "starting Vite on http://localhost:5173 ..."
echo "  (Ctrl-C stops both)"
exec npx vite
