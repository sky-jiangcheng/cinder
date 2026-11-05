#!/usr/bin/env bash
# Prune historical GitHub releases.
#
# The repository has accumulated ~40 releases and >1 GB of uploaded assets.
# Older ones are no longer downloadable from anywhere else, so this is
# destructive and irreversible: deleting a release removes its assets
# permanently. Git tags are NOT removed unless you pass --delete-tags.
#
# Safety properties, in order of importance:
#   1. Dry run by default. Nothing is deleted without --yes.
#   2. Fails immediately if gh is missing or unauthenticated, rather than
#      part-way through a delete loop.
#   3. Always keeps the most recent --keep releases, so you cannot prune down
#      to zero by accident.
#   4. Prints exactly what it will delete, and the bytes reclaimed, before
#      asking.
#
# Usage:
#   ./scripts/prune-releases.sh --keep 3            # show what would go
#   ./scripts/prune-releases.sh --keep 3 --yes      # do it
#   ./scripts/prune-releases.sh --keep 3 --yes --delete-tags
#   ./scripts/prune-releases.sh --list              # inventory only
set -euo pipefail

REPO="${REPO:-sky-jiangcheng/cinder}"
KEEP=3
ASSUME_YES=0
DELETE_TAGS=0
LIST_ONLY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP="${2:?--keep needs a number}"; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --delete-tags) DELETE_TAGS=1; shift ;;
    --list) LIST_ONLY=1; shift ;;
    --repo) REPO="${2:?--repo needs owner/name}"; shift 2 ;;
    -h|--help) sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

# Fail before touching anything if the tooling is not usable. Discovering this
# halfway through a delete loop would leave the release list in a partial state.
if ! command -v gh >/dev/null 2>&1; then
  cat >&2 <<'EOF'
ERROR: the GitHub CLI (gh) is not installed.

  macOS / Linux : brew install gh      (or see https://cli.github.com)
  Windows       : winget install --id GitHub.cli
  then          : gh auth login

Set REPO=owner/name to target a different repository.
EOF
  exit 1
fi
if ! gh auth status >/dev/null 2>&1; then
  echo "ERROR: gh is not authenticated. Run: gh auth login" >&2
  exit 1
fi

echo "=== $REPO ==="
echo

ALL="$(gh release list --repo "$REPO" --limit 200 --json tagName,isPrerelease,isDraft,createdAt,assets \
        --jq '.[] | [.tagName, (.assets | length), (.assets | map(.size) | add // 0), .isPrerelease, .createdAt] | @tsv')"

if [ -z "$ALL" ]; then
  echo "No releases found."
  exit 0
fi

if [ "$LIST_ONLY" -eq 1 ]; then
  printf '%-18s %8s %12s  %s\n' TAG ASSETS SIZE DATE
  printf '%s\n' "$ALL" | awk -F'\t' \
    '{ printf "%-18s %8s %9.1f MB  %s%s\n", $1, $2, $3/1048576, $5, ($4=="true" ? "  (prerelease)" : "") }' 
  exit 0
fi

TOTAL="$(printf '%s\n' "$ALL" | wc -l | tr -d ' ')"
if [ "$TOTAL" -le "$KEEP" ]; then
  echo "Only $TOTAL release(s) exist and --keep is $KEEP; nothing to prune."
  exit 0
fi

# gh release list returns newest first, so the first KEEP lines are the ones
# that survive. Everything after is a deletion candidate.
CANDIDATES="$(printf '%s\n' "$ALL" | tail -n "+$((KEEP + 1))")"
CAND_COUNT="$(printf '%s\n' "$CANDIDATES" | wc -l | tr -d ' ')"
CAND_BYTES="$(printf '%s\n' "$CANDIDATES" | awk -F'\t' '{ s += $3 } END { print s+0 }')"

echo "Releases: $TOTAL total, keeping the newest $KEEP, pruning $CAND_COUNT."
echo "Reclaims approximately $(awk -v b="$CAND_BYTES" 'BEGIN { printf "%.1f", b/1048576 }') MB of release assets."
echo
echo "Will delete:"
printf '%s\n' "$CANDIDATES" | awk -F'\t' \
  '{ printf "  %-18s %2s assets  %8.1f MB  %s\n", $1, $2, $3/1048576, $5 }' 

if [ "$DELETE_TAGS" -eq 1 ]; then
  echo
  echo "ALSO deleting the git tags for those releases."
else
  echo
  echo "Git tags will be KEPT (pass --delete-tags to remove them too)."
fi

echo
if [ "$ASSUME_YES" -ne 1 ]; then
  echo "Dry run. Re-run with --yes to actually delete."
  exit 0
fi

echo "Deleting..."
FAILED=0
printf '%s\n' "$CANDIDATES" | while IFS=$'\t' read -r tag _n _size _pre _date; do
  # --cleanup-tag is passed ONLY when the operator asked for it: it was
  # previously unconditional, so a plain --yes run deleted every pruned
  # release's git tag while the output claimed tags were kept.
  if [ "$DELETE_TAGS" -eq 1 ]; then
    if gh release delete "$tag" --repo "$REPO" --yes --cleanup-tag >/dev/null 2>&1; then
      echo "  deleted $tag (tag removed)"
    else
      echo "  FAILED  $tag" >&2
    fi
  else
    if gh release delete "$tag" --repo "$REPO" --yes >/dev/null 2>&1; then
      echo "  deleted $tag (tag kept)"
    else
      echo "  FAILED  $tag" >&2
    fi
  fi
done

# gh exits non-zero on the first failure inside a pipeline subshell, which the
# pipe hides; the count below is what actually matters to the operator.
REMAINING="$(gh release list --repo "$REPO" --limit 200 --json tagName --jq 'length')"
echo
echo "Done. $REMAINING release(s) remain."
[ "$REMAINING" -le "$KEEP" ] || { echo "WARNING: more releases remain than expected." >&2; exit 1; }
exit 0
