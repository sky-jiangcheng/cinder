#!/usr/bin/env node
// repotory-init — one-command MCP registration for Repotory.
// ADR-0009 step one / TODO M4: detect the repotory-mcp binary, register it with
// the MCP clients found in this project (Claude Code / Cursor / VS Code /
// Windsurf), and optionally install the Claude Code SessionEnd handoff hook.
// Zero dependencies. Node >= 18.

import fs from "node:fs";
import path from "node:path";
import os from "node:os";

const SERVER_NAME = "repotory";
const BINARY_NAME = process.platform === "win32" ? "repotory-mcp.exe" : "repotory-mcp";
const HOOK_SCRIPT_REL = ".claude/hooks/repotory-handoff.sh";
// Forward slashes on every platform, deliberately: this path is written into a
// shell command string, and path.join would produce backslashes on Windows —
// the one place where this script and the Go twin (internal/agentinit) could
// disagree about the same file. The two are pinned to one fixture by
// scripts/check-agent-init-contract.mjs.
const BACKUP_SUFFIX = ".repotory-init.bak";

const RELEASES_URL = "https://github.com/sky-jiangcheng/repotory/releases";

const USAGE = `repotory-init — register Repotory's MCP server with your AI clients (ADR-0009)

Usage: node index.mjs [options]

Options:
  --client <name>  Comma-separated subset of: claude,cursor,vscode,windsurf,jetbrains
                   Default: every supported client (config files land only where the client dir exists)
  --bin <path>     Explicit path to the repotory-mcp binary (skips detection)
  --with-hook      Also install the Claude Code SessionEnd handoff hook
  --with-instructions  Also write the repotory block into AGENTS.md / CLAUDE.md
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
  const fromPath = searchPathFor(BINARY_NAME);
  if (fromPath) return fromPath;
  const home = os.homedir();
  const common =
    process.platform === "win32"
      ? [
          path.join(home, "scoop", "shims", BINARY_NAME),
          path.join(home, "scoop", "apps", "repotory-mcp", "current", BINARY_NAME),
        ]
      : [
          "/opt/homebrew/bin/repotory-mcp",
          "/usr/local/bin/repotory-mcp",
          "/usr/local/opt/repotory-mcp/bin/repotory-mcp",
        ];
  return common.find((p) => fs.existsSync(p)) ?? null;
}

function printBinaryGuidance() {
  console.log(`
Could not find the ${BINARY_NAME} binary. Install it with one of:

  macOS   brew tap sky-jiangcheng/repo && brew install --cask sky-jiangcheng/repo/repotory-mcp
  Linux   brew tap sky-jiangcheng/repo && brew install repotory-mcp
  Windows scoop bucket add repo https://github.com/sky-jiangcheng/scoop-repo && scoop install repo/repotory-mcp
  Anywhere: download repotory-mcp-<target> from ${RELEASES_URL}

Then re-run with --bin /path/to/${BINARY_NAME}, or re-run with --yes to write
the config with the bare command name "repotory-mcp" (works once installed).`);
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
# Repotory SessionEnd hook: write the handoff note when a session ends.
# Installed by repotory-init. Delete this file and the matching
# .claude/settings.json entry to uninstall.
command -v claude >/dev/null 2>&1 || exit 0
cd "\${CLAUDE_PROJECT_DIR:-$(dirname "$0")/../..}" || exit 0
[ -f .mcp.json ] || exit 0
claude -p --mcp-config .mcp.json \\
  'This session ended. Call repotory_handoff for the current project: a concise summary plus next_steps. If the project cannot be resolved, call repotory_context once first. Do nothing else.' \\
  >/dev/null 2>&1 || true
`;

function writeHookScript() {
  const file = HOOK_SCRIPT_REL;
  const existed = fs.existsSync(file);
  if (existed && fs.readFileSync(file, "utf8") === HOOK_SCRIPT) return "unchanged";
  if (options.dryRun) return existed ? "would-update" : "would-create";
  fs.mkdirSync(path.dirname(file), { recursive: true });
  if (existed) fs.copyFileSync(file, file + BACKUP_SUFFIX);
  fs.writeFileSync(file, HOOK_SCRIPT, { mode: 0o755 });
  return existed ? "updated" : "created";
}

function settingsNeedsHook(settings) {
  const entries = settings?.hooks?.SessionEnd;
  if (!Array.isArray(entries)) return true;
  return !entries.some((entry) =>
    (entry?.hooks ?? []).some(
      (h) => typeof h?.command === "string" && h.command.includes(HOOK_SCRIPT_REL),
    ),
  );
}

function writeHookSettings() {
  const file = path.join(".claude", "settings.json");
  const settings = readJsonOrNull(file) ?? {};
  if (!settingsNeedsHook(settings)) return "unchanged";
  const next = {
    ...settings,
    hooks: {
      ...settings.hooks,
      SessionEnd: [
        ...(settings.hooks?.SessionEnd ?? []),
        {
          hooks: [
            { type: "command", command: `"$CLAUDE_PROJECT_DIR"/${HOOK_SCRIPT_REL}` },
          ],
        },
      ],
    },
  };
  const result = writeJsonChanged(file, next);
  return result;
}

function installHook() {
  const a = writeHookScript();
  console.log(`  hook script: ${VERB[a].replace("<file>", HOOK_SCRIPT_REL)} -> ${HOOK_SCRIPT_REL}`);
  const b = writeHookSettings();
  console.log(
    `  SessionEnd hook: ${VERB[b].replace("<file>", path.join(".claude", "settings.json"))}`,
  );
  if (!options.dryRun && a !== "unchanged" && process.platform !== "win32") {
    try {
      fs.chmodSync(HOOK_SCRIPT_REL, 0o755);
    } catch {
      /* mode best-effort */
    }
  }
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

const INSTRUCTIONS_BODY = `## Repotory memory layer

This project's knowledge and session memory live in Repotory, a local SQLite
database, reachable through the repotory_* MCP tools.

- At the start of work, call repotory_context with the current repository path
  and read what it returns before you plan. Its first entry is the most recent
  session handoff, so the previous session's conclusions come first.
- Before you finish, call repotory_handoff for this project with the fields
  gotchas, decisions, changes, next_steps and a short summary.
- Record what a later session would otherwise re-learn the hard way \u2014 a
  constraint that is not visible in the code, a decision and the alternative
  it rejected, a trap you just hit. Do not record what the commit log already
  shows: a list of files touched is not a handoff, and the notes that bury the
  useful signal are the ones that stop the next session from reading them.

Installed by Repotory. To remove, delete everything between the repotory:begin
and repotory:end markers (this block is plain text in your repository, and it
will show up in git status until you commit or delete it).
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

// --------------------------------------------------------------------- main

const options = { client: null, bin: null, withHook: false, withInstructions: false, yes: false, dryRun: false };

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

  console.log("repotory-init — Repotory MCP registration (ADR-0009 / TODO M4)\n");

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
    console.log("\nSessionEnd handoff hook:");
    installHook();
  } else if (!options.yes && process.stdin.isTTY && requested.includes("claude")) {
    console.log(
      "\nTip: also install the Claude Code SessionEnd hook so handoffs happen\nautomatically at session end. Re-run with --with-hook to install it.",
    );
  }

  if (options.withInstructions) {
    installInstructions();
  }

  console.log(`\nDone. Reload the client window, then start a session with
repotory_context and end it with repotory_handoff (see SKILL.md).`);
}

main();
