# ADR-0076: Appearance-aware app icon — paper in light, pure black in dark

- Status: **Accepted (2026-11-24, user asked the desktop app icon to follow the system like Claude's, with a pure-black background in dark mode)**
- Date: 2026-11-24
- Related: [ADR-0050](0050-family-design-standard.md) (family palette), [ADR-0052](0052-the-knot-brand-mark.md) (The Knot master and the four domains), `scripts/generate-icons.mjs`, `scripts/apply-macos-app-icon.sh`

## Background

Wails v2 reads a single `build/appicon.png`, emits one `iconfile.icns` and points `CFBundleIconFile` at it. One bitmap has no light/dark appearance slots, so modern macOS has to guess — which can leave the Dock and Spotlight showing different icons. The user asked for a Claude-like desktop icon that follows the system: **pure black** in the current dark mode.

Constraints stay fixed: `build/icon.svg` remains the single source of truth and the knot's geometry, paths and two colour tokens do not change. Only the OS app domain changes.

## Facts and decisions

1. **New modern-macOS icon subdomain**: `build/AppIcon.icon/` is derived from the master by the generator. Its appearance settings live in `icon.json`; the artwork is one transparent `knot.svg` scaled to 0.8 so Icon Composer owns the safe area and outer mask.
   - Light (Aqua): `#F7F4EE` (brand paper) field, `#34322D` (brand ink) knot.
   - Dark (Dark Aqua): **`#000000` pure black** field, `#F7F4EE` knot.
   - No glass, shadow or translucency — the silhouette stays primary and the black stays truly black.
2. **Legacy fallback becomes pure black**: the Wails `appicon.png`, the `build/icons/` size ladder, `icon.ico` and `iconfile.icns` use a black squircle with a paper knot. Older macOS dark mode therefore gets black immediately; the trade-off is that older macOS light mode also sees black. Modern systems are covered by decision 3.
3. **Post-build injection**: after `wails build` and before signing, `scripts/apply-macos-app-icon.sh` compiles the `.icon` with `xcrun actool`, copies `Assets.car` into `.app/Contents/Resources/` and sets `CFBundleIconName=AppIcon` in `Info.plist`. `CFBundleIconFile` remains as the classic fallback. The script asserts `NSAppearanceNameDarkAqua` with `assetutil`; if the dark rendition is missing, the release fails instead of shipping silently.
4. **Sync gate covers the source**: `icon.json` and `knot.svg` are committed and byte-compared by `generate-icons.mjs --check --tracked-only`, under the same discipline as favicons and IDE icons. Hand edits are caught in CI.
5. **No framework migration**: the icon does not justify moving to Wails v3. If v3 lands later, use its native `appicon.icon` support and delete the post-build script.

## Consequences

- Gained: a pure-black Dock icon in dark mode and a paper one in light mode; one source of truth; a usable black fallback on older macOS.
- Lost: the macOS CI runner needs Xcode 26+ `actool`; a missing tool fails the release rather than degrading quietly. Tinted and Clear appearances are not custom-designed this round — the system derives them from the existing layers.
- Boundaries: Windows / Linux OS icons, favicons and IDE plugin icons are unchanged; the in-app `BrandMark` stays the master's ink field with a paper knot.

## Reversal triggers

- Wails v3 adoption → replace the post-build script with native Icon Composer support.
- A request for dedicated Tinted / Clear renditions → add layers and appearance specialisations in `icon.json`, not in the master.
- Real complaints about the black fallback in older macOS light mode → revisit the classic fallback colour; pure black in dark mode remains the priority.

## Landing record

- 2026-11-24: generator now derives the `.icon` and the black classic fallback; `scripts/apply-macos-app-icon.sh` was added and wired after the Wails build in `release.yml`. Local `actool` compilation, `assetutil` dark-rendition assertion, injection into a simulated `.app`, `ictool` light/dark previews, `generate-icons --check --tracked-only` and `check-brand.mjs` all passed.
