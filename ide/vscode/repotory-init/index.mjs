#!/usr/bin/env node
// repotory-init — one-command MCP registration for Cinder.
// ADR-0009 step one / TODO M4: detect the cinder-mcp binary, register it with
// the MCP clients found in this project (Claude Code / Cursor / VS Code /
// Windsurf), and optionally install the Claude Code session hooks
// (SessionEnd handoff + SessionStart memory brief, ADR-0066).
// Zero dependencies. Node >= 18.

import fs from "node:fs";
import path from "node:path";
import os from "node:os";

const SERVER_NAME = "repotory";
// The MCP server binary, newest name first. `repotory-mcp` is the pre-rename
// name; every install still carries it as a symlink (ADR-0049 补注), so a
// machine that has not been updated yet resolves through the second entry.
const BINARY_NAMES =
  process.platform === "win32" ? ["cinder-mcp.exe", "repotory-mcp.exe"] : ["cinder-mcp", "repotory-mcp"];
const BINARY_NAME = BINARY_NAMES[0];
const HOOK_SCRIPT_REL = ".claude/hooks/repotory-handoff.sh";
const BRIEF_HOOK_SCRIPT_REL = ".claude/hooks/repotory-brief.sh";
// Forward slashes on every platform, deliberately: this path is written into a
// shell command string, and path.join would produce backslashes on Windows —
// the one place where this script and the Go twin (internal/agentinit) could
// disagree about the same file. The two are pinned to one fixture by
// scripts/check-agent-init-contract.mjs.
const BACKUP_SUFFIX = ".repotory-init.bak";

const RELEASES_URL = "https://github.com/sky-jiangcheng/cinder/releases";

const USAGE = `repotory-init — register Cinder's MCP server with your AI clients (ADR-0009)

Usage: node index.mjs [options]

Options:
  --client <name>  Comma-separated subset of: claude,cursor,vscode,windsurf,jetbrains
                   Default: every supported client (config files land only where the client dir exists)
  --bin <path>     Explicit path to the repotory-mcp binary (skips detection)
  --with-hook      Also install the Claude Code session hooks (SessionEnd handoff + SessionStart brief)
  --with-instructions  Also write the repotory block into AGENTS.md / CLAUDE.md
  --uninstall      Remove exactly what this script added (server entries, hook
                   entries + scripts, managed block); everything else stays
  --yes            Non-interactive: assume yes where a prompt would appear
  --dry-run        Show what would be written, change nothing
  -h, --help       Show this help

Exit codes: 0 registered / unchanged, 1 binary missing or config error, 2 bad usage.`;

// ---------------------------------------------------------------- utilities

function die(msg, code = 1) {
  console.error(`error: ${msg}`);
  process.exit(code);
}

function readJsonOrNull(file) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (err) {
    if (err.code === "ENOENT") return null;
    die(`${file} is not valid JSON (${err.message}); fix or remove it first`);
  }
}

/** Write JSON only when the serialized content changes. Back up before overwriting. */
function writeJsonChanged(file, next) {
  const serialized = JSON.stringify(next, null, 2) + "\n";
  const existed = fs.existsSync(file);
  if (existed && fs.readFileSync(file, "utf8") === serialized) return "unchanged";
  if (options.dryRun) return existed ? "would-update" : "would-create";
  fs.mkdirSync(path.dirname(file), { recursive: true });
  if (existed) fs.copyFileSync(file, file + BACKUP_SUFFIX);
  fs.writeFileSync(file, serialized);
  return existed ? "updated" : "created";
}

const VERB = {
  created: "created",
  updated: "updated (backup: <file>" + BACKUP_SUFFIX + ")",
  unchanged: "already registered, no change",
  "would-create": "would create (dry-run)",
  "would-update": "would update (dry-run)",
};

// ------------------------------------------------------------ binary search

