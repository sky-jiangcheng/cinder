# packaging/

Homebrew and Scoop manifests for Cinder release artifacts.

The release workflow (`.github/workflows/release.yml`) publishes these assets per
tag, and the manifests below point at them:

| Asset | Platform | Consumers |
|-------|----------|-----------|
| `repotory-darwin-arm64.dmg` / `repotory-darwin-amd64.dmg` | macOS desktop app | Homebrew Cask |
| `repotory-linux-amd64.tar.gz` | Linux desktop app | manual |
| `repotory-windows-amd64.zip` | Windows desktop app | Scoop |
| `repotory-mcp-darwin-arm64.tar.gz` / `repotory-mcp-darwin-amd64.tar.gz` | macOS MCP server | Homebrew Cask |
| `repotory-mcp-linux-amd64.tar.gz` | Linux MCP server | Homebrew Formula |
| `repotory-mcp-windows-amd64.zip` | Windows MCP server | Scoop |
| `repotory-server-darwin-arm64.tar.gz` / `repotory-server-darwin-amd64.tar.gz` | macOS headless server + web UI | manual (deep-link enhancement) |
| `repotory-server-linux-amd64.tar.gz` | Linux headless server + web UI | manual (deep-link enhancement) |
| `repotory-server-windows-amd64.zip` | Windows headless server + web UI | manual (deep-link enhancement) |

The desktop app is macOS/Windows/Linux, but only macOS has a real installer
artifact (`.dmg`); Linux ships a bare tarball and Windows a zip. The MCP server
is a pure Go / zero-CGO binary, so it installs cleanly everywhere via a Formula
or Cask `binary` stanza. The `repotory-server-*` bundles carry the binary plus
its `web/` directory; the JetBrains plugin passes `--web-dist <bundle>/web`
when it starts one, so result deep links work without any manual flag.

## Version is never edited by hand

`wails.json` → `info.productVersion` is the single source of truth. After
`scripts/bump-version.sh` bumps it, run:

```bash
./scripts/update-manifests.sh
```

which stamps the new version into every manifest below. `scripts/bump-version.sh`
should call it automatically — see the note in that script.

## sha256 fields are automated

No release artifact exists for a given tag until the release workflow runs, so
the `sha256` fields ship as explicit `__FILL_SHA256_*__` placeholders. Homebrew
and Scoop both hard-fail on a mismatch, which is the behaviour we want: a
placeholder must never be mistaken for a verified hash.

The **fill-sha256 job in `.github/workflows/release.yml`** closes the loop
automatically: after the release is published it reads the per-asset digest the
GitHub Release API reports for the tag, writes
`assets/releases/v<ver>/SHA256SUMS`, runs `scripts/update-manifests.sh
--fill-sha256`, verifies that no placeholder survived **and that every digest
in packaging/ belongs to this release** (a stale digest from the previous
version fails the job), then commits the result back to master as
`chore(packaging): fill sha256 for v<ver>`.

The same fill is available locally against an already-published release:

```bash
# digests straight from the release API (recommended; anonymous, no download)
./scripts/update-manifests.sh --fill-sha256 --from-api

# or from a locally produced checksum file
./scripts/build-release-assets.sh
./scripts/update-manifests.sh --fill-sha256
```

The two paths write different cache files (`SHA256SUMS.api` vs `SHA256SUMS`),
so they never clobber each other. Digests must come from the **published
release**, not from a local build of the same version: a binary compiled on a
different host (or with different flags) is a different file, and a manifest
carrying its digest would reject every real download.

## Where these are published

