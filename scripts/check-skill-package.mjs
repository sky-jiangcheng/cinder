#!/usr/bin/env node
// Validates the WorkBuddy skill package under skill/.
//
// Three things can rot silently, and none of them break the Go or web
// builds, so nothing else in CI would notice:
//
//   1. the frontmatter (name / description) going malformed or over the
//      loader's length limit — the skill then stops being discovered, and
//      the only symptom is that an agent never mentions Cinder;
//   2. the query script referencing a schema column that no longer exists
//      (the "kind" vs "category" rename already happened once) — the script
//      then dies at query time, on a machine where nobody is watching;
//   3. the package layout drifting from "one top-level directory named after
//      the skill" — the loader rejects such a package.
//
// The script check runs against a TEMPORARY database, never the user's real
// one: it builds a minimal schema, points REPOTORY_DB at it, and asserts
// each subcommand answers.

import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const SKILL_DIR = 'skill/repotory';
const errors = [];

function fail(msg) {
  errors.push(msg);
}

// ---- 1. layout -------------------------------------------------------------

for (const required of [
  `${SKILL_DIR}/SKILL.md`,
  `${SKILL_DIR}/references/mcp-tools.md`,
  `${SKILL_DIR}/references/data-model.md`,
  `${SKILL_DIR}/scripts/repotory-query.py`,
]) {
  if (!existsSync(required)) fail(`missing file: ${required}`);
}

// The package archive is built from the PARENT of the skill directory, so
// the directory name must equal the frontmatter `name`.
const skillName = SKILL_DIR.split('/').pop();

// ---- 1b. the installed copy must match the repo copy -------------------------
//
// The skill lives in two places: skill/repotory/ in this repo (the source of
// truth, shipped in the release) and the user's ~/.workbuddy/skills/repotory/
// (the installed copy). They drift the moment one is edited and the other is
// not, and the symptom is subtle: the installed copy keeps serving stale tool
// signatures to every agent while the repo copy looks correct.
//
// The installed copy is optional — CI has no user-level skills directory, and
// a fresh checkout legitimately has none. So this check only fires when the
// installed copy actually exists.
const installedDir = join(
  process.env.HOME ?? '',
  '.workbuddy/skills/repotory',
);
if (existsSync(installedDir)) {
  for (const rel of [
    'SKILL.md',
    'references/mcp-tools.md',
    'references/data-model.md',
    'scripts/repotory-query.py',
  ]) {
    const repoCopy = join(SKILL_DIR, rel);
    const installedCopy = join(installedDir, rel);
    if (!existsSync(installedCopy)) {
      fail(`installed copy is missing ${rel} (${installedDir})`);
      continue;
    }
    const a = readFileSync(repoCopy, 'utf8');
    const b = readFileSync(installedCopy, 'utf8');
    if (a !== b) {
      fail(
        `installed copy differs from the repo copy: ${rel}\n` +
          `  repo:      ${repoCopy}\n` +
          `  installed: ${installedCopy}\n` +
          '  Copy the repo version over the installed one (or reinstall the skill).',
      );
    }
  }
}

// ---- 2. frontmatter ---------------------------------------------------------

