#!/usr/bin/env bash
# Sync the package-manager manifests to the external tap / bucket repositories.
#
#   ./scripts/sync-packaging.sh homebrew [--push] [--with-workflows]
#   ./scripts/sync-packaging.sh scoop    [--push] [--with-workflows]
#
# --with-workflows also replaces the tap's / bucket's own CI definitions with
# the copies kept under packaging/. It is OFF by default because GitHub
# refuses to let a token without the `workflow` scope create or update
# .github/workflows/* files: the push fails with "refusing to allow an OAuth
# App to create or update workflow ... without `workflow` scope", and a
# half-applied sync is worse than a manifest-only one. Run it with a token
# that carries the scope, or update those files by hand.
#
# Why this script exists. packaging/README.md says the tap and the bucket
# "are copied here on every release" — that was never true, and nothing
# enforced it. The result was a tap and a bucket still serving the PRE-RENAME
# RepoNest v1.8.0 manifests while the product shipped v2.14.0: `brew install
# --cask repotory` did not exist, and `brew install --cask reponest` silently
# installed a six-versions-old build. The manifests were updated in THIS
# repository (release.yml's fill-sha256 job commits them back to master) and
# then went nowhere.
#
# So this script is the missing half: it copies packaging/ into the tap and the
# bucket and, with --push, commits and pushes. release.yml's sync job runs it
# after each release, so the two cannot drift again.
#
# The pre-rename names (reponest / reponest-mcp) are kept as ALIASES: the same
# current artifact under the old token, so a command somebody saved before the
# rename keeps working — and now installs the CURRENT build instead of the
# v1.8.0 fossil it used to fetch. An alias that points at the old release is
# worse than no alias: it fails silently, and "I installed it and it is six
# versions behind" is a far more expensive report than "no such cask".
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TARGET="${1:-}"
PUSH=0
WITH_WORKFLOWS=0
shift || true
for arg in "$@"; do
  case "$arg" in
    --push) PUSH=1 ;;
    --with-workflows) WITH_WORKFLOWS=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 1 ;;
  esac
done

case "$TARGET" in
  homebrew) REPO_SLUG="sky-jiangcheng/homebrew-repo"; DEST_CASK="Casks"; DEST_FORMULA="Formula" ;;
  scoop)    REPO_SLUG="sky-jiangcheng/scoop-repo";  DEST_CASK="bucket"; DEST_FORMULA="" ;;
  *) echo "usage: $0 <homebrew|scoop> [--push]" >&2; exit 1 ;;
esac

WORK="$(mktemp -d)"
# cd out before deleting: the script cds into the clone inside WORK, and
# removing the current working directory out from under the shell is what
# makes the cleanup fail on some platforms.
trap 'cd "$ROOT" && rm -rf "$WORK"' EXIT
CLONE="$WORK/repo"

# Authentication. A PAT with write access to the tap and the bucket goes in
# through PACKAGING_PUSH_TOKEN; it is put in the remote URL rather than in a
# global git config so it does not linger in ~/.gitconfig on the runner.
#
# The workflow token of the MAIN repository cannot write to the tap or the
# bucket, and cannot update .github/workflows/* at all — that needs the
# `workflow` scope. Hence a dedicated secret.
AUTH=""
if [ -n "${PACKAGING_PUSH_TOKEN:-}" ]; then
  AUTH="x-access-token:${PACKAGING_PUSH_TOKEN}@"
fi
REMOTE="https://github.com/${REPO_SLUG}.git"
if [ -n "$AUTH" ]; then
  REMOTE="https://${AUTH}github.com/${REPO_SLUG}.git"
fi

echo "==> cloning $REPO_SLUG"
git clone --depth=1 "$REMOTE" "$CLONE"

# write_manifest <source> <repo-relative-destination> [header-file]
# Copies a manifest from packaging/ into the target repository.
write_manifest() {
  local src="$1" dst="$2" header="${3:-}"
  local out="$CLONE/$dst"
  mkdir -p "$(dirname "$out")"
  if [ -n "$header" ]; then
    cat "$header" "$src" > "$out"
  else
    cp "$src" "$out"
  fi
  chmod 0644 "$out"
  echo "    $dst"
}

# alias_manifest <source> <repo-relative-destination> <name-expression> <header-file>
# The alias differs from the main manifest ONLY in the token users type.
# Everything that must match the artifact on disk — the URLs, the digests, the
# app bundle name, the binary name inside the archive — is identical, because
# it is the same file.
alias_manifest() {
  local src="$1" dst="$2" expr="$3" header="$4"
  local out="$CLONE/$dst"
  mkdir -p "$(dirname "$out")"
  cat "$header" "$src" > "$out"
  local tmp
  tmp="$(mktemp "${out}.XXXXXX")"
  sed -E "$expr" "$out" > "$tmp"
  chmod 0644 "$tmp"
  mv "$tmp" "$out"
  echo "    $dst (alias)"
}

