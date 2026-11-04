#!/usr/bin/env node
// Builds the human-written half of the GitHub Release notes from CHANGELOG.md.
//
// Why this exists. The release body used to be whatever GitHub's
// `generate_release_notes` produced: a list of commit subjects and a compare
// link. Every one of those lines says what a commit was CALLED, not what
// changed for the user — and this project's commits are small, incremental and
// heavily internal ("fix(test): 比对要归一化", "chore(packaging): fill sha256").
// A reader landing on the release page learned nothing.
//
// The CHANGELOG already carries the opposite kind of text: every entry opens
// with a bold headline written for a reader ("IDE 面板重画：按钮不再被裁切…")
// and then continues into the reasoning, the decisions and the cost. That
// headline IS the release note. The rest of the entry is the design record,
// and the design record belongs in the repository — on the release page it
// would be 4000 characters nobody reads.
//
// So this script reduces one version's CHANGELOG section to its headlines,
// grouped by the subsection the CHANGELOG itself uses, and the release
// workflow hands that to the release action as `body_path`. GitHub's own
// generated notes (the commit list and the compare link) are still appended
// after it — the action prepends `body` — so nothing is lost. The page simply
// stops opening with a diffstat.
//
// Two rules keep the reduction honest:
//
//   1. Every bullet must OPEN with a bold headline. A bullet without one has
//      no headline to publish, so this script refuses it rather than
//      publishing a truncated first sentence and calling that a headline.
//      The refusal is a CI gate (`--check`), not a surprise at tag time: the
//      gate runs on every push, so by the time a tag exists the changelog has
//      already been proven publishable.
//   2. Nothing is invented. The subsection names, the headline text and the
//      link back to the changelog all come from the changelog itself.
//
// Usage:
//   node scripts/release-notes.mjs                   # notes for the SSOT version, on stdout
//   node scripts/release-notes.mjs --version 2.29.0  # notes for one version
//   node scripts/release-notes.mjs --out notes.md    # write to a file instead of stdout
//   node scripts/release-notes.mjs --check           # validate only (CI gate)
//
// The paths are resolved relative to THIS FILE, not to the current directory:
// the release workflow checks the repository out into a subdirectory (the
// workspace root of that job holds the release artifacts, and every file in
// it ends up in SHA256SUMS), then runs this script from there.

import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(HERE, '..');

// ------------------------------------------------------------------ args ----

function parseArgs(argv) {
  const opts = { version: null, changelog: null, out: null, check: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--version') opts.version = argv[++i];
    else if (a === '--changelog') opts.changelog = argv[++i];
    else if (a === '--out') opts.out = argv[++i];
    else if (a === '--check') opts.check = true;
    else if (a === '--help' || a === '-h') {
      console.log(
        'usage: release-notes.mjs [--version X.Y.Z] [--changelog FILE] [--out FILE] [--check]',
      );
      process.exit(0);
    } else {
      fail(`不认识的参数：${a}`);
    }
  }
  return opts;
}

const errors = [];

function fail(msg) {
  errors.push(msg);
}

// --------------------------------------------------------------- reading ----

function readChangelog(path) {
  if (!existsSync(path)) {
    fail(`找不到 CHANGELOG：${path}`);
    return null;
  }
  return readFileSync(path, 'utf8');
}

/** The version every manifest agrees on (wails.json is the SSOT). */
function ssotVersion() {
  const path = resolve(ROOT, 'wails.json');
  if (!existsSync(path)) return null;
  try {
    const parsed = JSON.parse(readFileSync(path, 'utf8'));
    return parsed?.info?.productVersion ?? null;
  } catch {
    return null;
  }
}

