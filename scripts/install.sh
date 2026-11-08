#!/bin/bash
# Cinder Desktop install script for macOS and Linux.
# Installs the desktop app and the MCP server (cinder-mcp), which AI clients
# use to read the same local database.
#
# Release assets (see .github/workflows/release.yml) carry the brand and the
# version in their names, so a file in ~/Downloads says which build it is:
#   Linux   Cinder-<ver>-linux-amd64.tar.gz   -> bare `cinder` binary
#   macOS   Cinder-<ver>-darwin-<arch>.dmg    -> Cinder.app
#   MCP     cinder-mcp-<ver>-<target>.tar.gz  -> bare `cinder-mcp` binary
set -e

BINARY_NAME="cinder"
# The pre-rename names are still installed as symlinks: every AI client config
# and shell alias already on this machine points at them, and a rename is not a
# reason to break a working setup (ADR-0049 补注).
COMPAT_BINARY_NAME="repotory"
MCP_BINARY_NAME="cinder-mcp"
COMPAT_MCP_BINARY_NAME="repotory-mcp"
REPO="sky-jiangcheng/cinder"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

case "$OS" in
  linux)   TARGET="linux-amd64"; DEFAULT_INSTALL_DIR="/usr/local/bin" ;;
  darwin)
    if [ "$ARCH" = "arm64" ]; then
      TARGET="darwin-arm64"
    else
      TARGET="darwin-amd64"
    fi
    DEFAULT_INSTALL_DIR="/usr/local/bin"
    ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# INSTALL_DIR is overridable so CI can install into a scratch directory
# without sudo and verify the script end-to-end (see
# .github/workflows/install-smoke.yml). Defaults to the system path above.
INSTALL_DIR="${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"

# --- Which release? -----------------------------------------------------------
#
# Asset names embed the version, so `releases/latest/download/<file>` cannot be
# used any more: the tag has to be known before the URL can be built. It is
# read from the redirect of /releases/latest (one request, no API rate limit —
# the anonymous GitHub API shares 60 req/h per IP and CI runners exhaust it).
#
# RELEASES (a flat directory of assets) and VERSION are both overridable so the
# install smoke test can exercise this script against a local fixture server
# without a published release (see .github/workflows/install-smoke.yml).
if [ -z "${RELEASES:-}" ]; then
  if [ -z "${VERSION:-}" ]; then
    VERSION="$(curl -fsSIL -o /dev/null -w '%{url_effective}' \
      "https://github.com/$REPO/releases/latest" 2>/dev/null \
      | sed -E 's|.*/tag/v?||')"
    if [ -z "$VERSION" ] || [ "$VERSION" = "$REPO" ]; then
      echo "ERROR: could not resolve the latest release of $REPO." >&2
      echo "       Set VERSION or RELEASES to install a specific build." >&2
      exit 1
    fi
  fi
  RELEASES="https://github.com/$REPO/releases/download/v$VERSION"
fi
if [ -z "${VERSION:-}" ]; then
  VERSION="$(printf '%s' "$RELEASES" | sed -E 's|.*/v?([0-9]+\.[0-9]+\.[0-9]+.*)$|\1|')"
fi
echo "Installing Cinder $VERSION ($TARGET)"

# Run a command with sudo only when the install dir is not writable.
# Usage: as_root <cmd...>
as_root() {
  if [ -w "$INSTALL_DIR" ]; then
    "$@"
  else
    echo "Need sudo to write to $INSTALL_DIR"
    sudo "$@"
  fi
}

# Fetch $1 (an http(s) URL) into $2. Fails loudly on a non-2xx response so a
# wrong asset name surfaces as an error instead of a zero-byte binary.
fetch() {
  curl -fsSL --retry 3 -o "$2" "$1"
}

# Move the local file $1 to $2, preserving its executable bit.
install_binary() {
  as_root install -m 0755 "$1" "$2"
}

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

