#!/usr/bin/env node
// scripts/check-brand.mjs — 品牌守门（CI gate，纯 node、零 npm 依赖）。
//
// 背景 / rationale：ADR-0049 把品牌按 Manus 式重排——母品牌 Cinder、桌面
// Cinder Desktop、独立仓 Warden——并同时划了一条线：**显示层全换、标识层冻结**。
//
// 这条线如果只存在于某个人的脑子里，两边都会漂，而且是反方向的漂：
// 显示层漏掉一处，用户就看到一个旧名字（DMG 窗口上写着「Repotory」就是这么来的）；
// 标识层被「顺手清理」一处，每个用户的 `claude mcp add` 与插件白名单就断一次。
// 所以这里查五件事：
//
//   ① 品牌词有唯一真相源（build/brand.json），各处产品名必须与它相等
//   ② 标识层冻结清单逐条仍在原处——冻结不是「不检查」，是「检查它还在」
//   ③ 当前面（用户看得见的面）不再出现旧品牌词；历史面（ADR / CHANGELOG /
//      评审记录 / 设计产物 / TODO 的历史条目）故意保留旧名，按前缀放行
//   ④ 仓地址只许指向真存在的仓——旧仓地址与旧 Pages 路径都不是「旧一点」，
//      它们是打不开的链接；白名单由品牌词与同步脚本推导，不手抄
//   ⑤ App 里的 BrandMark 组件与母图 build/icon.svg 的字形必须是同一条路径
//
// 依赖：git（只扫已提交的文件——未提交的本地中间物不该决定门红不红）。
//
// 运行：node scripts/check-brand.mjs
// 结果：全过 → exit 0；发现漂移 → 打印清单并 exit 1。

import { readFileSync, existsSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const issues = []

function read(rel) {
  return readFileSync(join(root, rel), 'utf8')
}

function report(where, expected, got) {
  issues.push(`${where}: 期望 ${expected}，实际 ${got === undefined ? '读不到' : got}`)
}

/** A word standing on its own — "Cinder" must not match "CinderDesktop". */
function hasWord(text, word) {
  return new RegExp(`(?<![A-Za-z0-9_])${word}(?![A-Za-z0-9_])`).test(text)
}

// ------------------------------------------------- ① 品牌词的唯一真相源 ----

const brandPath = 'build/brand.json'
if (!existsSync(join(root, brandPath))) {
  console.error(`ERROR: ${brandPath} 不存在——它是品牌词的 SSOT`)
  process.exit(1)
}
const brand = JSON.parse(read(brandPath))
for (const key of ['mother', 'desktop', 'agent']) {
  if (!brand[key] || typeof brand[key] !== 'string') {
    issues.push(`${brandPath}: 读不到 "${key}"`)
  }
}

// Each site that shows a product name to a user must carry the word from
// brand.json. A site that names the wrong product is worse than a site
// that names nothing, because the user believes it.
const BRAND_SITES = [
  ['wails.json', 'desktop', 'Wails 构建读的产品名'],
  ['build/dmg-background.svg', 'desktop', 'DMG 窗口上的产品名'],
  ['build/icon.svg', 'mother', '母图的无障碍名'],
  ['web/index.html', 'desktop', '首屏 <title>'],
  ['README.md', 'desktop', '产物表里的桌面端'],
  ['README.zh-CN.md', 'desktop', '产物表里的桌面端'],
  ['TODO.md', 'mother', '现状速查里的母品牌'],
  ['TODO.md', 'agent', '现状速查里的助手名'],
]
for (const [file, key, where] of BRAND_SITES) {
  const word = brand[key]
  if (!word) continue
  if (!existsSync(join(root, file))) {
    issues.push(`${file}: 文件不存在（${where}）`)
    continue
  }
  if (!hasWord(read(file), word)) {
    issues.push(`${file}: ${where}里没有品牌词「${word}」`)
  }
}

// ------------------------------------------------- ①b 市场名字：精确字段 ----
//
// The IDE plugins' marketplace name is not prose — it is a field a
// marketplace can refuse. VS Code requires `displayName` to be globally
// unique across every extension on the Marketplace, and "Cinder" was
// already taken there by another extension: the publish job failed with
// "This extension display name is taken." and the release went out with
// the plugin missing.
//
// A "contains the mother word" check cannot catch that class of failure —
// the broken value contained the word too. So this field is compared
// exactly, and it lives in brand.json like every other name.
const EXACT_NAMES = [
  ['ide/vscode/package.json', /"displayName":\s*"([^"]*)"/, 'idePlugin', 'VS Code 插件的 displayName'],
  [
    'ide/jetbrains/src/main/resources/META-INF/plugin.xml',
    /<name>([^<]*)<\/name>/,
    'idePlugin',
    'JetBrains 插件的 <name>',
  ],
]
for (const [file, re, key, where] of EXACT_NAMES) {
  const expected = brand[key]
  if (!expected) {
    issues.push(`${brandPath}: 读不到 "${key}"——${where}没有名字可比对`)
    continue
  }
  if (!existsSync(join(root, file))) {
    issues.push(`${file}: 文件不存在（${where}）`)
    continue
  }
  const got = read(file).match(re)?.[1]
  if (got !== expected) {
    issues.push(
      `${file}: ${where}是「${got ?? '读不到'}」，应为「${expected}」——` +
        '市场名字是精确字段，「含有母品牌词」不算过'
    )
  }
}

