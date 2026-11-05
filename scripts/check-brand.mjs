#!/usr/bin/env node
// scripts/check-brand.mjs — 品牌守门（CI gate，纯 node、零 npm 依赖）。
//
// 背景 / rationale：ADR-0049 把品牌按 Manus 式重排——母品牌 Cinder、桌面
// Cinder Desktop、独立仓 Warden——并同时划了一条线：**显示层全换、标识层冻结**。
//
// 这条线如果只存在于某个人的脑子里，两边都会漂，而且是反方向的漂：
// 显示层漏掉一处，用户就看到一个旧名字（DMG 窗口上写着「Repotory」就是这么来的）；
// 标识层被「顺手清理」一处，每个用户的 `claude mcp add` 与插件白名单就断一次。
// 所以这里查四件事：
//
//   ① 品牌词有唯一真相源（build/brand.json），各处产品名必须与它相等
//   ② 标识层冻结清单逐条仍在原处——冻结不是「不检查」，是「检查它还在」
//   ③ 当前面（用户看得见的面）不再出现旧品牌词；历史面（ADR / CHANGELOG /
//      评审记录 / 设计产物 / TODO 的历史条目）故意保留旧名，按前缀放行
//   ④ App 里的 BrandMark 组件与母图 build/icon.svg 的字形必须是同一条路径
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
  ['ide/vscode/package.json', 'mother', 'VS Code 插件的 displayName'],
  ['ide/jetbrains/src/main/resources/META-INF/plugin.xml', 'mother', 'JetBrains 插件的 <name>'],
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

// The desktop name is the mother name plus a qualifier. If that stops
// holding, "Cinder Desktop" is two unrelated words and the family reads
// as two brands.
if (brand.mother && brand.desktop && !brand.desktop.startsWith(`${brand.mother} `)) {
  issues.push(
    `build/brand.json: 桌面名「${brand.desktop}」不是母品牌「${brand.mother}」加后缀——` +
      '一个品牌族里的两个面要读得出同源'
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
  ['packaging/homebrew/Casks/repotory.rb', 'cask "repotory" do', 'Homebrew cask token'],
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

// --------------------------------------------- ④ BrandMark 与母图同源 ----

// The app's own mark is a second copy of the master's letterform. It was
// hand-copied, and a hand copy is exactly what this pipeline exists to
// prevent — a rebrand would move the master and leave the navbar behind.
if (!existsSync(join(root, 'build/icon.svg')) || !existsSync(join(root, 'web/src/components/BrandMark.tsx'))) {
  issues.push('build/icon.svg 或 web/src/components/BrandMark.tsx 不存在——母图与 App 内标记必须同时存在')
} else {
  const master = read('build/icon.svg')
  const app = read('web/src/components/BrandMark.tsx')

  const masterPath = master.match(/<path[^>]*\sd="([^"]+)"/)?.[1]
  const masterTransform = master.match(/<g transform="([^"]+)"/)?.[1]
  const masterField = master.match(/<rect width="512" height="512" fill="([^"]+)"/)?.[1]
  const masterFrame = master.match(/<rect x="(\d+)" y="(\d+)" width="(\d+)" height="(\d+)"[^>]*stroke="([^"]+)" stroke-width="(\d+)"/)

  const appPath = app.match(/<path[^>]*\sd="([^"]+)"/)?.[1]
  const appTransform = app.match(/<g transform="([^"]+)"/)?.[1]

  if (!masterPath || !appPath) {
    issues.push('build/icon.svg 或 BrandMark.tsx 里读不到字形路径——母图与 App 内标记的结构变了，更新本检查')
  } else if (masterPath !== appPath) {
    issues.push(
      'web/src/components/BrandMark.tsx: 字形路径与 build/icon.svg 不一致——' +
        'App 里的标记是母图的手抄副本，改母图时它会漏改'
    )
  }

  if (masterTransform && appTransform && masterTransform !== appTransform) {
    issues.push(
      `web/src/components/BrandMark.tsx: 字母组变换 "${appTransform}" 与母图的 "${masterTransform}" 不一致`
    )
  }

  // The frame's geometry and the two colours are the master's too.
  if (masterFrame) {
    const [, x, y, w, h, ink, stroke] = masterFrame
    const appStroke = app.match(/const MASTER_STROKE = (\d+)/)?.[1]
    const appInset = app.match(/const MASTER_INSET = (\d+)/)?.[1]
    if (appStroke !== stroke) {
      report('web/src/components/BrandMark.tsx · MASTER_STROKE', `母图的 ${stroke}`, appStroke)
    }
    if (appInset !== x) {
      report('web/src/components/BrandMark.tsx · MASTER_INSET', `母图的 ${x}`, appInset)
    }
    if (!app.includes(`stroke="${ink}"`)) {
      issues.push(`web/src/components/BrandMark.tsx: 描边色不是母图的 ${ink}`)
    }
  }
  if (masterField && !app.includes(`fill="${masterField}"`)) {
    issues.push(`web/src/components/BrandMark.tsx: 纸面底色不是母图的 ${masterField}`)
  }
}

// ------------------------------------------------------------------ 结论 ----

if (issues.length > 0) {
  console.error(`品牌漂移检测到 ${issues.length} 处：\n`)
  for (const line of issues) console.error(`  - ${line}`)
  console.error(
    '\n修法：显示层改成 build/brand.json 里的词；标识层不许动——' +
      '它是接口不是品牌（ADR-0049 决策③），要动先走契约 major bump。'
  )
  process.exit(1)
}

console.log(
  `brand OK: 母品牌 ${brand.mother} / 桌面 ${brand.desktop} / 助手 ${brand.agent}；` +
    `${BRAND_SITES.length} 处产品名一致，${FROZEN.length} 条冻结标识层仍在原处，` +
    `${scanned.length} 个当前面文件无旧品牌词，BrandMark 与母图同源。`
)
