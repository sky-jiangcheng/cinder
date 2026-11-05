#!/usr/bin/env bash
# Sync the package-manager manifests to the external tap / bucket repositories.
#
#   ./scripts/sync-packaging.sh homebrew [--push] [--with-workflows]
#   ./scripts/sync-packaging.sh scoop    [--push] [--with-workflows]
#
# The tap and the bucket are SHARED: they serve every project that has a
# namespace under packaging/. The layout is:
#
#   packaging/homebrew/                shared: the tap's own README and CI
#   packaging/scoop/                    shared: the bucket's own README and CI
#   packaging/<project>/homebrew/…      one project's casks and formulas
#   packaging/<project>/scoop/…        one project's scoop manifests
#
# So a future project joins by adding a directory and its manifests — this
# script discovers the namespaces, and the guards (check-version-drift.mjs,
# update-manifests.sh, check-packaging-manifests.sh) do the same. Nothing here
# names a project.
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

# ------------------------------------------------------------- namespaces
# A namespace is a directory under packaging/ that holds this target's
# manifests. The shared level (packaging/homebrew, packaging/scoop) is NOT a
# namespace: it holds the tap's / bucket's own README and CI, which belong to
# the target repository as a whole rather than to one project.
PROJECTS=()
for dir in packaging/*/; do
  name="$(basename "$dir")"
  [ -d "packaging/$name/$TARGET" ] || continue
  PROJECTS+=("$name")
done

if [ "${#PROJECTS[@]}" -eq 0 ]; then
  echo "ERROR: no packaging/<project>/$TARGET namespace found." >&2
  echo "       A project joins the tap by adding packaging/<project>/$TARGET/." >&2
  exit 1
fi

echo "==> projects for $TARGET: ${PROJECTS[*]}"

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
# DEPRECATED-ALIAS: an alias of a current manifest, not a manifest of its own.
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

# read_aliases <conf-file>
# Emits the alias table, one record per line, comments and blanks removed.
# The table is per project and per target, so a project declares its own
# deprecated tokens without this script knowing any of them.
#
# Homebrew records have three fields (source|destination|sed expression);
# scoop records have two (source|destination) — JSON cannot carry a comment
# header, so its aliases are plain copies under a different name.
read_aliases() {
  local conf="$1"
  [ -f "$conf" ] || return 0
  # Only WHOLE-LINE comments are stripped. A homebrew record carries a sed
  # expression that uses `#` as its own delimiter, so a strip that reached
  # mid-line would cut the expression in half and the alias would be written
  # with no rename at all.
  sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$/d' "$conf"
}

echo "==> writing manifests"
for project in "${PROJECTS[@]}"; do
  src="packaging/$project/$TARGET"
  if [ "$TARGET" = "homebrew" ]; then
    for kind in "$DEST_CASK" "$DEST_FORMULA"; do
      [ -d "$src/$kind" ] || continue
      for f in "$src/$kind"/*.rb; do
        [ -e "$f" ] || continue
        write_manifest "$f" "$kind/$(basename "$f")"
      done
    done
    while IFS='|' read -r a_src a_dst a_expr; do
      [ -n "$a_src" ] || continue
      alias_manifest "$src/$a_src" "$a_dst" "$a_expr" "$HEADER_FILE"
    done < <(read_aliases "$src/aliases.conf")
  else
    for f in "$src"/*.json; do
      [ -e "$f" ] || continue
      write_manifest "$f" "$DEST_CASK/$(basename "$f")"
    done
    while IFS='|' read -r a_src a_dst; do
      [ -n "$a_src" ] || continue
      # The bucket alias keeps "bin": "repotory.exe" — the zip contains that
      # file. Only the manifest FILENAME changes; the user still types
      # `scoop install reponest` and gets the current binary under its real
      # name.
      #
      # No comment header: JSON has no comment syntax, and a `#` line makes the
      # manifest unparseable. The deprecation notice lives in the bucket
      # README.
      write_manifest "$src/$a_src" "$DEST_CASK/$a_dst"
    done < <(read_aliases "$src/aliases.conf")
  fi
done

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
  for f in "packaging/${TARGET}"/.github/workflows/*; do
    [ -e "$f" ] || continue
    write_manifest "$f" ".github/workflows/$(basename "$f")"
  done
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
  if ! curl -fsSL --max-time 60 -H "Accept: application/vnd.github+json" \
       ${API_AUTH[@]+"${API_AUTH[@]}"} \
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
  # The scan covers every namespace: a future project's manifests are checked
  # by the same rule, without this file naming it.
  while IFS= read -r d; do
    [ -n "$d" ] || continue
    if ! grep -q "$d" "$SUMS"; then
      echo "STALE digest in packaging/: $d" >&2
      stale=1
    fi
  done < <(find packaging -type f \( -name '*.rb' -o -name '*.json' \) -exec grep -hoE '[a-f0-9]{64}' {} + | sort -u)
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