// The window title and the HTML <title> are one string shown in two places,
// and they drifted apart once (main.go said "Cinder: …" while the page said
// "Cinder Desktop: …"). Compare both against one value derived from
// brand.json — not against each other.
const DISPLAY_NAME = `${brand.desktop}: Local Git Knowledge Base`
const TITLE_SITES = [
  ['main.go', /Title:\s*"([^"]*)"/, 'Wails 窗口标题'],
  ['web/index.html', /<title>([^<]*)<\/title>/, '首屏 <title>'],
]
for (const [file, re, where] of TITLE_SITES) {
  if (!existsSync(join(root, file))) {
    issues.push(`${file}: 文件不存在（${where}）`)
    continue
  }
  const got = read(file).match(re)?.[1]
  if (got !== DISPLAY_NAME) {
    issues.push(`${file}: ${where}是「${got ?? '读不到'}」，应为「${DISPLAY_NAME}」`)
  }
}

// Wails reads productName for the bundle; a wrong value renames the app in
// Applications without touching a single line of copy.
if (existsSync(join(root, 'wails.json'))) {
  const product = read('wails.json').match(/"productName":\s*"([^"]*)"/)?.[1]
  if (product !== brand.desktop) {
    issues.push(`wails.json: productName 是「${product ?? '读不到'}」，应为「${brand.desktop}」`)
  }
}

// The sibling repository keeps its own copy of brand.json because its CI runs
// without this checkout — the structural blind spot ADR-0049 records. When
// both are present on disk, they must agree; otherwise one repo can rename a
// product and both gates stay green.
const siblingBrand = join(root, '..', 'warden', 'brand.json')
if (existsSync(siblingBrand)) {
  const other = JSON.parse(readFileSync(siblingBrand, 'utf8'))
  for (const key of ['mother', 'desktop', 'agent', 'idePlugin']) {
    if (brand[key] !== other[key]) {
      issues.push(
        `../warden/brand.json: "${key}" 是「${other[key] ?? '读不到'}」，本仓是「${brand[key]}」——` +
          '两仓 brand.json 必须同批改'
      )
    }
  }
}

// The desktop name is the mother name plus a qualifier. If that stops
// holding, "Cinder Desktop" is two unrelated words and the family reads
// as two brands.
if (brand.mother && brand.desktop && !brand.desktop.startsWith(`${brand.mother} `)) {
  issues.push(
    `build/brand.json: 桌面名「${brand.desktop}」不是母品牌「${brand.mother}」加后缀——` +
      '一个品牌族里的两个面要读得出同源'
  )
}

// The IDE plugins' name has the same requirement, for the same reason: the
// qualifier is what keeps it inside the family.
if (brand.mother && brand.idePlugin && !brand.idePlugin.startsWith(`${brand.mother} `)) {
  issues.push(
    `build/brand.json: 插件名「${brand.idePlugin}」不是母品牌「${brand.mother}」加后缀——` +
      'Marketplace 上它是独立条目，名字得让人看得出它属于这个品牌族'
  )
}

// ------------------------------------------------- ② 标识层冻结清单 ----

