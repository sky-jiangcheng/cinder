#!/usr/bin/env bash
# Guard the package-manager manifests BEFORE they are pushed to the tap and
# the bucket.
#
# Why this file exists. The tap's own CI caught a real defect on 2026-10-26:
# the in-repo cask carried a `license "MIT"` stanza, and `license` is a
# FORMULA method — `brew install --cask` rejects the whole definition with
# "undefined method 'license' for Cask". The in-repo manifests had never been
# install-tested, because the tap's CI only ever exercised the hand-maintained
# `reponest` cask; the manifests under packaging/ were updated in this
# repository and then went nowhere (the copy that packaging/README.md
# described did not exist until scripts/sync-packaging.sh was added).
#
# So the defect reached the tap and made the tap red, instead of failing
# here. That is the wrong order: this repository is where the manifests are
# authored, so it is where they should be checked.
#
# Two checks, both runnable on Linux (no Homebrew needed):
#
#   1. Every Scoop manifest is valid JSON with the fields Scoop requires.
#      A JSON manifest with a `#` comment line is unparseable, and the bucket
#      rejects it — that happened too, with the alias manifests.
#
#   2. Every Homebrew Cask uses only Cask DSL. The Formula-only methods this
#      guard knows about are listed in FORMULA_ONLY_METHODS; the list is not
#      exhaustive, it is the set that has actually bitten.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail=0

# The manifests are discovered per namespace: every packaging/<project>/…
# manifest is checked, so a new project is covered without editing this file.

# --- 1. Scoop manifests must be valid JSON ----------------------------------
for f in packaging/*/scoop/*.json; do
  [ -e "$f" ] || continue
  if ! out="$(python3 -m json.tool "$f" > /dev/null 2>&1)"; then
    echo "ERROR: $f is not valid JSON:"
    python3 -m json.tool "$f" 2>&1 | tail -3 || true
    echo "       (JSON has no comment syntax — a '#' line makes the manifest unparseable)"
    fail=1
    continue
  fi
  # The fields Scoop's own schema requires for a usable manifest.
  if ! python3 - "$f" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
required = ["version", "description", "homepage", "license", "url", "hash", "bin"]
missing = [k for k in required if not m.get(k)]
if missing:
    sys.exit(f"{sys.argv[1]}: missing required field(s): {', '.join(missing)}")
PY
  then
    fail=1
  fi
done

# --- 2. Cask manifests must use Cask DSL only -------------------------------
# `license` is a Formula method; `test` is not a Cask DSL method in current
# Homebrew (it is absent from Cask::DSL::DSL_METHODS); `depends_on` and
# `head` are Formula-only too.
FORMULA_ONLY_METHODS="license|test|depends_on|head|keg_only|deprecated"

for f in packaging/*/homebrew/Casks/*.rb; do
  [ -e "$f" ] || continue
  # Match a DSL call at the start of a line, indented — not a mention inside a
  # comment. A comment mentioning "license" must not trip the guard.
  if grep -nE "^[[:space:]]+(${FORMULA_ONLY_METHODS})[[:space:]]" "$f" | grep -vE "^[[:space:]]*[0-9]+:[[:space:]]*#" > /tmp/cask-dsl-hit.$$; then
    echo "ERROR: $f uses a Formula-only Homebrew method:"
    sed 's/^/       /' /tmp/cask-dsl-hit.$$
    echo "       A Cask is rejected outright by 'brew install --cask' when it uses one."
    fail=1
  fi
  rm -f /tmp/cask-dsl-hit.$$
done

# Ruby syntax: cheap, and it catches a half-finished edit. Both casks and
# formulas are Ruby, so both are checked.
for f in packaging/*/homebrew/Casks/*.rb packaging/*/homebrew/Formula/*.rb; do
  [ -e "$f" ] || continue
  if command -v ruby > /dev/null 2>&1; then
    if ! ruby -c "$f" > /dev/null 2>&1; then
      echo "ERROR: $f is not valid Ruby:"
      ruby -c "$f" 2>&1 | sed 's/^/       /'
      fail=1
    fi
  fi
done

if [ "$fail" -ne 0 ]; then
  echo
  echo "packaging manifests are invalid — fix them here before scripts/sync-packaging.sh pushes them."
  exit 1
fi

scoop_count=$(find packaging -type f -path '*/scoop/*.json' | wc -l | tr -d ' ')
rb_count=$(find packaging -type f -path '*/homebrew/*/*.rb' | wc -l | tr -d ' ')
echo "packaging manifests OK (${scoop_count} scoop, ${rb_count} homebrew)"