function searchPathFor(binaryName) {
  const dirs = (process.env.PATH || "").split(path.delimiter).filter(Boolean);
  for (const dir of dirs) {
    const candidate = path.join(dir, binaryName);
    try {
      fs.accessSync(candidate, fs.constants.X_OK);
      return candidate;
    } catch {
      /* keep looking */
    }
  }
  return null;
}

function findBinary() {
  if (options.bin) {
    if (fs.existsSync(options.bin)) return path.resolve(options.bin);
    die(`--bin ${options.bin} does not exist`);
  }
  if (process.env.REPOTORY_MCP_BIN && fs.existsSync(process.env.REPOTORY_MCP_BIN)) {
    return path.resolve(process.env.REPOTORY_MCP_BIN);
  }
  for (const name of BINARY_NAMES) {
    const fromPath = searchPathFor(name);
    if (fromPath) return fromPath;
  }
  const home = os.homedir();
  const dirs =
    process.platform === "win32"
      ? [
          path.join(home, "scoop", "shims"),
          path.join(home, "scoop", "apps", "cinder-mcp", "current"),
          path.join(home, "scoop", "apps", "repotory-mcp", "current"),
        ]
      : ["/opt/homebrew/bin", "/usr/local/bin", "/usr/local/opt/cinder-mcp/bin", "/usr/local/opt/repotory-mcp/bin"];
  const common = dirs.flatMap((dir) => BINARY_NAMES.map((name) => path.join(dir, name)));
  return common.find((p) => fs.existsSync(p)) ?? null;
}

function printBinaryGuidance() {
  console.log(`
Could not find the ${BINARY_NAME} binary. Install it with one of:

  macOS   brew tap sky-jiangcheng/homebrew-repo && brew install --cask sky-jiangcheng/homebrew-repo/repotory-mcp
  Linux   brew tap sky-jiangcheng/homebrew-repo && brew install repotory-mcp
  Windows scoop bucket add repo https://github.com/sky-jiangcheng/scoop-repo && scoop install repo/repotory-mcp
  Anywhere: download repotory-mcp-<target> from ${RELEASES_URL}

Then re-run with --bin /path/to/${BINARY_NAME}, or re-run with --yes to write
the config with the bare command name "cinder-mcp" (works once installed).`);
}

// ------------------------------------------------------------ registrations

function mcpServersEntry(bin) {
  return { command: bin, args: [] };
}

/** Register into a {mcpServers:{}} or {servers:{}} style JSON file. */
function registerConfigFile({ label, file, key, bin }) {
  const current = readJsonOrNull(file) ?? {};
  const servers = current[key] && typeof current[key] === "object" ? current[key] : {};
  if (
    servers[SERVER_NAME] &&
    JSON.stringify(servers[SERVER_NAME]) === JSON.stringify(mcpServersEntry(bin))
  ) {
    console.log(`  ${label}: ${VERB.unchanged}`);
    return;
  }
  servers[SERVER_NAME] = mcpServersEntry(bin);
  const result = writeJsonChanged(file, { ...current, [key]: servers });
  console.log(`  ${label}: ${VERB[result].replace("<file>", file)} -> ${file}`);
}

function registerClaude(bin) {
  registerConfigFile({ label: "Claude Code (.mcp.json)", file: ".mcp.json", key: "mcpServers", bin });
}

function registerCursor(bin) {
  registerConfigFile({
    label: "Cursor (.cursor/mcp.json)",
    file: path.join(".cursor", "mcp.json"),
    key: "mcpServers",
    bin,
  });
}

function registerVsCode(bin) {
  registerConfigFile({
    label: "VS Code (.vscode/mcp.json)",
    file: path.join(".vscode", "mcp.json"),
    key: "servers",
    bin,
  });
}

function registerWindsurf(bin) {
  registerConfigFile({
    label: "Windsurf (~/.codeium/windsurf/mcp_config.json)",
    file: path.join(os.homedir(), ".codeium", "windsurf", "mcp_config.json"),
    key: "mcpServers",
    bin,
  });
}