# --- Checksum verification -----------------------------------------------------
#
# Every release publishes a SHA256SUMS asset listing the digest of each
# shipped file (see .github/workflows/release.yml). Verifying against it turns
# a corrupt mirror or a tampered download into a loud failure instead of a
# silently broken binary.
#
# The manifest is optional on purpose: releases published before it existed
# do not have the asset, and a missing manifest must not fail an otherwise
# healthy install. A digest MISMATCH is always fatal — that is the whole point
# of the check.

SHA256SUMS_FILE="$TMP_DIR/SHA256SUMS"
SHA256SUMS_LOADED="no" # no | failed | yes

# sha256_of <file> prints the file's SHA-256 digest with whichever tool the
# platform ships: shasum on macOS, sha256sum on Linux.
sha256_of() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

# load_sha256sums fetches the release's SHA256SUMS manifest once. Any failure
# (404 on an older release, network hiccup) downgrades to "unavailable" and
# verification is skipped downstream.
load_sha256sums() {
  [ "$SHA256SUMS_LOADED" != "no" ] && return 0
  SHA256SUMS_LOADED="failed"
  if curl -fsSL --retry 2 -o "$SHA256SUMS_FILE" "$RELEASES/SHA256SUMS" 2>/dev/null \
    && [ -s "$SHA256SUMS_FILE" ]; then
    SHA256SUMS_LOADED="yes"
    echo "Found SHA256SUMS manifest — downloads will be verified."
  else
    echo "WARNING: this release has no SHA256SUMS asset, skipping checksum verification." >&2
  fi
}

# verify_sha256 <file> <asset-name> checks a downloaded release asset against
# the manifest and exits on mismatch. An asset missing from the manifest is
# only a warning (a partial manifest must not block an install either).
verify_sha256() {
  load_sha256sums
  [ "$SHA256SUMS_LOADED" = "yes" ] || return 0

  local expected
  # sub() strips a CR from CRLF manifests so a Windows-generated SHA256SUMS
  # still matches; the "*name" form covers binary-mode digest listings.
  expected=$(awk -v name="$2" '{sub(/\r$/, "")} $2 == name || $2 == "*"name {print $1; exit}' "$SHA256SUMS_FILE")
  if [ -z "$expected" ]; then
    echo "WARNING: $2 is not listed in SHA256SUMS, skipping its verification." >&2
    return 0
  fi

  local actual
  actual=$(sha256_of "$1")
  if [ "$actual" != "$expected" ]; then
    echo "ERROR: SHA256 mismatch for $2 — refusing to install." >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
  echo "Verified $2 (sha256)."
}

# --- Desktop app --------------------------------------------------------------

# APP_INSTALL_DIR is where the .app lands. Overridable (like INSTALL_DIR)
# so CI can verify the script without writing to the real /Applications.
APP_INSTALL_DIR="${APP_INSTALL_DIR:-/Applications}"

if [ "$OS" = "darwin" ]; then
  DMG="$TMP_DIR/Cinder.dmg"
  APP_NAME="Cinder.app"
  DESKTOP_ASSET="Cinder-$VERSION-$TARGET.dmg"
  echo "Downloading Cinder Desktop for $TARGET (dmg)..."
  fetch "$RELEASES/$DESKTOP_ASSET" "$DMG"
  verify_sha256 "$DMG" "$DESKTOP_ASSET"

  MOUNT_DIR=$(hdiutil attach "$DMG" -nobrowse -readonly | tail -1 | sed -E 's|.*(/Volumes/.*)|\1|')
  trap 'rm -rf "$TMP_DIR"; [ -n "${MOUNT_DIR:-}" ] && hdiutil detach "$MOUNT_DIR" -quiet' EXIT
  if [ ! -d "$MOUNT_DIR/$APP_NAME" ]; then
    echo "ERROR: $APP_NAME not found inside the dmg" >&2
    exit 1
  fi
  as_root mkdir -p "$APP_INSTALL_DIR"
  as_root rm -rf "$APP_INSTALL_DIR/$APP_NAME"
  as_root cp -R "$MOUNT_DIR/$APP_NAME" "$APP_INSTALL_DIR/"
  hdiutil detach "$MOUNT_DIR" -quiet
  MOUNT_DIR=""

  # The bundle is ad-hoc signed (no Apple Developer ID), so a quarantine flag
  # makes Gatekeeper refuse the first launch with "Apple cannot check Cinder
  # for malicious software" and offer only 完成 / 移到废纸篓. curl does not set
  # that flag, so this install usually produces a clean bundle already — but a
  # flag inherited from the dmg (or set by anything that touched the file) is
  # invisible until the user is staring at the dialog, and the command below is
  # exactly what that dialog is asking them to do. Failure is not fatal: the
  # app is installed, this only makes the first launch quiet.
  as_root xattr -dr com.apple.quarantine "$APP_INSTALL_DIR/$APP_NAME" 2>/dev/null || true

  echo ""
  echo "Cinder Desktop installed to $APP_INSTALL_DIR/$APP_NAME"
  echo "Run 'open -a \"$APP_INSTALL_DIR/$APP_NAME\"' to start!"