# The deprecation header for the pre-rename aliases. It goes ABOVE the
# manifest so the reason a reader finds it is the first thing they see.
#
# Ruby manifests take a `#` comment block. JSON has no comments at all — a
# `#` line makes the file unparseable, which the bucket's own CI caught
# immediately — so JSON aliases are written WITHOUT a header and the notice
# lives in the bucket's README instead.
alias_header() {
  cat <<'HEADER'
# DEPRECATED NAME — the project was renamed from RepoNest to Cinder.
#
# This alias exists so a command saved before the rename keeps working. It
# serves the CURRENT release: the previous version of this file pointed at
# RepoNest v1.8.0, so installing it silently fetched a six-versions-old
# build. Use the new name instead.
#
HEADER
}

HEADER_FILE="$WORK/alias-header.txt"
alias_header > "$HEADER_FILE"

if [ "$TARGET" = "homebrew" ]; then
  echo "==> writing manifests"
  write_manifest packaging/homebrew/Casks/repotory.rb        "$DEST_CASK/repotory.rb"
  write_manifest packaging/homebrew/Casks/repotory-mcp.rb    "$DEST_CASK/repotory-mcp.rb"
  write_manifest packaging/homebrew/Formula/repotory-mcp.rb  "$DEST_FORMULA/repotory-mcp.rb"
  alias_manifest packaging/homebrew/Casks/repotory.rb       "$DEST_CASK/reponest.rb" \
    's|^cask "repotory" do$|cask "reponest" do|' "$HEADER_FILE"
  alias_manifest packaging/homebrew/Casks/repotory-mcp.rb   "$DEST_CASK/reponest-mcp.rb" \
    's|^cask "repotory-mcp" do$|cask "reponest-mcp" do|' "$HEADER_FILE"
  alias_manifest packaging/homebrew/Formula/repotory-mcp.rb "$DEST_FORMULA/reponest-mcp.rb" \
    's|^class RepotoryMcp < Formula$|class ReponestMcp < Formula|' "$HEADER_FILE"
else
  echo "==> writing manifests"
  write_manifest packaging/scoop/repotory.json     "$DEST_CASK/repotory.json"
  write_manifest packaging/scoop/repotory-mcp.json "$DEST_CASK/repotory-mcp.json"
  # The bucket alias keeps "bin": "repotory.exe" — the zip contains that file.
  # Only the manifest FILENAME changes; the user still types `scoop install
  # reponest` and gets the current binary under its real name.
  #
  # No comment header: JSON has no comment syntax, and a `#` line makes the
  # manifest unparseable. The deprecation notice lives in the bucket README.
  write_manifest packaging/scoop/repotory.json     "$DEST_CASK/reponest.json"
  write_manifest packaging/scoop/repotory-mcp.json "$DEST_CASK/reponest-mcp.json"
fi

# The README documents the install commands by name, so it moves with the
# manifests. It is a plain file (not a workflow), so the ordinary token can
# push it.
write_manifest "packaging/${TARGET}/README.md" README.md

# The tap's and the bucket's own CI are part of the same contract: they
# install and verify the manifests BY NAME, so a rename that does not reach
# them turns the tap's test into a red build on the very next push. They are
# kept under packaging/ here so this repository stays the single source of
# truth for what the tap contains.
#
# Opt-in: a token without the `workflow` scope cannot push these files at all
# (GitHub rejects the push outright), and a push that dies half-applied is
# worse than one that never tried.
if [ "$WITH_WORKFLOWS" -eq 1 ]; then
  echo "==> writing CI workflows"
  if [ "$TARGET" = "homebrew" ]; then
    write_manifest packaging/homebrew/.github/workflows/tests.yml .github/workflows/tests.yml
  else
    write_manifest packaging/scoop/.github/workflows/verify.yml .github/workflows/verify.yml
  fi
else
  echo "==> skipping CI workflows (pass --with-workflows; needs a token with the 'workflow' scope)"
fi

VERSION="$(grep -oE '"productVersion"[[:space:]]*:[[:space:]]*"[^"]+"' wails.json | head -1 | sed -E 's/.*"([^"]+)"$/\1/')"