function escapeRe(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// ------------------------------------------------------------- extraction ---

/**
 * Pull the headline out of one changelog bullet.
 *
 * The house format is `- **headline**：the rest of the entry`, so the headline
 * is the first bold span. A bullet that does not open with one is an error,
 * not something to guess at — see rule 1 at the top of this file.
 */
function headlineOf(bulletBody, lineNo, errors) {
  const m = bulletBody.match(/^\*\*(.+?)\*\*/);
  if (!m) {
    errors.push(
      `第 ${lineNo} 行的条目没有以加粗标题开头（形如 \`- **标题**：正文\`）——` +
        `发布页要的就是这个标题，没有它就没有可发布的一行`,
    );
    return null;
  }
  return m[1].trim();
}

/**
 * Reduce one version's section of the changelog to its headlines.
 *
 * The section runs from `## [version]` to the next `## [`. Inside it, `###`
 * opens a subsection and a `- ` at column 0 opens an entry. Indented bullets
 * and the continuation lines of an entry are part of that entry's prose and
 * are not entries of their own.
 */
function extractHighlights(lines, version) {
  let start = -1;
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/^## \[(.+?)\]/);
    if (m && m[1] === version) {
      start = i;
      break;
    }
  }
  if (start === -1) {
    fail(`CHANGELOG 里没有 [${version}] 这一节——发版前要先切出这一节`);
    return null;
  }

  let end = lines.length;
  for (let i = start + 1; i < lines.length; i++) {
    if (/^## /.test(lines[i])) {
      end = i;
      break;
    }
  }

  const highlights = [];
  let subsection = '变更';
  for (let i = start + 1; i < end; i++) {
    const line = lines[i];
    const h3 = line.match(/^###\s+(.+?)\s*$/);
    if (h3) {
      subsection = h3[1];
      continue;
    }
    if (!line.startsWith('- ')) continue;
    const title = headlineOf(line.slice(2), i + 1, errors);
    if (title !== null) highlights.push({ subsection, title });
  }

  if (highlights.length === 0 && errors.length === 0) {
    fail(`CHANGELOG 的 [${version}] 这一节里一条条目都没有——空节的发布页只有一句话`);
  }
  return highlights;
}

// -------------------------------------------------------------- rendering ---

/**
 * The repository URL, read out of the changelog's own link definitions rather
 * than hardcoded here: `[2.29.0]: https://github.com/<owner>/<repo>/compare/…`.
 * The CI environment provides GITHUB_REPOSITORY as a second source; if neither
 * is available the link line is simply left out.
 */
function repoUrl(changelog, version) {
  const m = changelog?.match(new RegExp(`^\\[${escapeRe(version)}\\]:\\s*(\\S+)\\s*$`, 'm'));
  if (m) {
    const cut = m[1].indexOf('/compare/');
    if (cut !== -1) return m[1].slice(0, cut);
  }
  const env = process.env.GITHUB_REPOSITORY;
  if (env && /^[\w.-]+\/[\w.-]+$/.test(env)) return `https://github.com/${env}`;
  return null;
}

function renderBody(highlights, version, repo) {
  const out = ['## 本次更新', ''];
  let current = null;
  for (const h of highlights) {
    if (h.subsection !== current) {
      if (current !== null) out.push('');
      out.push(`### ${h.subsection}`, '');
      current = h.subsection;
    }
    out.push(`- **${h.title}**`);
  }
  if (repo) {
    out.push('');
    out.push(
      `每条的完整推导、决策与代价见 [CHANGELOG.md](${repo}/blob/v${version}/CHANGELOG.md)。`,
    );
  }
  out.push('');
  return out.join('\n');
}

// ------------------------------------------------------------------ main ----

const opts = parseArgs(process.argv.slice(2));

const changelogPath = opts.changelog ? resolve(opts.changelog) : resolve(ROOT, 'CHANGELOG.md');
const changelog = readChangelog(changelogPath);

if (errors.length === 0) {
  const version = opts.version ?? ssotVersion();
  if (!version) {
    fail('定不出版本号：既没有 --version，wails.json 里也读不到 info.productVersion');
  } else {
    const highlights = extractHighlights(changelog.split('\n'), version);
    if (highlights && highlights.length && errors.length === 0) {
      if (opts.check) {
        console.log(
          `release notes OK: [${version}] 的 ${highlights.length} 条标题都能发布（${changelogPath}）`,
        );
      } else {
        const body = renderBody(highlights, version, repoUrl(changelog, version));
        if (opts.out) {
          writeFileSync(resolve(opts.out), body, 'utf8');
          console.log(`release notes written: ${resolve(opts.out)}（${highlights.length} 条）`);
        } else {
          process.stdout.write(body);
        }
      }
    }
  }
}

if (errors.length > 0) {
  console.error('release notes 失败：');
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}