function registerJetBrains() {
  console.log(`  JetBrains (manual): Settings -> Tools -> AI Assistant -> MCP -> Add,
    command: ${BINARY_NAME}   (auto-registration is not supported by JetBrains yet;
    see docs/features/ai-integration.md)`);
}

// ---------------------------------------------------------------- hook (M4)

const HOOK_SCRIPT = `#!/bin/sh
# Cinder SessionEnd hook: write the handoff note when a session ends.
# Installed by repotory-init. Delete this file and the matching
# .claude/settings.json entry to uninstall.
# ADR-0054: zero-credit first, model-driven fallback.
cd "\${CLAUDE_PROJECT_DIR:-$(dirname "$0")/../..}" || exit 0

# Primary path (ADR-0054): repotory-capture reads the transcript directly
# and writes a handoff note. Zero credit — survives credit exhaustion.
# Keep stdout/stderr visible: a failed capture must stay diagnosable.
# --strict makes a capture that wrote nothing (capture disabled, no
# matching project) exit non-zero, so a non-zero here falls through to the
# model-driven fallback below instead of reporting a silent success. Without
# it the hook would exit 0 on a capture that produced no note — the
# "installed but silent" failure this two-tier design exists to remove.
if command -v repotory-capture >/dev/null 2>&1 && repotory-capture --strict 2>&1; then
  exit 0
fi

# Fallback: model-driven structured handoff. Costs credit, but produces a
# more complete summary when credit is available.
command -v claude >/dev/null 2>&1 || exit 0
[ -f .mcp.json ] || exit 0
claude -p --mcp-config .mcp.json \\
  'This session ended. Call repotory_handoff for the current project: a concise summary plus next_steps. If the project cannot be resolved, call repotory_context once first. Do nothing else.' \\
  >/dev/null 2>&1 || true
`;

// The SessionStart brief hook (ADR-0066). The bytes MUST match the Go twin's
// briefHookScript exactly; scripts/check-agent-init-contract.mjs compares them.
const BRIEF_HOOK_SCRIPT = `#!/bin/sh
# Cinder SessionStart hook: inject the memory brief when a session starts.
# Installed by repotory-init. Delete this file and the matching
# .claude/settings.json entry to uninstall.
# ADR-0066: zero-model, read-only; a failure never breaks session start.
cd "\${CLAUDE_PROJECT_DIR:-$(dirname "$0")/../..}" || exit 0

# repotory-capture --brief prints the compact project brief (latest handoff,
# open todos, approved-page excerpts). Claude Code adds SessionStart stdout
# to the session context. No model call, no database write. Keep stderr
# visible so a broken brief stays diagnosable; every failure exits 0.
command -v repotory-capture >/dev/null 2>&1 || exit 0
repotory-capture --brief || exit 0
`;

function writeHookScript(file, content) {
  const existed = fs.existsSync(file);
  if (existed && fs.readFileSync(file, "utf8") === content) return "unchanged";
  if (options.dryRun) return existed ? "would-update" : "would-create";
  fs.mkdirSync(path.dirname(file), { recursive: true });
  if (existed) fs.copyFileSync(file, file + BACKUP_SUFFIX);
  fs.writeFileSync(file, content, { mode: 0o755 });
  return existed ? "updated" : "created";
}

function settingsNeedsHook(settings, event, scriptRel) {
  const entries = settings?.hooks?.[event];
  if (!Array.isArray(entries)) return true;
  return !entries.some((entry) =>
    (entry?.hooks ?? []).some(
      (h) => typeof h?.command === "string" && h.command.includes(scriptRel),
    ),
  );
}

// The event→script wiring, in append order. Must match the Go twin's
// hookEvents exactly.
const HOOK_EVENTS = [
  ["SessionEnd", HOOK_SCRIPT_REL],
  ["SessionStart", BRIEF_HOOK_SCRIPT_REL],
];