# ---------------------------------------------------------------- freshness
# The manifests carry a sha256 per asset, and brew/scoop HARD-FAIL when the
# download does not match. So a manifest that still holds an older release's
# digest is not "slightly wrong" — it is an install path that fails on the
# user's machine.
#
# That is not hypothetical. The digests are filled by the release workflow's
# fill-sha256 job, which commits the filled manifests back to master. A
# manual sync run from a checkout that predates that commit publishes the
# PREVIOUS release's digests, and the tap/bucket then point at artifacts that
# no longer match what the release serves. (Seen for real on v2.18.0: the
# first manual sync shipped v2.17.0's digests, and Homebrew would have
# rejected the download.)
#
# So: before pushing, ask the Release API which digests this version
# actually serves, and refuse to publish anything that is not in that set.
if [ "$PUSH" -eq 1 ]; then
  echo "==> checking the manifests against the v${VERSION} release"
  SUMS="$(mktemp)"
  trap 'cd "$ROOT" && rm -rf "$WORK" && rm -f "$SUMS"' EXIT
  # Authenticate the API read.
  #
  # An ANONYMOUS request shares the 60 requests/hour budget of the runner's
  # IP, and GitHub's shared runner IPs exhaust it — the read then answers 403
  # and the script refuses to push anything it cannot verify. The release
  # itself is already out at that point, so the failure mode is the worst
  # kind: the tap keeps serving the previous version and the release looks
  # complete. (Seen for real on v2.26.0: the release published, the tap sync
  # died on exactly this.)
  #
  # The read needs a token that can read THIS repository's releases — the
  # push token may be scoped to the tap and the bucket only — so the two
  # credentials are deliberately separate: GITHUB_TOKEN/GH_TOKEN reads,
  # PACKAGING_PUSH_TOKEN writes.
  API_AUTH=()
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    API_AUTH=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  elif [ -n "${GH_TOKEN:-}" ]; then
    API_AUTH=(-H "Authorization: Bearer ${GH_TOKEN}")
  elif [ -n "${PACKAGING_PUSH_TOKEN:-}" ]; then
    API_AUTH=(-H "Authorization: Bearer ${PACKAGING_PUSH_TOKEN}")
  fi
  if ! curl -fsSL --max-time 60 -H "Accept: application/vnd.github+json" "${API_AUTH[@]}" \
       "https://api.github.com/repos/sky-jiangcheng/cinder/releases/tags/v${VERSION}" \
     | python3 -c "
import json, sys
rel = json.load(sys.stdin)
assets = rel.get('assets')
if assets is None:
    sys.exit('no release found for v%s (message: %s)' % ('${VERSION}', rel.get('message', '?')))
for a in assets:
    name, digest = a.get('name', ''), a.get('digest', '')
    if name and digest.startswith('sha256:'):
        print(digest[7:])
" > "$SUMS"; then
    echo "ERROR: could not read the v${VERSION} release from the GitHub API." >&2
    echo "       Refusing to push unverified manifests." >&2
    echo "       If this is a 403, the request went out unauthenticated and the" >&2
    echo "       shared runner IP had exhausted the anonymous rate limit — set" >&2
    echo "       GITHUB_TOKEN (a token that can read this repo's releases)." >&2
    exit 1
  fi
  stale=0
  for d in $(grep -rhoE '[a-f0-9]{64}' packaging/homebrew packaging/scoop | sort -u); do
    if ! grep -q "$d" "$SUMS"; then
      echo "STALE digest in packaging/: $d" >&2
      stale=1
    fi
  done
  if [ "$stale" -ne 0 ]; then
    echo "ERROR: packaging/ carries digests that the v${VERSION} release does not serve." >&2
    echo "       The release workflow fills them in a commit to master — this" >&2
    echo "       checkout is probably older than that commit. Fix:" >&2
    echo "         git pull --rebase origin master" >&2
    echo "         ./scripts/update-manifests.sh --fill-sha256 --from-api" >&2
    exit 1
  fi
  echo "    all packaging/ digests match release v${VERSION}"
fi

echo "==> diff"
cd "$CLONE"
# Stage first: the new repotory.* manifests are UNTRACKED, and a plain
# `git diff` only sees tracked files — the stat would omit exactly the files
# this script exists to add.
git add -A
if git diff --cached --quiet; then
  echo "    no changes — $REPO_SLUG is already in sync"
  exit 0
fi
git --no-pager diff --cached --stat

if [ "$PUSH" -eq 0 ]; then
  echo "==> dry run (pass --push to commit and push)"
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git add -A
git commit -m "chore(packaging): sync manifests to v${VERSION}" \
           -m "Automated by scripts/sync-packaging.sh from the main repository."
git push origin HEAD
echo "==> pushed to $REPO_SLUG"
