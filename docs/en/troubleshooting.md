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

### Broke something or deleted it by accident — how do I go back?

1. **Look at the built-in snapshots first.** `backups/` in the data directory holds two sets: one daily file `repotory-<YYYY-MM-DD>.db` (14 kept) and intraday rolling snapshots `repotory-<YYYYMMDD-HHMM>.db` (written only when the last one is 30+ minutes old AND something was written since; 6 kept). The first is "before the app first opened today", the second is "the last few hours".
2. **A restore never replaces the live database** — that is deliberate:
   ```bash
   cinder restore --from <snapshot file> --to <new path>
   ```
   It verifies the source, writes the target atomically, fails if the target already exists, and **refuses** the live database path. Quit Cinder afterwards and move the new file into place yourself. The desktop binary lives at `/Applications/Cinder.app/Contents/MacOS/Cinder`.
3. **One bad note is not a reason to restore the whole database**: every edit leaves the pre-edit state in the note's version history. Try that first; it costs far less.

---

## Log file locations

| Platform | Log directory |
|------|---------|
| macOS | `~/Library/Logs/` |
| Linux | `$XDG_STATE_HOME/repotory/` (default `~/.local/state/repotory/`) |
| Windows | `%APPDATA%\repotory\logs\` |

**Each of the three processes writes its own file** (ADR-0055 D4: `slog` JSON lines, 5 MiB rotation, one `.1` backup):

| Process | File |
|------|------|
| Cinder Desktop (Wails) | `repotory.log` |
| `cinder-mcp` (stdio service used by IDEs) | `repotory-mcp.log` |
| `cinder-server` (headless HTTP) | `repotory-server.log` |

The MCP process keeps **stdout for JSON-RPC only**; logs go to the file or stderr. When a client does not capture stderr, `repotory-mcp.log` is the source of truth. Entries carry `time` / `level` / `msg` and `request_id`; HTTP responses echo `X-Request-ID`, and one id ties together "tool call → service → external LLM / vector store". PATH and plugin runtime state are logged too — attach the relevant file when reporting an issue.

**Quick self-check**: in headless mode open `http://127.0.0.1:18765/health` (`status` / `version` / `core_version` plus `db_size_bytes` / `compile_jobs_failed_24h` / `last_backup_at`); on the MCP side run `repotory_integrity` for the six data-consistency checks and `repotory_agent_score` for install readiness. Tool failures now set `IsError=true` with `code` and `request_id` in `structuredContent` — include them in a bug report.

## Data safety notes

- Cinder is **local-first**: the desktop app itself listens on no network ports; in headless mode `cinder-server` serves a local HTTP API on `127.0.0.1:18765` only and is never exposed to the LAN. Data stays on the machine unless remote backup (S3 and friends) or an external AI endpoint is explicitly configured; statistics are read through the local `git` CLI
- The database and configuration live in the user application data directory (see [Getting Started](getting-started.md) for the location); uninstalling does not remove them automatically. For backup, migration, and full removal, see [Data and Backup](data-management.md)
- Desktop WebView responses carry security headers such as CSP (`default-src 'self'` and others)

## Reporting issues

Found a bug or have a feature request? Please open it on [GitHub Issues](https://github.com/sky-jiangcheng/cinder/issues) (include the version, platform, and log excerpts). For security vulnerabilities, use the [private reporting channel](https://github.com/sky-jiangcheng/cinder/security/advisories/new).