function writeHookSettings() {
  const file = path.join(".claude", "settings.json");
  const settings = readJsonOrNull(file) ?? {};
  const hooks = { ...settings.hooks };
  let changed = false;
  for (const [event, scriptRel] of HOOK_EVENTS) {
    if (!settingsNeedsHook(settings, event, scriptRel)) continue;
    hooks[event] = [
      ...(settings.hooks?.[event] ?? []),
      {
        hooks: [{ type: "command", command: `"$CLAUDE_PROJECT_DIR"/${scriptRel}` }],
      },
    ];
    changed = true;
  }
  if (!changed) return "unchanged";
  return writeJsonChanged(file, { ...settings, hooks });
}

function installHook() {
  for (const [rel, content] of [
    [HOOK_SCRIPT_REL, HOOK_SCRIPT],
    [BRIEF_HOOK_SCRIPT_REL, BRIEF_HOOK_SCRIPT],
  ]) {
    const a = writeHookScript(rel, content);
    console.log(`  hook script: ${VERB[a].replace("<file>", rel)} -> ${rel}`);
    if (!options.dryRun && a !== "unchanged" && process.platform !== "win32") {
      try {
        fs.chmodSync(rel, 0o755);
      } catch {
        /* mode best-effort */
      }
    }
  }
  const b = writeHookSettings();
  console.log(
    `  session hooks: ${VERB[b].replace("<file>", path.join(".claude", "settings.json"))}`,
  );
}

// ------------------------------------------------- instructions (ADR-0043)

const INSTRUCTIONS_BEGIN = "<!-- repotory:begin -->";
const INSTRUCTIONS_END = "<!-- repotory:end -->";

// Both, always — not one chosen per client. An instruction file nobody reads is
// inert: it produces no behaviour and no error. Writing both costs one extra
// file; picking per client costs a silent break the first time the user switches
// agent. The bytes MUST match internal/agentinit/instructions.go exactly —
// scripts/check-agent-init-contract.mjs compares the two against one fixture.
const INSTRUCTION_FILES = ["AGENTS.md", "CLAUDE.md"];

const INSTRUCTIONS_BODY = `## Cinder memory layer

This project's knowledge and session memory live in Cinder, a local SQLite
database. Every agent working here shares that one memory — Claude Code,
Cursor, VS Code, Windsurf, WorkBuddy, Qoder, Trae, DSH, or anything else that
reads this file — so what one session learned is what the next one starts
with.

- At the start of work, call repotory_context with the current repository path
  and read what it returns before you plan. Its first entry is the most recent
  session handoff, so the previous session's conclusions come first.
- After each meaningful work unit (a feature, a fix, a test run), call
  repotory_handoff with milestone=true to record the current state and what is
  still unfinished. This is a rolling checkpoint: if the session is killed
  mid-work, the last checkpoint is the next session's starting point.
- Before you finish, call repotory_handoff (without milestone) for a session
  summary with gotchas, decisions, changes, next_steps and a short summary.
- If you detect credits are running low, write a milestone handoff immediately
  before continuing.
- Record what a later session would otherwise re-learn the hard way — a
  constraint that is not visible in the code, a decision and the alternative
  it rejected, a trap you just hit. Do not record what the commit log already
  shows: a list of files touched is not a handoff, and the notes that bury the
  useful signal are the ones that stop the next session from reading them.
- If your client does not expose the repotory_* tools, run
  "cinder-capture --agent auto --cwd ." before you finish. It reads this
  session's transcript and writes the handoff with no model call, so the memory
  still closes without any per-client setup.
- For factual questions with a numeric or time answer (commit counts, lines
  changed, "yesterday's commits", dates, repo stats), call the stats tools
  first (query_daily_stats / get_project_stats / repotory_projects_stats), not
  search_notes or memory. Notes are prose; they do not carry commit data.
  Never guess a number from memory — without a tool result, say so instead.

Installed by Cinder into every project it collects, and refreshed on each scan.
To remove, delete everything between the repotory:begin and repotory:end
markers (this block is plain text in your repository, and it will show up in
git status until you commit or delete it).
`;