// The interface layer, frozen by ADR-0049 decision 3. Each entry must
// still be there, verbatim, in the file that owns it. "Frozen" is not
// "don't look" — it is "look, and fail if it moved".
const FROZEN = [
  ['cmd/mcp/main.go', '"repotory-mcp"', 'MCP server 名（用户的 MCP 客户端配置里写的就是它）'],
  ['cmd/mcp/main.go', '"repotory_', 'MCP 工具前缀（18 个工具名全靠它）'],
  ['internal/platform/platform.go', 'const dirName = "repotory"', '用户数据目录名'],
  ['internal/platform/platform.go', '"repotory.db"', '数据库文件名'],
  ['internal/domain/types.go', 'repotory://memory/default', 'DB 里的默认项目 sentinel'],
  ['go.mod', 'module repotory', 'Go module 路径'],
  ['ide/vscode/package.json', '"name": "repotory-vscode"', 'VS Code 插件 id（publisher + name）'],
  [
    'ide/jetbrains/src/main/resources/META-INF/plugin.xml',
    '<id>dev.repotory.jetbrains</id>',
    'JetBrains 插件 id',
  ],
  ['scripts/repotory-init/package.json', '"name": "repotory-init"', 'npm 包名'],
  ['packaging/cinder/homebrew/Casks/repotory.rb', 'cask "repotory" do', 'Homebrew cask token'],
  ['scripts/install.ps1', '$env:LOCALAPPDATA\\Repotory', 'Windows 安装目录名'],
  ['skill/repotory/SKILL.md', 'name: repotory', 'skill 包名（skill/repotory/）'],
]
for (const [file, literal, where] of FROZEN) {
  if (!existsSync(join(root, file))) {
    issues.push(`${file}: 文件不存在（${where}）`)
    continue
  }
  if (!read(file).includes(literal)) {
    issues.push(`${file}: 冻结的标识层不见了 —— ${where}（期望字面量 ${literal}）`)
  }
}

// ------------------------------------------------- ③ 当前面不留旧品牌词 ----

// The old brand word, standing on its own. Class names (RepotoryClient,
// RepotoryToolWindow) are code identifiers and are out of scope: renaming
// them is churn, not branding.
const OLD_BRAND = 'Repotory'
const oldWordRe = new RegExp(`(?<![A-Za-z0-9_])${OLD_BRAND}(?![A-Za-z0-9_])`)

// Historical surfaces keep the old name on purpose: a record of what was
// decided when it was decided. Rewriting them would be falsifying the
// record, and the old word is the only way to find those decisions.
const HISTORICAL = [
  'CHANGELOG.md', // 发布历史：写下那一刻就是过去
  'TODO.md', // 决策日志，按定义引述过去
  'docs/adr/', // ADR 历史
  'docs/en/adr/',
  'docs/product-review/', // 带日期的评审记录
  'blog/', // 历史文章
  'outputs/', // 设计过程产物
]

// Comment lines are not a surface: they explain why a frozen string still
// says "repotory", and stripping them keeps that explanation from being
// mistaken for a display-layer miss.
const COMMENT_EXT = new Set(['.go', '.ts', '.tsx', '.js', '.mjs', '.css', '.yml', '.yaml', '.sh', '.ps1'])

function stripComments(text, ext) {
  if (!COMMENT_EXT.has(ext)) return text
  return text
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .split('\n')
    .map((line) => {
      // A '#' inside a string is not a comment, but none of the files
      // scanned here has one on a line that also mentions the old brand.
      const hash = line.indexOf('#')
      const slash = line.indexOf('//')
      let cut = -1
      if (slash >= 0) cut = slash
      if (hash >= 0 && (cut < 0 || hash < cut)) cut = hash
      return cut >= 0 ? line.slice(0, cut) : line
    })
    .join('\n')
}

// Frozen literals explain any occurrence of the old word that contains
// one of them — that occurrence IS the interface, not a leftover name.
const frozenLiterals = FROZEN.map(([, literal]) => literal)

// The scanner's own source contains the word it hunts for. Excluding it by
// path is honest: a word in the scanner is not a brand leak.
const SELF = 'scripts/check-brand.mjs'

