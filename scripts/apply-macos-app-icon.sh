#!/usr/bin/env bash
# Inject a system-appearance-aware macOS app icon into a Wails-built .app.
#
# Wails v2 reads build/appicon.png and emits a single CFBundleIconFile. That
# cannot carry a light/dark pair. This script compiles build/AppIcon.icon
# (generated from build/icon.svg) into Assets.car and points CFBundleIconName
# at it; the original iconfile.icns stays as the classic fallback.
#
# Usage:
#   scripts/apply-macos-app-icon.sh [path/to/Cinder.app]
#
# With no argument the single build/bin/*.app bundle is used. The script must
# run AFTER `wails build` and BEFORE codesigning — signing an already-signed
# bundle after this point invalidates the signature.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ICON_SRC="$ROOT/build/AppIcon.icon"

APP="${1:-}"
if [ -z "$APP" ]; then
  APP="$(ls -d "$ROOT"/build/bin/*.app 2>/dev/null | head -1 || true)"
fi
if [ -z "$APP" ] || [ ! -d "$APP" ]; then
  echo "ERROR: no .app bundle found; pass its path explicitly" >&2
  exit 1
fi
if [ "$(uname -s)" != "Darwin" ]; then
  echo "ERROR: Icon Composer icons can only be compiled on macOS" >&2
  exit 1
fi
if [ ! -f "$ICON_SRC/icon.json" ] || [ ! -f "$ICON_SRC/Assets/knot.svg" ]; then
  echo "ERROR: $ICON_SRC is incomplete; run 'node scripts/generate-icons.mjs'" >&2
  exit 1
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/cinder-appicon.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/compiled"

xcrun actool "$ICON_SRC" \
  --compile "$TMP/compiled" \
  --output-format human-readable-text \
  --notices --warnings --errors \
  --output-partial-info-plist "$TMP/info.plist" \
  --app-icon AppIcon \
  --include-all-app-icons \
  --enable-on-demand-resources NO \
  --development-region en \
  --target-device mac \
  --minimum-deployment-target 26.0 \
  --platform macosx

if [ ! -f "$TMP/compiled/Assets.car" ]; then
  echo "ERROR: actool produced no Assets.car" >&2
  exit 1
fi
if ! xcrun --sdk macosx assetutil --info "$TMP/compiled/Assets.car" | grep -q 'NSAppearanceNameDarkAqua'; then
  echo "ERROR: Assets.car has no dark-aqua rendition — the dark tile would not be pure black" >&2
  exit 1
fi

RES="$APP/Contents/Resources"
INFO="$APP/Contents/Info.plist"
if [ ! -d "$RES" ] || [ ! -f "$INFO" ]; then
  echo "ERROR: $APP is not a valid .app bundle" >&2
  exit 1
fi

cp "$TMP/compiled/Assets.car" "$RES/Assets.car"
if ! /usr/libexec/PlistBuddy -c "Print :CFBundleIconName" "$INFO" >/dev/null 2>&1; then
  /usr/libexec/PlistBuddy -c "Add :CFBundleIconName string AppIcon" "$INFO"
else
  /usr/libexec/PlistBuddy -c "Set :CFBundleIconName AppIcon" "$INFO"
fi

echo "Injected appearance-aware app icon into $APP (Assets.car + CFBundleIconName=AppIcon)"
