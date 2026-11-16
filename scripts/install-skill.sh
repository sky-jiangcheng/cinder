#!/usr/bin/env bash
# Install the Cinder skill package into EVERY agent on this machine.
#
# The skill is app-agnostic: skill/repotory/ is the same SKILL.md +
# references + query script for every agent, and each agent discovers skills
# by scanning its own user-level skills directory. So "install once, every
# agent sees it" means: copy the package into each DETECTED agent's skills
# directory — one command, no per-app configuration, no second copy to keep
# in sync by hand. (Same model as arkcli's connect: detect the agents,
# install into each.)
#
# The agent list is scripts/skill-agents.txt — the same table
# scripts/check-skill-package.mjs reads, so the drift check covers every
# location this script writes to. Add an agent by adding one line there.
#
# Usage:
#   bash scripts/install-skill.sh            # install into every detected agent
#   bash scripts/install-skill.sh --list     # show detected agents and status
#   bash scripts/install-skill.sh uninstall  # remove from every installed agent
#
# Exit codes: 0 success, 1 nothing installed / usage error.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILL_NAME="repotory"

# Two layouts, resolved by where the package sits relative to this script:
#   - repo checkout:    the script lives in scripts/, the package at skill/repotory
#   - unpacked release: the script sits at the archive root, next to repotory/
#                       (the archive is built from skill/, so its single
#                       top-level directory is the skill name)
if [[ -f "$SCRIPT_DIR/repotory/SKILL.md" ]]; then
  ROOT="$SCRIPT_DIR"
  SKILL_SRC="$SCRIPT_DIR/repotory"
  AGENT_TABLE="$SCRIPT_DIR/skill-agents.txt"
elif [[ -f "$SCRIPT_DIR/../skill/repotory/SKILL.md" ]]; then
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
  SKILL_SRC="$ROOT/skill/repotory"
  AGENT_TABLE="$ROOT/scripts/skill-agents.txt"
else
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
  SKILL_SRC=""
  AGENT_TABLE="$ROOT/scripts/skill-agents.txt"
fi

say() { printf '\n=== %s ===\n' "$*"; }

if [[ -z "$SKILL_SRC" || ! -f "$SKILL_SRC/SKILL.md" ]]; then
  echo "error: skill package not found — expected skill/repotory/SKILL.md (repo checkout)" >&2
  echo "       or repotory/SKILL.md (an unpacked release archive), relative to:" >&2
  echo "       $ROOT" >&2
  exit 1
fi
if [[ ! -f "$AGENT_TABLE" ]]; then
  echo "error: agent table not found at $AGENT_TABLE" >&2
  exit 1
fi

# Install one agent's copy. Idempotent: re-running overwrites the installed
# copy with the repo copy, which is exactly the "fix the drift" path.
install_one() {
  local agent="$1" rel="$2"
  local dest="$HOME/$rel/$SKILL_NAME"
  mkdir -p "$(dirname "$dest")"
  rm -rf "$dest"
  cp -R "$SKILL_SRC" "$dest"
  printf '  installed  %-10s -> %s\n' "$agent" "$dest"
}

# Remove one agent's copy — but only if it is OUR skill. The guard compares
# the installed SKILL.md's `upstream` value against the repo copy's own: a
# copy that declares the same upstream is one this script installed, and a
# user's own skill (different upstream, or no upstream at all) at the same
# path is never deleted. grep -qE is per-line, which is what a frontmatter
# check needs (a bash [[ =~ ]] anchors against the whole string).
#
# The expected value is read from the repo copy at runtime, so the guard
# follows the package's own declaration instead of a hard-coded URL.
EXPECTED_UPSTREAM="$(grep -E '^[[:space:]]*upstream:' "$SKILL_SRC/SKILL.md" | head -1 | sed -E 's/^[[:space:]]*upstream:[[:space:]]*//; s/^["'"'"']//; s/["'"'"']$//')"

uninstall_one() {
  local agent="$1" rel="$2"
  local dest="$HOME/$rel/$SKILL_NAME"
  if [[ -f "$dest/SKILL.md" ]] &&
    grep -qE '^name:[[:space:]]*'"$SKILL_NAME"'[[:space:]]*$' "$dest/SKILL.md" &&
    grep -qF "$EXPECTED_UPSTREAM" "$dest/SKILL.md"; then
    rm -rf "$dest"
    printf '  removed    %-10s <- %s\n' "$agent" "$dest"
  else
    printf '  skipped    %-10s (not a Cinder skill install: %s)\n' "$agent" "$dest"
  fi
}

case "${1:-install}" in
  --list|list)
    say "detected agents (from $AGENT_TABLE)"
    while IFS=$'\t' read -r agent rel; do
      [[ -z "$agent" || "$agent" == \#* ]] && continue
      if [[ -d "$HOME/$rel/$SKILL_NAME" ]]; then
        printf '  %-10s installed  %s\n' "$agent" "$HOME/$rel/$SKILL_NAME"
      elif [[ -d "$HOME/$rel" ]]; then
        printf '  %-10s detected   skill not installed yet (%s)\n' "$agent" "$HOME/$rel"
      else
        printf '  %-10s not found  (%s does not exist)\n' "$agent" "$HOME/$rel"
      fi
    done < "$AGENT_TABLE"
    ;;
  uninstall)
    say "uninstalling the Cinder skill from every agent"
    found=0
    while IFS=$'\t' read -r agent rel; do
      [[ -z "$agent" || "$agent" == \#* ]] && continue
      if [[ -d "$HOME/$rel/$SKILL_NAME" ]]; then
        found=1
        uninstall_one "$agent" "$rel"
      fi
    done < "$AGENT_TABLE"
    if [[ "$found" -eq 0 ]]; then
      echo "nothing installed"
    fi
    ;;
  install|--install)
    say "installing the Cinder skill into every detected agent"
    found=0
    while IFS=$'\t' read -r agent rel; do
      [[ -z "$agent" || "$agent" == \#* ]] && continue
      # Detection: the agent's skills directory exists on this machine.
      if [[ -d "$HOME/$rel" ]]; then
        found=1
        install_one "$agent" "$rel"
      else
        printf '  skipped    %-10s (%s does not exist — install that agent first)\n' "$agent" "$HOME/$rel"
      fi
    done < "$AGENT_TABLE"
    if [[ "$found" -eq 0 ]]; then
      echo "no supported agent found on this machine." >&2
      echo "Supported agents and their skills directories:" >&2
      while IFS=$'\t' read -r agent rel; do
        [[ -z "$agent" || "$agent" == \#* ]] && continue
        printf '  %-10s %s\n' "$agent" "$HOME/$rel" >&2
      done < "$AGENT_TABLE"
      exit 1
    fi
    say "done"
    echo "Every detected agent now discovers the Cinder skill automatically."
    echo "Re-run this script after upgrading Cinder to refresh the installed copies."
    ;;
  *)
    echo "usage: bash scripts/install-skill.sh [--list | uninstall]" >&2
    exit 1
    ;;
esac
