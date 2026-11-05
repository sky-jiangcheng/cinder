#!/usr/bin/env bash
# Propagate wails.json's productVersion into the Homebrew / Scoop manifests.
#
# Called by scripts/bump-version.sh after the SSOT has moved, and runnable
# standalone (idempotent) to re-sync without a version change:
#
#   ./scripts/update-manifests.sh
#
# The sha256 fields are deliberately NOT touched: they are release-time facts
# that only exist once the release workflow has published the assets, and both
# Homebrew and Scoop hard-fail on a mismatch. They ship as __FILL_SHA256_*__
# placeholders and are filled in by hand after publishing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="$(grep -oE '"productVersion"[[:space:]]*:[[:space:]]*"[^"]+"' wails.json | head -1 | sed -E 's/.*"([^"]+)"$/\1/')"
if [[ -z "$VERSION" ]]; then
  echo "ERROR: could not read productVersion from wails.json" >&2
  exit 1
fi
echo "Syncing package manifests to $VERSION"

# Portable in-place edit, same technique (and same reason) as bump-version.sh:
# `sed -i` is not portable across BSD/GNU/toybox, so write to a temp file and
# rename. The quotes live on the replacement side — writing "\1$VERSION" would
# expand to e.g. "\11.7.9", which sed reads as back-reference 11.
update_file() {
  local file="$1" pattern="$2" replacement="$3"
  if [[ ! -f "$file" ]]; then
    echo "  WARN: $file not found, skipping" >&2
    return 0
  fi
  # mktemp creates 0600, so reset the mode on the replacement — otherwise every
  # run silently tightens the manifest's permissions. 0644 explicitly rather
  # than "copy the old mode via ls -l": manifest files are plain data, and
  # parsing `ls` output is not portable (toybox/busybox ls formats it
  # differently, which is the same trap that makes `sed -i` unusable here).
  local tmp
  tmp="$(mktemp "${file}.XXXXXX")"
  if ! sed -E "s|$pattern|$replacement|" "$file" > "$tmp"; then
    echo "  ERROR: sed failed for $file" >&2
    rm -f "$tmp"
    return 1
  fi
  chmod 0644 "$tmp"
  mv "$tmp" "$file"
  echo "  updated $file"
}

for cask in packaging/homebrew/Casks/repotory.rb \
            packaging/homebrew/Casks/repotory-mcp.rb \
            packaging/homebrew/Formula/repotory-mcp.rb; do
  # Ruby: `  version "1.7.9"`
  update_file "$cask" \
    '(version[[:space:]]+)"[^"]+"' \
    "\1\"$VERSION\""
done

for manifest in packaging/scoop/repotory.json packaging/scoop/repotory-mcp.json; do
  # JSON: the version is pinned in two places, and Scoop has no interpolation
  # in the top-level `url` — so both must move together or `scoop install`
  # silently resolves a 404.
  update_file "$manifest" \
    '("version"[[:space:]]*:[[:space:]]*)"[^"]+"' \
    "\1\"$VERSION\""
  update_file "$manifest" \
    '(releases/download/)v[0-9]+\.[0-9]+\.[0-9]+([+-][A-Za-z0-9.-]+)?' \
    "\1v$VERSION"
done

echo
echo "Done. Verify with:"
echo "  grep -rn '$VERSION' packaging/"
echo "  ruby -c packaging/homebrew/Casks/repotory.rb"
echo "  python3 -m json.tool packaging/scoop/repotory-mcp.json > /dev/null"
echo
echo "sha256 fields are filled automatically by release.yml (fill-sha256 job) after"
echo "each tag's release is published, or manually via:"
echo "  ./scripts/update-manifests.sh --fill-sha256 --from-api"

# --- sha256 filling -----------------------------------------------------------
#
# `--fill-sha256` replaces the __FILL_SHA256_*__ placeholders in packaging/ with
# the real digests of the published release. It only touches a manifest when
# the exact asset that manifest's URL points at has a digest available, so a
# partial release leaves the remaining placeholders in place instead of writing
# a digest that does not correspond to the download.
#
# Two digest sources:
#   --fill-sha256                reads assets/releases/v<version>/SHA256SUMS
#                                (offline path: build locally first, or use the
#                                file the release workflow already produced)
#   --fill-sha256 --from-api     reads the per-asset digest the GitHub Release
#                                API reports for the tag (recommended: matches
#                                what the release actually serves, no download;
#                                anonymous access, 60 req/h is ample for one run)
if [ "${1:-}" = "--fill-sha256" ]; then
  if [ "${2:-}" = "--from-api" ]; then
    SUMS="$ROOT/assets/releases/v$VERSION/SHA256SUMS.api"
    echo "Reading digests for v$VERSION from the GitHub Release API (anonymous)"
    mkdir -p "$(dirname "$SUMS")"
    curl -fsSL -H "Accept: application/vnd.github+json" \
      "https://api.github.com/repos/sky-jiangcheng/cinder/releases/tags/v$VERSION" \
    | python3 -c "
