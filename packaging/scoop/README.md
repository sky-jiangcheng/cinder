# scoop-repo

Scoop bucket for [Cinder](https://github.com/sky-jiangcheng/repotory) — the
local-first code project context base.

## What is in here

| Manifest | Package | Artifact |
|----------|---------|----------|
| [`bucket/repotory.json`](bucket/repotory.json) | `repotory` | `repotory-windows-amd64.zip` — the Wails desktop app |
| [`bucket/repotory-mcp.json`](bucket/repotory-mcp.json) | `repotory-mcp` | `repotory-mcp-windows-amd64.zip` — the MCP stdio server |

### Deprecated aliases

`reponest` and `reponest-mcp` are the project's pre-rename names (the app was
called RepoNest until v2.0.0). They are kept as aliases so a command saved
before the rename keeps working — and they now serve the CURRENT release. The
previous version of those files pointed at RepoNest v1.8.0, so installing them
silently fetched a six-versions-old build; that is the failure this alias
policy exists to prevent.

Use the new names:

## Install

```powershell
scoop bucket add repo https://github.com/sky-jiangcheng/scoop-repo

# Desktop app
scoop install repo/repotory

# MCP server
scoop install repo/repotory-mcp
```

After installing the MCP server, register it with an AI client:

```powershell
claude mcp add repotory -- (Get-Command repotory-mcp).Source
```

The desktop app is **not** required to use the MCP server. Both read the same
local SQLite database, and both share one `internal/service` implementation, so
their behaviour cannot drift.

## Updates

Each manifest carries a `checkver` that reads the newest release tag from the
GitHub API and an `autoupdate` block that rewrites the download URL from it. To
pull in a new version:

```powershell
scoop update
```

Scoop hashes the downloaded file locally when `autoupdate` has no `hash` block,
so a manifest whose `hash` has not been refreshed will still update cleanly.

## Maintenance

The manifests in `bucket/` are generated from
[`packaging/scoop/`](https://github.com/sky-jiangcheng/repotory/tree/master/packaging/scoop)
in the main repository. **Edit them there**, then re-publish; editing here will
be overwritten on the next release.

`hash` values come from the assets GitHub actually serves, not from a local
build of the same version — a binary compiled on a different host is a
different file, and a digest taken from it would reject every real download.
