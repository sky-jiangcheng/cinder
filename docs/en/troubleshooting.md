---
title: Troubleshooting
order: 10
---

# Troubleshooting

## Common issues

### The dashboard shows "Cinder has not found any Git repositories yet"

**Cause**: No scan roots are configured, or the configured directories contain no Git repositories.

**Fix**: Go to **Settings → Scan Roots**, add directories containing Git repositories, then click **Rescan**.

> The first scan only registers repository names; it does not scan historical commit data.

---

### A repository card shows only the name, with no statistics

**Cause**: The repository is not starred, or history was not backfilled after starring.

**Fix**:
1. Click the star on the card to star the repository
2. Click the **Backfill History** button and wait for the git log backfill to finish

---

### Statistics show zero

**Cause**: History has not been backfilled yet, or there are no commits on the selected date.

**Fix**: Click **Backfill History** on the repository card; the app scans the last 365 days of git log for that repository. Make sure git is installed and on the PATH (when launched from Finder/Dock, the app automatically supplements common PATH entries).

---

### How do I find a repository quickly?

The dashboard search box searches repositories / notes / todos together; or use the `⌘/Ctrl+K` command palette (see [Command Palette](features/command-palette.md)). In the search results you can click the star directly to toggle the starred state.

---

### How do I adjust project grouping?

When multiple repositories are grouped together incorrectly (or the opposite), open the project detail page and use the header action buttons:

- **Split down**: split a multi-repository project into one project per repository (the original project keeps the notes and todos of the first repository)
- **Merge up**: merge sibling projects under the same parent directory (repositories, notes, and todos move along)

Both operations run in a single transaction and never leave a half-completed state on failure.

---

### Why doesn't knowledge base search find two-character Chinese terms?

The FTS5 trigram index matches a minimum of 3 characters; shorter queries automatically fall back to a LIKE full-table scan (they still hit, just without relevance ranking). No action needed.

---

### Knowledge sources not importing?

1. Check the trigger mode: only `claude` runs automatically at startup with `auto_import` (off by default); the other five must be triggered manually via **Settings → Plugins**
2. `openclaw` / `hermes` need a target project configured first (`openclaw_project` / `hermes_project`); when unset they skip silently and only show up in the `skipped` count
3. See [Knowledge Source Import](plugins/overview.md) for details

> The in-process script-plugin runtime (yaegi) was removed in C11: the `plugins/` directory is no longer loaded — delete any old script plugins it may still contain.

---

## Log file locations

| Platform | Log path |
|------|---------|
| macOS | `~/Library/Logs/repotory.log` |
| Linux | `$XDG_STATE_HOME/repotory/repotory.log` (default `~/.local/state/repotory/repotory.log`) |
| Windows | `%APPDATA%\repotory\logs\repotory.log` |

Logs also record the PATH environment and plugin runtime state; attach them when reporting an issue.

## Data safety notes

- Cinder is a **local-first** desktop app: it listens on no network ports and uploads no data; statistics are read through the local `git` CLI
- The database and configuration live in the user application data directory (see [Getting Started](getting-started.md) for the location); uninstalling does not remove them automatically. For backup, migration, and full removal, see [Data and Backup](data-management.md)
- Desktop WebView responses carry security headers such as CSP (`default-src 'self'` and others)

## Reporting issues

Found a bug or have a feature request? Please open it on [GitHub Issues](https://github.com/sky-jiangcheng/repotory/issues) (include the version, platform, and log excerpts). For security vulnerabilities, use the [private reporting channel](https://github.com/sky-jiangcheng/repotory/security/advisories/new).