const INSTRUCTIONS_BLOCK = INSTRUCTIONS_BEGIN + "\n" + INSTRUCTIONS_BODY + INSTRUCTIONS_END + "\n";

/**
 * Place the managed block in one file's content without touching anything
 * outside it. Three shapes, in order: our block is already there (replace in
 * place, so running twice is byte-identical), the file has other content
 * (append after a blank line), there is no file (the block is the whole file).
 */
function upsertInstructions(content, hadFile) {
  const begin = content.indexOf(INSTRUCTIONS_BEGIN);
  let end = content.indexOf(INSTRUCTIONS_END);
  if (begin >= 0 && end > begin) {
    end += INSTRUCTIONS_END.length;
    // Keep whatever trailing newline the old block had, so an idempotent second
    // run does not shift the bytes after the marker.
    const tail = content.slice(end);
    let nl = 0;
    while (nl < tail.length && (tail[nl] === "\n" || tail[nl] === "\r")) nl += 1;
    return content.slice(0, begin) + INSTRUCTIONS_BLOCK + tail.slice(nl);
  }
  if (!hadFile || content.trim() === "") return INSTRUCTIONS_BLOCK;
  return content.replace(/\n+$/, "") + "\n\n" + INSTRUCTIONS_BLOCK;
}

function installInstructions() {
  console.log("\nAgent instructions (ADR-0043):");
  for (const rel of INSTRUCTION_FILES) {
    let hadFile = true;
    let raw = "";
    try {
      raw = fs.readFileSync(rel, "utf8");
    } catch (err) {
      if (err.code !== "ENOENT") die(`${rel} cannot be read (${err.message})`);
      hadFile = false;
    }
    const next = upsertInstructions(raw, hadFile);
    if (hadFile && raw === next) {
      console.log(`  ${rel}: already contains the repotory block, no change`);
      continue;
    }
    if (options.dryRun) {
      console.log(`  ${rel}: would ${hadFile ? "update" : "create"} (dry-run)`);
      continue;
    }
    if (hadFile) fs.copyFileSync(rel, rel + BACKUP_SUFFIX);
    fs.writeFileSync(rel, next);
    console.log(
      `  ${rel}: ${hadFile ? VERB.updated.replace("<file>", rel) : VERB.created} -> ${rel}`,
    );
  }
}

// ---------------------------------------------------- uninstall (ADR-0067)
// Removes exactly what this script added, and nothing else. Semantics are
// pinned to the Go twin (internal/agentinit/unregister.go):
//   - other servers / hooks / file content are never touched;
//   - a hook script whose bytes were edited after install is left in place
//     with a warning (it is the user's edit, not ours to delete);
//   - an instruction file that held nothing but the block is deleted;
//   - missing files / entries are unchanged, so a second run is a no-op;
//   - every overwrite backs up first, same as install.

function removeMCPEntry({ label, file, key }) {
  const current = readJsonOrNull(file);
  if (!current || !current[key] || typeof current[key] !== "object" || !current[key][SERVER_NAME]) {
    console.log(`  ${label}: not registered, no change`);
    return;
  }
  const servers = { ...current[key] };
  delete servers[SERVER_NAME];
  const result = writeJsonChanged(file, { ...current, [key]: servers });
  if (result.startsWith("would-")) {
    console.log(`  ${label}: would remove the repotory entry (dry-run)`);
    return;
  }
  console.log(`  ${label}: removed (backup: ${file}${BACKUP_SUFFIX}) -> ${file}`);
}