| | Repository | User command |
|---|---|---|
| Homebrew | [sky-jiangcheng/homebrew-repo](https://github.com/sky-jiangcheng/homebrew-repo) | `brew tap sky-jiangcheng/repo` |
| Scoop | [sky-jiangcheng/scoop-repo](https://github.com/sky-jiangcheng/scoop-repo) | `scoop bucket add repo https://github.com/sky-jiangcheng/scoop-repo` |

The tap keeps its own CI (`brew tap` → `brew install` → verify the installed
layout) so a broken manifest fails there rather than in a user's terminal. The
bucket validates each manifest's `hash` against the digest GitHub reports for
the release asset.

**This directory is the source of truth for both repositories, and the copy is
automated.** `scripts/sync-packaging.sh` pushes these manifests — and the tap's
and the bucket's own CI definitions, kept under `packaging/homebrew/` and
`packaging/scoop/` — to the two repositories; `release.yml`'s `sync-packaging`
job runs it after every release. Do not edit the tap or the bucket by hand:
the next release overwrites them.

That automation is recent. Until it existed, this paragraph claimed the copy
happened "on every release" and nothing did it — the tap served the pre-rename
RepoNest v1.8.0 manifests while the product shipped v2.14.0, so
`brew install --cask repotory` did not exist at all. Two defects were sitting
in these files the whole time, and only the tap's CI surfaced them once the
manifests actually reached it:

- the casks carried a `license "MIT"` stanza, which is a **Formula** method —
  `brew install --cask` rejects the whole definition;
- the Scoop alias manifests carried `#` comment lines, which JSON cannot parse.

`scripts/check-packaging-manifests.sh` guards both classes here, before
anything is pushed.

## Trying a manifest before publishing

```bash
# Ruby syntax only (no Homebrew needed)
ruby -c homebrew/Casks/repotory.rb

# JSON syntax only
python3 -m json.tool scoop/repotory-mcp.json > /dev/null
```

Both are safe to run in CI on every change.

## Cutting a release

```bash
# 1. Bump the version (SSOT is wails.json; this syncs package.json,
#    package-lock.json, version.go and every manifest below).
./scripts/bump-version.sh 1.8.0

# 2. Build what this host can build — the MCP server for all four targets.
#    The Wails desktop shell is skipped with a printed reason: it needs GTK on
#    Linux, Xcode on macOS and MSVC on Windows, so it comes from CI instead.
./scripts/build-release-assets.sh

# 3. sha256 is filled automatically: after CI publishes the release, the
#    fill-sha256 job commits `chore(packaging): fill sha256 for v1.8.0` to
#    master. To fill locally instead:
#      ./scripts/update-manifests.sh --fill-sha256 --from-api

# 4. Commit, tag, push. The tag is what makes release.yml run on the native
#    runners and produce the desktop artifacts.
git add -A && git commit -m "chore: release 1.8.0"
git tag -a v1.8.0 -m "Release v1.8.0"
git push github master --tags

# 5. After CI publishes the desktop artifacts, the fill-sha256 job closes the
#    loop automatically (see "sha256 fields are automated" above). Nothing to
#    do by hand unless the job fails - it fails loudly on a missing asset or
#    a stale digest.
```

For v1.8.0 all eight digests are filled in and match what the release
actually serves. A manifest still holding `__FILL_SHA256_*__` is one Homebrew
and Scoop will refuse to install from — deliberate, since a missing digest
should fail loudly rather than silently install an unverified binary.

Digests must come from the **published release**, not from a local build of
the same version: a binary compiled on a different host (or with different
flags) is a different file, and a manifest carrying its digest would reject
every real download. Use the `digest` field the GitHub API returns for each
release asset, or download the assets and run `sha256sum` on them.

## Pruning old releases

`scripts/prune-releases.sh` removes historical releases and their assets
(~40 releases, >1 GB as of 1.8.0). It is dry-run by default, always keeps the
newest `--keep` releases, and leaves git tags alone unless `--delete-tags` is
passed. It needs `gh` and an authenticated session.

```bash
./scripts/prune-releases.sh --list              # inventory
./scripts/prune-releases.sh --keep 3            # show what would go
./scripts/prune-releases.sh --keep 3 --yes      # do it
```
