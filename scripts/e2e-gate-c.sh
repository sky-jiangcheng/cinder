#!/usr/bin/env bash
# Gate C (ADR-0059): the three panels' journeys against REAL binaries.
#
# What this proves that the unit suites cannot: the panel's own conversation
# model, through its own transport, reaches a real `cinder-mcp` (stdio) and a
# real `cinder-server` (HTTP) and completes search → in-place read → ask. No IDE,
# no webview, no display — the models were built behind a Transport port for
# exactly this reason (ADR-0036), so the gate runs on a headless CI runner.
#
#   bash scripts/e2e-gate-c.sh            # both IDEs
#   bash scripts/e2e-gate-c.sh vscode     # one of them
#
# Requirements: Go toolchain (builds the binaries), Node + the vscode
# extension's build output, JDK 21 + gradle for the JetBrains half, and a
# directory to use as HOME (`CINDER_E2E_HOME`, defaulted to a temp dir so the
# gate never touches the user's real data directory).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WHICH="${1:-both}"
BIN_DIR="${CINDER_E2E_BIN:-$(mktemp -d)}"
export CINDER_E2E_HOME="${CINDER_E2E_HOME:-$(mktemp -d)}"

say() { printf '\n=== %s ===\n' "$*"; }

say "building the real binaries"
mkdir -p "$BIN_DIR"
(cd "$ROOT" && GOCACHE="${GOCACHE:-$ROOT/.gocache}" go build -o "$BIN_DIR/cinder-mcp" ./cmd/mcp)
(cd "$ROOT" && GOCACHE="${GOCACHE:-$ROOT/.gocache}" go build -o "$BIN_DIR/cinder-server" ./cmd/server)
export CINDER_E2E_MCP="$BIN_DIR/cinder-mcp"
export CINDER_E2E_SERVER="$BIN_DIR/cinder-server"
echo "mcp:    $CINDER_E2E_MCP"
echo "server: $CINDER_E2E_SERVER"
echo "home:   $CINDER_E2E_HOME  (never the user's real data directory)"

run_vscode() {
  say "VS Code panel: search → in-place read → ask"
  (cd "$ROOT/ide/vscode" && npm run compile --silent)
  (cd "$ROOT/ide/vscode" && node --test out/__tests__/e2e-gate-c.e2e.js)
}

run_jetbrains() {
  say "JetBrains panel: search → in-place read → ask"
  # Gradle PROPERTIES, not exported variables: the daemon keeps the environment
  # it was started with, so `export` is invisible to the build (proven — the
  # gate silently skipped itself). Properties are configuration, not ambient
  # state, and they also invalidate the configuration cache when they change.
  (cd "$ROOT/ide/jetbrains" && gradle test \
    --tests 'dev.repotory.jetbrains.E2eGateCTest' \
    -Pcinder.e2e.mcp="$CINDER_E2E_MCP" \
    -Pcinder.e2e.server="$CINDER_E2E_SERVER" \
    -Pcinder.e2e.home="$CINDER_E2E_HOME" \
    --console=plain)
}

case "$WHICH" in
  vscode) run_vscode ;;
  jetbrains) run_jetbrains ;;
  both) run_vscode; run_jetbrains ;;
  *) echo "usage: $0 [vscode|jetbrains|both]" >&2; exit 2 ;;
esac

say "gate C passed ($WHICH)"