function removeHookSettings() {
  const file = path.join(".claude", "settings.json");
  const settings = readJsonOrNull(file);
  if (!settings || !settings.hooks || typeof settings.hooks !== "object") {
    console.log(`  session hooks: not installed, no change`);
    return;
  }
  const hooks = { ...settings.hooks };
  let changed = false;
  for (const [event, scriptRel] of HOOK_EVENTS) {
    const entries = hooks[event];
    if (!Array.isArray(entries)) continue;
    const kept = entries.filter(
      (entry) =>
        !(entry?.hooks ?? []).some(
          (h) => typeof h?.command === "string" && h.command.includes(scriptRel),
        ),
    );
    if (kept.length === entries.length) continue;
    changed = true;
    if (kept.length === 0) delete hooks[event];
    else hooks[event] = kept;
  }
  if (!changed) {
    console.log(`  session hooks: not installed, no change`);
    return;
  }
  const next = { ...settings };
  if (Object.keys(hooks).length === 0) delete next.hooks;
  else next.hooks = hooks;
  const result = writeJsonChanged(file, next);
  if (result.startsWith("would-")) {
    console.log(`  session hooks: would remove our entries (dry-run)`);
    return;
  }
  console.log(`  session hooks: removed (backup: ${file}${BACKUP_SUFFIX})`);
}

function removeHookScript(rel, installed) {
  if (!fs.existsSync(rel)) {
    console.log(`  hook script: ${rel} not present, no change`);
    return;
  }
  if (fs.readFileSync(rel, "utf8") !== installed) {
    console.warn(
      `  warning: ${rel} was modified after install; left in place (remove it manually if intended)`,
    );
    return;
  }
  if (options.dryRun) {
    console.log(`  hook script: would remove ${rel} (dry-run)`);
    return;
  }
  fs.unlinkSync(rel);
  console.log(`  hook script: removed ${rel}`);
}

function uninstallInstructions() {
  console.log("\nAgent instructions (ADR-0043):");
  for (const rel of INSTRUCTION_FILES) {
    if (!fs.existsSync(rel)) {
      console.log(`  ${rel}: not present, no change`);
      continue;
    }
    const raw = fs.readFileSync(rel, "utf8");
    const begin = raw.indexOf(INSTRUCTIONS_BEGIN);
    let end = raw.indexOf(INSTRUCTIONS_END);
    if (begin < 0 || end <= begin) {
      console.log(`  ${rel}: no repotory block, no change`);
      continue;
    }
    end += INSTRUCTIONS_END.length;
    const tail = raw.slice(end);
    let nl = 0;
    while (nl < tail.length && (tail[nl] === "\n" || tail[nl] === "\r")) nl += 1;
    const next = raw.slice(0, begin) + tail.slice(nl);
    if (next.trim() === "") {
      if (options.dryRun) {
        console.log(`  ${rel}: would remove, the file holds only the repotory block (dry-run)`);
        continue;
      }
      fs.unlinkSync(rel);
      console.log(`  ${rel}: removed (the file held only the repotory block)`);
      continue;
    }
    if (options.dryRun) {
      console.log(`  ${rel}: would remove the block (dry-run)`);
      continue;
    }
    fs.copyFileSync(rel, rel + BACKUP_SUFFIX);
    fs.writeFileSync(rel, next.replace(/\n+$/, "") + "\n");
    console.log(`  ${rel}: block removed (backup: ${rel}${BACKUP_SUFFIX})`);
  }
}

