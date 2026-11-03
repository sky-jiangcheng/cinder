#!/usr/bin/env node
// Checks that the CLI wiring script and the Go twin the desktop app uses
// (internal/agentinit) produce the SAME files.
//
// Why this exists. The desktop app ships one self-contained binary with no
// node, so the "connect your AI client" button had to be written in Go; the
// terminal path stays a node script. Two implementations of one wire format
// drift, and the drift is invisible from either side: the button says
// "registered", the CLI says "registered", and Claude Code sees nothing.
//
// So both sides are pinned to one fixture (internal/agentinit/testdata/
// contract.json). This script runs the mjs in a temp directory and compares
// its output against that fixture; the Go test
// (TestContractFixture) does the same for the Go side. Change one without the
// other and one of the two goes red.
//
// TWO copies of the script exist, and both are checked here:
//   scripts/repotory-init/index.mjs       — the source of truth
//   ide/vscode/repotory-init/index.mjs    — the copy the VSIX ships
// The second is regenerated from the first by ide/vscode/scripts/sync-init.mjs
// (and CI fails on drift), but "CI would catch it" is not a reason to leave
// the shipped installer unchecked here: the extension would be handing users
// a file the desktop app no longer agrees with.
//
// The check writes only inside a temp directory. It never touches the user's
// real client configuration.

import { execFileSync } from 'node:child_process';
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

// The check runs from the repo root (like the other scripts/ checks), but the
// wiring script itself is executed inside a temp directory — so its path has
// to be absolute before the child's cwd changes.
const ROOT = process.cwd();
const SCRIPTS = [
  { label: 'scripts/repotory-init/index.mjs', path: resolve(ROOT, 'scripts/repotory-init/index.mjs') },
  { label: 'ide/vscode/repotory-init/index.mjs', path: resolve(ROOT, 'ide/vscode/repotory-init/index.mjs') },
];
const FIXTURE = resolve(ROOT, 'internal/agentinit/testdata/contract.json');

// The files a registration produces, relative to the project directory.
const PRODUCED = [
  '.mcp.json',
  '.cursor/mcp.json',
  '.vscode/mcp.json',
  '.claude/settings.json',
  // ADR-0043: the managed instruction block lands in both instruction files.
  'AGENTS.md',
  'CLAUDE.md',
];
const HOOK_SCRIPT = '.claude/hooks/repotory-handoff.sh';

const errors = [];

function fail(msg) {
  errors.push(msg);
}

/** Replace the machine-specific binary path with a placeholder. */
function replaceBin(value, bin) {
  if (typeof value === 'string') return value.split(bin).join('<BIN>');
  if (Array.isArray(value)) return value.map((v) => replaceBin(v, bin));
  if (value && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) out[k] = replaceBin(v, bin);
    return out;
  }
  return value;
}

/** Run one copy of the script and compare what it produced with the fixture. */
function checkScript(script) {
  const dir = mkdtempSync(join(tmpdir(), 'repotory-init-contract-'));
  try {
    // A fake binary: the script only records the path, it never runs it.
    const bin = join(dir, 'repotory-mcp');
    writeFileSync(bin, '#!/bin/sh\n');
    chmodSync(bin, 0o755);

    execFileSync(
      process.execPath,
      [
        script.path,
        '--client',
        'claude,cursor,vscode',
        '--bin',
        bin,
        '--with-hook',
        '--with-instructions',
        '--yes',
      ],
      { cwd: dir, stdio: 'pipe' },
    );

    const actual = {};
    for (const rel of PRODUCED) {
      const file = join(dir, rel);
      if (!existsSync(file)) {
        fail(`${script.label}: the script did not produce ${rel}`);
        continue;
      }
      // The instruction files are Markdown, not JSON — compared as exact text,
      // the same way the hook script is.
      if (rel === 'AGENTS.md' || rel === 'CLAUDE.md') {
        actual[rel] = readFileSync(file, 'utf8');
        continue;
      }
      try {
        actual[rel] = replaceBin(JSON.parse(readFileSync(file, 'utf8')), bin);
      } catch (err) {
        fail(`${script.label}: ${rel} is not valid JSON: ${err.message}`);
      }
    }
    const hookFile = join(dir, HOOK_SCRIPT);
    if (!existsSync(hookFile)) {
      fail(`${script.label}: the script did not produce ${HOOK_SCRIPT}`);
    } else {
      actual[HOOK_SCRIPT] = readFileSync(hookFile, 'utf8');
    }

    if (!existsSync(FIXTURE)) {
      fail(`missing fixture: ${FIXTURE} (regenerate with: go test ./internal/agentinit -run TestContractFixture -update)`);
      return;
    }
    const expected = JSON.parse(readFileSync(FIXTURE, 'utf8'));
    for (const key of Object.keys(expected)) {
      if (!(key in actual)) continue; // already reported as missing
      if (JSON.stringify(sorted(expected[key])) !== JSON.stringify(sorted(actual[key]))) {
        fail(
          `${script.label}: ${key} differs from the contract fixture\n` +
            `  script:  ${JSON.stringify(actual[key])}\n` +
            `  fixture: ${JSON.stringify(expected[key])}`,
        );
      }
    }
    for (const key of Object.keys(actual)) {
      if (!(key in expected)) {
        fail(`${script.label}: ${key} is produced by the script but not in the contract fixture`);
      }
    }
  } catch (err) {
    fail(`running ${script.label} failed: ${err.stderr ? err.stderr.toString() : err.message}`);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

for (const script of SCRIPTS) {
  if (!existsSync(script.path)) {
    fail(`missing script: ${script.path}`);
    continue;
  }
  checkScript(script);
}

/** Stable stringify: key order must not decide whether two files agree. */
function sorted(value) {
  if (Array.isArray(value)) return value.map(sorted);
  if (value && typeof value === 'object') {
    const out = {};
    for (const key of Object.keys(value).sort()) out[key] = sorted(value[key]);
    return out;
  }
  return value;
}

if (errors.length > 0) {
  console.error('agent-init contract FAILED');
  for (const e of errors) console.error(`  - ${e}`);
  console.error(
    '\nThe CLI script and the Go implementation must write the same files.\n' +
      'If the change is intended: update BOTH scripts/repotory-init/index.mjs and\n' +
      'internal/agentinit, then regenerate the fixture with\n' +
      '  go test ./internal/agentinit -run TestContractFixture -update\n' +
      'and re-sync the copy the VSIX ships:\n' +
      '  node ide/vscode/scripts/sync-init.mjs',
  );
  process.exit(1);
}

console.log('agent-init contract OK: the CLI script and the Go implementation write the same files.');