else
  TARBALL="$TMP_DIR/Cinder.tar.gz"
  DESKTOP_ASSET="Cinder-$VERSION-$TARGET.tar.gz"
  echo "Downloading Cinder Desktop for $TARGET (tar.gz)..."
  fetch "$RELEASES/$DESKTOP_ASSET" "$TARBALL"
  verify_sha256 "$TARBALL" "$DESKTOP_ASSET"
  tar -xzf "$TARBALL" -C "$TMP_DIR" "$BINARY_NAME"
  install_binary "$TMP_DIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
  # Old name → new name, so `repotory` in someone's shell history still starts
  # the app. A symlink also means there is exactly one binary to update.
  as_root ln -sf "$BINARY_NAME" "$INSTALL_DIR/$COMPAT_BINARY_NAME"

  echo ""
  echo "Cinder Desktop installed to $INSTALL_DIR/$BINARY_NAME"
  echo "Run '$BINARY_NAME' to start!"
fi

# --- MCP server ---------------------------------------------------------------
# Ships as its own pure-Go archive, so AI clients can use Cinder without the
# desktop app. Non-fatal: a release predating the split simply has no asset.
echo ""
echo "Downloading Cinder MCP server for $TARGET..."

MCP_ASSET="cinder-mcp-$VERSION-$TARGET.tar.gz"
MCP_URL="$RELEASES/$MCP_ASSET"

# Recovery hint shared by both failure paths (no asset / extract failure).
mcp_install_hint() {
  echo "WARNING: could not install $MCP_BINARY_NAME ($1)." >&2
  echo "         The desktop app is installed and working; install the MCP server manually:" >&2
  echo "         $MCP_URL" >&2
  echo "         then: tar -xzf $MCP_ASSET && sudo install -m 0755 $MCP_BINARY_NAME $INSTALL_DIR/" >&2
}

if ! fetch "$MCP_URL" "$TMP_DIR/$MCP_ASSET"; then
  mcp_install_hint "no asset for $TARGET, or download failed"
else
  # Verified outside an if-condition on purpose: a checksum mismatch exits
  # from inside verify_sha256, and must abort the install rather than fall
  # through to the non-fatal hint below.
  verify_sha256 "$TMP_DIR/$MCP_ASSET" "$MCP_ASSET"
  if tar -xzf "$TMP_DIR/$MCP_ASSET" -C "$TMP_DIR" "$MCP_BINARY_NAME" \
    && install_binary "$TMP_DIR/$MCP_BINARY_NAME" "$INSTALL_DIR/$MCP_BINARY_NAME"; then
    as_root ln -sf "$MCP_BINARY_NAME" "$INSTALL_DIR/$COMPAT_MCP_BINARY_NAME"
    echo "Cinder MCP server installed to $INSTALL_DIR/$MCP_BINARY_NAME"
    echo "Register it with an AI client: claude mcp add repotory -- $INSTALL_DIR/$MCP_BINARY_NAME"
  else
    mcp_install_hint "extract or install failed"
  fi
fi