function uninstallAll() {
  const requested = options.client ?? ALL_CLIENTS;
  console.log("Removing MCP server entries:");
  if (requested.includes("claude"))
    removeMCPEntry({ label: "Claude Code (.mcp.json)", file: ".mcp.json", key: "mcpServers" });
  if (requested.includes("cursor"))
    removeMCPEntry({ label: "Cursor (.cursor/mcp.json)", file: path.join(".cursor", "mcp.json"), key: "mcpServers" });
  if (requested.includes("vscode"))
    removeMCPEntry({ label: "VS Code (.vscode/mcp.json)", file: path.join(".vscode", "mcp.json"), key: "servers" });
  if (requested.includes("windsurf"))
    removeMCPEntry({
      label: "Windsurf (~/.codeium/windsurf/mcp_config.json)",
      file: path.join(os.homedir(), ".codeium", "windsurf", "mcp_config.json"),
      key: "mcpServers",
    });
  if (requested.includes("jetbrains")) registerJetBrains();

  console.log("\nSession hooks (SessionEnd handoff + SessionStart brief):");
  removeHookSettings();
  removeHookScript(HOOK_SCRIPT_REL, HOOK_SCRIPT);
  removeHookScript(BRIEF_HOOK_SCRIPT_REL, BRIEF_HOOK_SCRIPT);

  uninstallInstructions();

  console.log("\nDone. The database, notes and binaries are untouched — only the\nclient wiring this script installed was removed.");
}

// --------------------------------------------------------------------- main

const options = { client: null, bin: null, withHook: false, withInstructions: false, yes: false, dryRun: false, uninstall: false };

function parseArgs(argv) {
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    switch (arg) {
      case "--client": {
        const v = argv[++i];
        if (!v) die("--client requires a value", 2);
        options.client = v.split(",").map((s) => s.trim().toLowerCase());
        break;
      }
      case "--bin":
        options.bin = argv[++i];
        if (!options.bin) die("--bin requires a value", 2);
        break;
      case "--with-hook":
        options.withHook = true;
        break;
      case "--with-instructions":
        options.withInstructions = true;
        break;
      case "--uninstall":
        options.uninstall = true;
        break;
      case "--yes":
        options.yes = true;
        break;
      case "--dry-run":
        options.dryRun = true;
        break;
      case "-h":
      case "--help":
        console.log(USAGE);
        process.exit(0);
      default:
        die(`unknown option: ${arg}\n${USAGE}`, 2);
    }
  }
}

const ALL_CLIENTS = ["claude", "cursor", "vscode", "windsurf", "jetbrains"];

function main() {
  parseArgs(process.argv.slice(2));

  if (options.client) {
    for (const c of options.client) {
      if (!ALL_CLIENTS.includes(c)) die(`unknown client "${c}" (known: ${ALL_CLIENTS.join(", ")})`, 2);
    }
  }

  console.log("repotory-init — Cinder MCP registration (ADR-0009 / TODO M4)\n");

  if (options.uninstall) {
    uninstallAll();
    return;
  }

  // --with-hook only makes sense for Claude Code; keep it regardless of --client.
  let bin = findBinary();
  if (!bin) {
    printBinaryGuidance();
    if (!options.yes) process.exit(1);
    console.log(`--yes given: writing configs with the bare command name "${BINARY_NAME}".\n`);
    bin = BINARY_NAME;
  } else {
    console.log(`Found repotory-mcp: ${bin}\n`);
  }

  const requested = options.client ?? ALL_CLIENTS;
  console.log("Registering MCP server:");
  if (requested.includes("claude")) registerClaude(bin);
  if (requested.includes("cursor")) registerCursor(bin);
  if (requested.includes("vscode")) registerVsCode(bin);
  if (requested.includes("windsurf")) registerWindsurf(bin);
  if (requested.includes("jetbrains")) registerJetBrains();

  if (options.withHook) {
    console.log("\nSession hooks (SessionEnd handoff + SessionStart brief):");
    installHook();
  } else if (!options.yes && process.stdin.isTTY && requested.includes("claude")) {
    console.log(
      "\nTip: also install the Claude Code session hooks so every session starts\nwith the memory brief and ends with a handoff. Re-run with --with-hook to install them.",
    );
  }

  if (options.withInstructions) {
    installInstructions();
  }

  console.log(`\nDone. Reload the client window, then start a session with
repotory_context and end it with repotory_handoff (see SKILL.md).`);
}

main();
