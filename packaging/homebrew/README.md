# homebrew-repo

Homebrew tap for [Cinder](https://github.com/sky-jiangcheng/repotory) — the
local-first code project context base.

## What is in here

| Manifest | Package | Platform | Artifact |
|----------|---------|----------|----------|
| [`Casks/repotory.rb`](Casks/repotory.rb) | `repotory` | macOS | `repotory-darwin-<arch>.dmg` — the Wails desktop app |
| [`Casks/repotory-mcp.rb`](Casks/repotory-mcp.rb) | `repotory-mcp` | macOS | `repotory-mcp-<target>.tar.gz` — the MCP stdio server |
| [`Formula/repotory-mcp.rb`](Formula/repotory-mcp.rb) | `repotory-mcp` | macOS + Linux | `repotory-mcp-<target>.tar.gz` — the MCP stdio server |

### Deprecated aliases

`reponest` and `reponest-mcp` are the project's pre-rename names (the app was
called RepoNest until v2.0.0). They are kept as aliases so a command saved
before the rename keeps working — and they now serve the CURRENT release. The
previous version of those files pointed at RepoNest v1.8.0, so installing them
silently fetched a six-versions-old build; that is the failure this alias
policy exists to prevent.

Use the new names:

The desktop app is a GUI bundle, so it ships as a **Cask** and only exists for
macOS. The MCP server is a single static binary with no UI dependency, so it
installs cleanly on every platform Homebrew supports and ships as a **Formula**.

## Install

```bash
brew tap sky-jiangcheng/repo

# Desktop app (macOS)
brew install --cask sky-jiangcheng/repo/repotory

# MCP server (macOS and Linux)
brew install sky-jiangcheng/repo/repotory-mcp
```

After installing the MCP server, register it with an AI client:

```bash
claude mcp add reponest -- "$(which reponest-mcp)"
```

The desktop app is **not** required to use the MCP server. Both read the same
local SQLite database (see [data directory docs][datadir]), and both share one
`internal/service` implementation, so their behaviour cannot drift.

## Versioning

Manifest versions are derived from `wails.json` in the main repository, which
is the single source of truth. `scripts/bump-version.sh` there rewrites these
files, so a release bump does not need to be mirrored by hand here.

`sha256` values come from the assets GitHub actually serves, not from a local
build of the same version — a binary compiled on a different host is a
different file, and a digest taken from it would reject every real download.

## Maintenance

The manifests in this repository are generated from
[`packaging/homebrew/`](https://github.com/sky-jiangcheng/repotory/tree/master/packaging/homebrew)
in the main repository. **Edit them there**, then re-publish; editing here will
be overwritten on the next release.

The `tests.yml` workflow runs `brew install`, `brew audit --cask` and
`brew test` against every push, so a broken manifest fails here rather than in
a user's terminal.

[datadir]: https://github.com/sky-jiangcheng/repotory#数据目录