const skillMd = readFileSync(`${SKILL_DIR}/SKILL.md`, 'utf8');
const fmMatch = skillMd.match(/^---\r?\n([\s\S]*?)\r?\n---/);
if (!fmMatch) {
  fail('SKILL.md has no YAML frontmatter');
} else {
  const fm = fmMatch[1];
  const nameMatch = fm.match(/^name:\s*(.+)$/m);
  if (!nameMatch) {
    fail('frontmatter has no `name`');
  } else {
    const declared = nameMatch[1].trim().replace(/^["']|["']$/g, '');
    if (declared !== skillName) {
      fail(`frontmatter name "${declared}" != directory name "${skillName}"`);
    }
  }

  // The description may be a YAML block scalar (`description: |`), so the
  // value runs until the next top-level key. A regex on the first line
  // would measure "|" instead of the text.
  const descMatch = fm.match(/^description:\s*(\|[-+]?|>[-+]?)?\s*\n([\s\S]*?)(?=\n[a-zA-Z_-]+:|\Z)/m);
  if (!descMatch) {
    fail('frontmatter has no `description`');
  } else {
    let desc;
    if (descMatch[1]) {
      // Block scalar: strip the indentation of the first content line.
      const body = descMatch[2];
      const indent = body.split('\n').find((l) => l.trim())?.match(/^\s*/)?.[0].length ?? 0;
      desc = body
        .split('\n')
        .map((l) => l.slice(indent))
        .join('\n')
        .trim();
    } else {
      desc = descMatch[2].trim();
    }
    if (desc.length > 1024) {
      fail(`description is ${desc.length} chars, over the 1024 limit`);
    }
    if (desc.length < 40) {
      fail(`description is only ${desc.length} chars — too short to route on`);
    }
  }
}

// ---- 3. the script against a throwaway database -----------------------------

const tmp = mkdtempSync(join(tmpdir(), 'repotory-skill-'));
const dbPath = join(tmp, 'test.db');
try {
  const sqlite = (sql) =>
    execFileSync('sqlite3', [dbPath, sql], { encoding: 'utf8' });

  // A minimal schema mirroring the columns the script actually selects.
  // The "kind" column is deliberate: the MCP layer calls it "category" while
  // the stored column is "kind", and the script must read the stored name.
  sqlite(`
    CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT, root_path TEXT);
    CREATE TABLE project_notes (
      id INTEGER PRIMARY KEY, project_id INTEGER, title TEXT, content TEXT,
      tags TEXT, kind TEXT, pinned INTEGER DEFAULT 0, source TEXT,
      sort_order INTEGER DEFAULT 0, created_at DATETIME, updated_at DATETIME,
      topic_id INTEGER
    );
    CREATE TABLE project_todos (id INTEGER PRIMARY KEY, title TEXT);
    CREATE TABLE note_topics (
      id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, name TEXT,
      description TEXT DEFAULT '', sort_order INTEGER DEFAULT 0,
      created_at DATETIME, updated_at DATETIME
    );
    CREATE VIRTUAL TABLE project_notes_fts USING fts5(title, content, tags);
    INSERT INTO projects VALUES (1, 'demo', '/tmp/demo');
    INSERT INTO project_notes
      VALUES (1, 1, '测试笔记', '正文内容 knowledge base', 'tag1', 'knowledge', 0, 'manual', 0, '2026-01-01', '2026-01-01', 9001);
    INSERT INTO project_notes_fts(rowid, title, content, tags)
      VALUES (1, '测试笔记', '正文内容 knowledge base', 'tag1');
    INSERT INTO note_topics VALUES (9001, 1, '支付网关', '', 0, '2026-01-01', '2026-01-01');
  `);

  const run = (args) =>
    execFileSync('python3', [`${SKILL_DIR}/scripts/repotory-query.py`, ...args], {
      encoding: 'utf8',
      env: { ...process.env, REPOTORY_DB: dbPath },
    });

  const checks = [
    { args: ['--stats'], expect: (out) => out.includes('FTS') },
    { args: ['--projects'], expect: (out) => out.includes('demo') },
    { args: ['--topics'], expect: (out) => out.includes('支付网关') },
    { args: ['--note', '1'], expect: (out) => out.includes('测试笔记') },
    { args: ['测试'], expect: (out) => out.includes('测试笔记') },
    { args: ['knowledge'], expect: (out) => out.includes('测试笔记') },
  ];

  for (const c of checks) {
    try {
      const out = run(c.args);
      if (!c.expect(out)) {
        fail(`script ${c.args.join(' ')} produced unexpected output:\n${out}`);
      }
    } catch (e) {
      fail(`script ${c.args.join(' ')} failed: ${e.message}`);
    }
  }
} catch (e) {
  fail(`could not set up the test database: ${e.message}`);
} finally {
  rmSync(tmp, { recursive: true, force: true });
}

// ---- verdict ----------------------------------------------------------------

if (errors.length > 0) {
  console.error('skill package check FAILED:');
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}
console.log('skill package OK');