let tracked = []
try {
  tracked = execFileSync('git', ['ls-files', '-z'], { cwd: root, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
    .split('\0')
    .filter(Boolean)
} catch (err) {
  console.error('ERROR: git ls-files 失败——本检查只扫已提交的文件，需要在 git 工作树里跑')
  process.exit(1)
}

const scanned = tracked.filter((f) => f !== SELF && !HISTORICAL.some((h) => f === h || f.startsWith(h)))
for (const file of scanned) {
  if (!existsSync(join(root, file))) continue
  const ext = file.slice(file.lastIndexOf('.'))
  const text = stripComments(read(file), ext)
  const lines = text.split('\n')
  lines.forEach((line, i) => {
    if (!oldWordRe.test(line)) return
    if (frozenLiterals.some((lit) => line.includes(lit))) return
    issues.push(`${file}:${i + 1}: 当前面还留着旧品牌词「${OLD_BRAND}」—— ${line.trim().slice(0, 90)}`)
  })
}

// ------------------------------------------ ④ 仓地址只许指向真存在的仓 ----
//
// The repository slugs changed on 2026-11-05 (ADR-0049 addendum two), and
// they had changed once before (v2.0.0). A stale address is not "slightly
// old": GitHub Pages does not follow a rename — the old path answers 404
// while the new one answers 200 — and GitHub promises no permanence for
// the 301 it hands out for the repository itself. So the old addresses
// are hunted where a reader can still follow them.
//
// The org prefix is what makes a mention an address. A bare "repotory" is
// the frozen identifier layer, which section ③ already owns, and a bare
// mention of the old name in prose is a remark about history — neither is
// a link. That is also why this check reads the raw line: the comment
// stripper cuts at "//", which is inside every https:// URL, so a
// stripped line would have had the address cut off before the scan.
//
// The stems are the names this project has ever published under: repo-nest
// (up to v1.16) and repotory (v2.0 to v2.31), the latter also the stem of
// the separate repository's repotory-agent. Everything after the stem
// belongs to the same stale address, so the stem is what gets hunted.
//
// But a rename is not the only way to end up pointing at nothing. A typo
// does it too, and it does it in the one place everybody copies an install
// command out of: every documented `brew install --cask
// sky-jiangcheng/<tap>/…` used to name a repository that had never
// existed (`sky-jiangcheng/repo` — 404, and no redirect to save it) while
// the real tap was `sky-jiangcheng/homebrew-repo`. Nothing caught it: the
// tap's own CI installs a manifest by its path INSIDE the tap and never
// types the tap's own address, so the only thing that ever had to be
// right was the thing nobody checked.
//
// So the addresses allowed in a current surface are an ALLOWLIST, derived
// rather than written down twice:
//
//   · the mother and the agent words from build/brand.json, lowercased
//     — the two projects' own repositories;
//   · the tap and the bucket slugs from scripts/sync-packaging.sh — the
//     script that actually pushes to them is the authority on what they
//     are called.
//
// Anything else under the org is a dead link waiting to be typed.
const ORG = 'sky-jiangcheng'
const OLD_SLUG_STEMS = ['repo-nest', 'repotory']

const allowedSlugs = new Set(
  [brand.mother, brand.agent].filter(Boolean).map((word) => word.toLowerCase())
)

// The tap and the bucket are named in the script that clones and pushes to
// them. Reading the names from there keeps a second copy of them from
// existing here — a copy is a copy that can drift.
const syncFile = 'scripts/sync-packaging.sh'
if (!existsSync(join(root, syncFile))) {
  issues.push(`${syncFile}: 文件不存在——tap / bucket 的仓名没有第二处真相源可比对`)
} else {
  for (const m of read(syncFile).matchAll(/REPO_SLUG="[^"]*\/([A-Za-z0-9._-]+)"/g)) {
    allowedSlugs.add(m[1].replace(/\.git$/, ''))
  }
}

// An address under the org, wherever it shows up: a repository URL, an API
// path, a raw file path, a Pages path, or the `owner/tap` shorthand
// Homebrew takes. The optional ".github.io" is the Pages host, whose path
// is the repository name.
const addressRe = new RegExp(`${ORG}(?:\\.github\\.io)?/([A-Za-z0-9._-]+)`, 'g')

for (const file of scanned) {
  if (!existsSync(join(root, file))) continue
  read(file)
    .split('\n')
    .forEach((line, i) => {
      const slugs = new Set()
      for (const m of line.matchAll(addressRe)) {
        // A trailing ".git" and a trailing dot or hyphen belong to the
        // sentence, not to the name.
        slugs.add(m[1].replace(/\.git$/, '').replace(/[-.]+$/, ''))
      }
      for (const slug of slugs) {
        if (allowedSlugs.has(slug)) continue
        if (OLD_SLUG_STEMS.some((stem) => slug.startsWith(stem))) {
          issues.push(
            `${file}:${i + 1}: 当前面还指着改过名的仓地址 —— ${line.trim().slice(0, 90)}`
          )
        } else {
          issues.push(
            `${file}:${i + 1}: 当前面指着白名单外的仓地址「${ORG}/${slug}」—— ` +
              `白名单只有 ${[...allowedSlugs].sort().join(' / ')}；` +
              `照抄不出来的安装命令就是从这种行里长出来的 —— ${line.trim().slice(0, 60)}`
          )
        }
      }
    })
}

// --------------------------------------------- ⑤ BrandMark 与母图同源 ----

// The app's own mark is a second copy of the master's letterform. It used to
// be a hand-copied <path d> in BrandMark.tsx, byte-compared against only the
// FIRST master path. The v6 knot is three paths, so the geometry now lives
// in generated data and this gate compares EVERY path plus the group
// transform — a rebrand cannot leave the navbar behind.
const MASTER_REL = 'build/icon.svg'
const UI_DATA_REL = 'web/src/components/brandMarkPaths.ts'
const UI_COMPONENT_REL = 'web/src/components/BrandMark.tsx'
if (
  !existsSync(join(root, MASTER_REL)) ||
  !existsSync(join(root, UI_DATA_REL)) ||
  !existsSync(join(root, UI_COMPONENT_REL))
) {
  issues.push(`${MASTER_REL} / ${UI_DATA_REL} / ${UI_COMPONENT_REL} 必须同时存在——母图与 App 内标记数据同源`)
} else {
  const master = read(MASTER_REL)
  const uiData = read(UI_DATA_REL)
  const component = read(UI_COMPONENT_REL)

  const masterPaths = [...master.matchAll(/<path[^>]*\sd="([^"]+)"/g)].map((m) => m[1])
  const uiPaths = [...uiData.matchAll(/^\s*'([^']+)',?$/gm)].map((m) => m[1])
  const masterTransform = master.match(/<g transform="([^"]+)"/)?.[1]
  const uiTransform = uiData.match(/BRAND_MARK_TRANSFORM = '([^']+)'/)?.[1]
  const masterField = master.match(/<rect width="512" height="512" fill="([^"]+)"/)?.[1]

  if (!masterPaths.length || !uiPaths.length) {
    issues.push(`${MASTER_REL} 或 ${UI_DATA_REL} 里读不到字形路径——母图结构变了，更新本检查`)
  } else if (masterPaths.length !== uiPaths.length) {
    issues.push(`${UI_DATA_REL}: 字形路径数 ${uiPaths.length} 与母图 ${masterPaths.length} 不一致`)
  } else {
    for (let i = 0; i < masterPaths.length; i++) {
      if (masterPaths[i] !== uiPaths[i]) {
        issues.push(`${UI_DATA_REL}: 第 ${i + 1} 段字形路径与 ${MASTER_REL} 不一致——App 里的标记是母图的手抄副本`)
      }
    }
  }

  if (masterTransform && uiTransform && masterTransform !== uiTransform) {
    issues.push(`${UI_DATA_REL}: 字形组变换 "${uiTransform}" 与母图的 "${masterTransform}" 不一致`)
  }
  if (masterField && !component.includes(`fill="${masterField}"`)) {
    issues.push(`${UI_COMPONENT_REL}: 墨场底色不是母图的 ${masterField}`)
  }
  if (!component.includes('BRAND_MARK_PATHS')) {
    issues.push(`${UI_COMPONENT_REL}: 没有使用生成的字形数据——标记会与母图脱钩`)
  }
}

// ------------------------------------------------------------------ 结论 ----

if (issues.length > 0) {
  console.error(`品牌漂移检测到 ${issues.length} 处：\n`)
  for (const line of issues) console.error(`  - ${line}`)
  console.error(
    '\n修法：显示层改成 build/brand.json 里的词；标识层不许动——' +
      '它是接口不是品牌（ADR-0049 决策③），要动先走契约 major bump。' +
      '旧仓地址换成 GitHub 上现在那个路径（仓地址不是品牌词，它跟实际路径走，' +
      '改名当天必须同批换掉）；白名单外的仓地址先问一句「这个仓真的存在吗」，' +
      '再决定是改地址还是把它加进真相源。'
  )
  process.exit(1)
}

console.log(
  `brand OK: 母品牌 ${brand.mother} / 桌面 ${brand.desktop} / 助手 ${brand.agent}；` +
    `${BRAND_SITES.length} 处产品名一致，${FROZEN.length} 条冻结标识层仍在原处，` +
    `${scanned.length} 个当前面文件无旧品牌词，仓地址都在白名单里（` +
    `${[...allowedSlugs].sort().join(' / ')}），BrandMark 与母图同源。`
)
