#!/usr/bin/env bash
# Build release artifacts into assets/releases/v<version>/.
#
# Two products ship from one release:
#   - repotory-mcp  : pure Go, zero CGO. Cross-compiles for every target from
#                     any host, so this script can always produce all four.
#   - repotory      : the Wails desktop shell. It links GTK/WebKit on Linux and
#                     needs the native toolchain on macOS and Windows, so it can
#                     only be produced on each target's own CI runner - exactly
#                     what .github/workflows/release.yml does on a tag push.
#
# The script therefore builds what the host can build and says plainly what it
# skipped, rather than failing halfway and leaving a half-populated release
# directory. A partially-populated release that looks complete is worse than an
# obviously incomplete one.
#
# Usage:
#   ./scripts/build-release-assets.sh              # build for the current version
#   ./scripts/build-release-assets.sh --mcp-only   # skip desktop, never fail on it
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MCP_ONLY=0
[ "${1:-}" = "--mcp-only" ] && MCP_ONLY=1

VERSION="$(grep -oE '"productVersion"[[:space:]]*:[[:space:]]*"[^"]+"' wails.json | head -1 | sed -E 's/.*"([^"]+)"$/\1/')"
if [[ -z "$VERSION" ]]; then
  echo "ERROR: could not read productVersion from wails.json" >&2
  exit 1
fi

OUT="$ROOT/assets/releases/v$VERSION"
mkdir -p "$OUT"

# Matches the release matrix in .github/workflows/release.yml.
TARGETS=(linux-amd64 darwin-amd64 darwin-arm64 windows-amd64)
LDFLAGS="-s -w -X repotory/internal/version.Version=$VERSION"

echo "=== Cinder $VERSION ==="
echo "output: $OUT"
echo

build_mcp() {
  local target="$1" goos="$2" goarch="$3"
  local stage="$OUT/.stage/$target"
  mkdir -p "$stage"

  # CGO_ENABLED=0: the MCP server is a pure stdio JSON-RPC loop with no UI
  # dependency, so it cross-compiles cleanly and runs anywhere.
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -ldflags "$LDFLAGS" -o "$stage/repotory-mcp" ./cmd/mcp/ 2>&1 | sed 's/^/    /'

  # `go build -o name` uses the name verbatim, so the Windows .exe suffix has to
  # be added here - release assets, the Scoop manifest and install.ps1 all
  # expect repotory-mcp.exe.
  local asset
  case "$target" in
    windows-amd64)
      mv "$stage/repotory-mcp" "$stage/repotory-mcp.exe"
      asset="repotory-mcp-$target.zip"
      (cd "$stage" && zip -q -j "$OUT/$asset" repotory-mcp.exe)
      ;;
    *)
      asset="repotory-mcp-$target.tar.gz"
      tar -czf "$OUT/$asset" -C "$stage" repotory-mcp
      ;;
  esac
  echo "  ✓ $asset"
}

# The desktop shell needs web/dist to exist for //go:embed (see ci.yml), plus a
# platform toolchain. Returns 1 with a reason instead of dying.
can_build_desktop() {
  if [ ! -f web/dist/index.html ]; then
    echo "web/dist is missing (the frontend has not been built)" >&2
    return 1
  fi
  case "$(uname -s)" in
    Darwin) return 0 ;;
    Linux)
      if ! command -v pkg-config >/dev/null || ! pkg-config --exists gtk+-3.0; then
        echo "GTK3 development files are not installed (libgtk-3-dev)" >&2
        return 1
      fi
      return 0
      ;;
    *) return 0 ;;
  esac
}

echo "[1/2] MCP server (all targets)"
for t in "${TARGETS[@]}"; do
  case "$t" in
    linux-amd64)   build_mcp "$t" linux   amd64 ;;
    darwin-amd64)  build_mcp "$t" darwin  amd64 ;;
    darwin-arm64)  build_mcp "$t" darwin  arm64 ;;
    windows-amd64) build_mcp "$t" windows amd64 ;;
  esac
done

echo
echo "[2/2] Desktop app"
DESKTOP_SKIPPED=0
if [ "$MCP_ONLY" -eq 1 ]; then
  echo "  skipped (--mcp-only)"
  DESKTOP_SKIPPED=1
elif ! can_build_desktop; then
  echo "  skipped: this host cannot build the Wails shell."
  echo "  Reason: $(can_build_desktop 2>&1 >/dev/null | head -1)"
  echo "  Produce it by pushing a v$VERSION tag so release.yml runs on the native runners."
  DESKTOP_SKIPPED=1
else
  echo "  ! building the desktop app requires 'wails build'; run it via release.yml"
  echo "    or 'wails build -platform <goos>/<goarch> -clean -ldflags \"$LDFLAGS\"'."
  DESKTOP_SKIPPED=1
fi

rm -rf "$OUT/.stage"

# Checksums last, over exactly what landed in the directory. Homebrew and Scoop
# refuse to install on a mismatch, so this file is what makes those manifests
# usable once filled in.
if command -v sha256sum >/dev/null; then
  (cd "$OUT" && sha256sum ./*.tar.gz ./*.zip 2>/dev/null > SHA256SUMS || true)
elif command -v shasum >/dev/null; then
  (cd "$OUT" && shasum -a 256 ./*.tar.gz ./*.zip 2>/dev/null > SHA256SUMS || true)
fi

echo
echo "=== $OUT ==="
ls -la "$OUT"
echo
if [ "$DESKTOP_SKIPPED" -eq 1 ]; then
  echo "NOTE: desktop artifacts are absent. Tag v$VERSION to have release.yml build them."
  echo "      The sha256 values in packaging/ must be filled in after that run."
fi