import json, sys
rel = json.load(sys.stdin)
assets = rel.get('assets')
if assets is None:
    sys.exit('no release found (message: %s)' % rel.get('message', '?'))
for a in assets:
    name, digest = a.get('name', ''), a.get('digest', '')
    if name and digest.startswith('sha256:'):
        print('%s  assets/releases/v$VERSION/%s' % (digest[7:], name))
" > "$SUMS"
  else
    SUMS="$ROOT/assets/releases/v$VERSION/SHA256SUMS"
  fi
  if [ ! -f "$SUMS" ]; then
    echo "ERROR: $SUMS not found. Either fetch digests from the release API:" >&2
    echo "         ./scripts/update-manifests.sh --fill-sha256 --from-api" >&2
    echo "       or build the assets first:" >&2
    echo "         ./scripts/build-release-assets.sh" >&2
    exit 1
  fi

  sha_for() { awk -v want="$1" '$2 ~ ("(^|/)" want "$") { print $1 }' "$SUMS" | head -1; }

  # Replace the digest that belongs to a specific asset, wherever it currently
  # sits. Keying off the asset name in the URL rather than off the placeholder
  # string is deliberate: a manifest may already hold a digest from an earlier
  # build of the same version (a locally-compiled artifact, say) that differs
  # from what the release actually serves. Matching only __FILL_SHA256_*__ would
  # leave those stale values in place and every install would fail verification.
  #
  # All three manifest formats put the digest on the line after the one naming
  # the asset: Cask and Formula emit `url` then `sha256`, Scoop emits "url"
  # then "hash". So: find the asset's line, overwrite the digest on the next
  # non-blank line.
  fill() {
    local file="$1" asset="$2"
    local digest
    digest="$(sha_for "$asset")"
    if [ -z "$digest" ]; then
      echo "  skip $file: $asset not in SHA256SUMS (left untouched)"
      return 0
    fi
    if ! grep -q "$asset" "$file"; then
      echo "  skip $file: no reference to $asset"
      return 0
    fi

    local tmp
    tmp="$(mktemp "${file}.XXXXXX")"
    trap 'rm -f "${tmp:-}"' RETURN
    awk -v asset="$asset" -v digest="$digest" '
      # Inside a block already matched for this asset, the digest is the next
      # non-blank line that mentions sha256.
      !inblk && index($0, asset) && /url/ { inblk = 1; print; next }
      inblk && /sha256/ {
        if (match($0, /"sha256:[^"]*"/))   sub(/"sha256:[^"]*"/, "\"sha256:" digest "\"")
        else if (match($0, /sha256 "[^"]*"/)) sub(/sha256 "[^"]*"/, "sha256 \"" digest "\"")
        inblk = 0; print; next
      }
      # Scoop accepts the digest with or without a "sha256:" prefix, and a
      # manifest edited by hand (or by an older revision of this script) may
      # carry the bare form. Match it too, or the substitution silently no-ops.
      inblk && /"hash"[[:space:]]*:/ {
        sub(/"hash"[[:space:]]*:[[:space:]]*"[^"]*"/, "\"hash\": \"sha256:" digest "\"")
        inblk = 0; print; next
      }
      inblk && NF == 0 { print; next }
      inblk { inblk = 0 }   # blank-tolerant, but do not run away on prose
      { print }
    ' "$file" > "$tmp"

    if cmp -s "$tmp" "$file"; then
      rm -f "$tmp"; trap - RETURN
      echo "  unchanged $file ($asset already current)"
      return 0
    fi
    chmod 0644 "$tmp"
    mv "$tmp" "$file"
    trap - RETURN
    echo "  set $file <- $asset (${digest:0:12}…)"
  }

  echo "Filling sha256 from $SUMS"
  # Desktop app
  fill packaging/homebrew/Casks/repotory.rb    repotory-darwin-arm64.dmg
  fill packaging/homebrew/Casks/repotory.rb    repotory-darwin-amd64.dmg
  fill packaging/scoop/repotory.json           repotory-windows-amd64.zip
  # MCP server
  fill packaging/homebrew/Casks/repotory-mcp.rb    repotory-mcp-darwin-arm64.tar.gz
  fill packaging/homebrew/Casks/repotory-mcp.rb    repotory-mcp-darwin-amd64.tar.gz
  fill packaging/homebrew/Formula/repotory-mcp.rb  repotory-mcp-darwin-arm64.tar.gz
  fill packaging/homebrew/Formula/repotory-mcp.rb  repotory-mcp-darwin-amd64.tar.gz
  fill packaging/homebrew/Formula/repotory-mcp.rb  repotory-mcp-linux-amd64.tar.gz
  fill packaging/scoop/repotory-mcp.json           repotory-mcp-windows-amd64.zip

  echo
  echo "Any manifest still holding a __FILL_SHA256_*__ placeholder needs a"
  echo "release asset that was not published - fetch it from the release page."
  exit 0
fi
