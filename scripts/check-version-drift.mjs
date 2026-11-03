#!/usr/bin/env node
// scripts/check-version-drift.mjs — 版本 / 计数漂移守卫（CI gate，纯 node、零依赖）。
//
// 背景 / rationale：版本号与「有多少份 ADR / 多少个工具」这类事实散落在十来个文件里，
// 全靠人手抄。抄写的代价是可验证的：TODO.md 在 v2.19.0 发布后仍写着 v2.18.0；
// SECURITY.md 承诺「每周扫描」，而 dependabot.yml 里是 monthly；AGENTS.md 数着
// 「33 份 ADR」，docs/adr/ 里加了第 34 篇就没人改回去。逐次手改每两轮漂一次，
// 证明「记得顺手改」不成立。
//
// 所以这里的做法是三条：能派生的就派生（本脚本从事实源算，而不是比对两份抄写），
// 抄写不掉的就不抄（改成指向事实源的链接，见 AGENTS.md 与 SECURITY.md 的改法），
// 剩下的由 CI 兜住——手改绕不过这道门，bump-version.sh 漏掉的也会被这里抓出来。
//
// 五道检查：
//   ① 版本一致 —— wails.json 的 info.productVersion 是 SSOT，十处产物清单必须与它相等
//   ② TODO 现状速查 —— TODO.md 顶部那行的版本必须与 SSOT 相等（由 bump-version.sh 同步）
//   ③ ADR 集合 —— docs/adr/ 的文件集合 = 中英两份索引列出的集合，且两份索引逐条对应
//   ④ 扫描频率 —— SECURITY.md 承诺的扫描频率必须与 .github/dependabot.yml 一致
//   ⑤ 工具数量 —— 随包分发的 skill 参考文档声称的工具数 = cmd/mcp 测试钉死的工具数
//   ⑥ Node 版本 —— .nvmrc 是 SSOT，Dockerfile 的 ARG 默认值必须与它相等，
//      且所有 setup-node 步骤必须读 .nvmrc 而不是各写一份字面量
//
// 运行：node scripts/check-version-drift.mjs
// 结果：全过 → exit 0；发现漂移 → 打印清单并 exit 1。

import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const issues = []

function read(rel) {
  return readFileSync(join(root, rel), 'utf8')
}

function report(where, expected, got) {
  issues.push(`${where}: 期望 ${expected}，实际 ${got === undefined ? '读不到' : got}`)
}

// ---------------------------------------------------------------- ① 版本 ----

const wails = read('wails.json')
const ssot = wails.match(/"productVersion"\s*:\s*"([^"]+)"/)?.[1]
if (!ssot) {
  console.error('ERROR: wails.json 里读不到 info.productVersion（版本 SSOT）')
  process.exit(1)
}

// 每个站点返回 { 字段名: 版本 }；同一文件里的多个字段必须一起相等。
const versionSites = [
  [
    'web/package.json',
    (t) => ({ version: JSON.parse(t).version }),
  ],
  [
    'web/package-lock.json',
    (t) => {
      const j = JSON.parse(t)
      return { version: j.version, 'packages[""].version': j.packages['']?.version }
    },
  ],
  [
    'internal/version/version.go',
    (t) => ({ 'const Version': t.match(/const Version\s*=\s*"([^"]+)"/)?.[1] }),
  ],
  [
    'ide/vscode/package.json',
    (t) => ({ version: JSON.parse(t).version }),
  ],
  [
    'ide/jetbrains/build.gradle.kts',
    (t) => ({ version: t.match(/^version\s*=\s*"([^"]+)"/m)?.[1] }),
  ],
  [
    'packaging/homebrew/Casks/repotory.rb',
    (t) => ({ version: t.match(/\n\s*version\s+"([^"]+)"/)?.[1] }),
  ],
  [
    'packaging/homebrew/Casks/repotory-mcp.rb',
    (t) => ({ version: t.match(/\n\s*version\s+"([^"]+)"/)?.[1] }),
  ],
  [
    'packaging/homebrew/Formula/repotory-mcp.rb',
    (t) => ({ version: t.match(/\n\s*version\s+"([^"]+)"/)?.[1] }),
  ],
  [
    'packaging/scoop/repotory.json',
    (t) => {
      const j = JSON.parse(t)
      return { version: j.version, 'url 里的 tag': j.url?.match(/\/v([^/]+)\//)?.[1] }
    },
  ],
  [
    'packaging/scoop/repotory-mcp.json',
    (t) => {
      const j = JSON.parse(t)
      return { version: j.version, 'url 里的 tag': j.url?.match(/\/v([^/]+)\//)?.[1] }
    },
  ],
]

for (const [file, extract] of versionSites) {
  if (!existsSync(join(root, file))) {
    report(file, ssot, '文件不存在')
    continue
  }
  let fields
  try {
    fields = extract(read(file))
  } catch (err) {
    report(file, ssot, `解析失败：${err.message}`)
    continue
  }
  for (const [field, value] of Object.entries(fields)) {
    if (value !== ssot) report(`${file} · ${field}`, ssot, value)
  }
}

// ------------------------------------------------------- ② TODO 现状速查 ----

// 只认「现状速查」那一行（以 "- 品牌 **Repotory**" 开头），不扫全文——TODO.md 里
// 还有大量「随 v2.15.0 发布改为……」这类历史叙述，它们本来就该停在过去。
const todoLine = read('TODO.md')
  .split('\n')
  .find((l) => l.startsWith('- 品牌 **Repotory**'))
if (!todoLine) {
  issues.push('TODO.md: 找不到「- 品牌 **Repotory**」开头的现状速查行')
} else {
  const todoVersion = todoLine.match(/版本 \*\*v([^*]+)\*\*/)?.[1]?.trim()
  if (!todoVersion) {
    issues.push('TODO.md: 现状速查行里读不到「版本 **vX.Y.Z**」')
  } else if (todoVersion !== ssot) {
    report('TODO.md · 现状速查的版本', ssot, todoVersion)
  }
}

// ------------------------------------------------------------ ③ ADR 集合 ----

const adrDir = join(root, 'docs/adr')
const adrFiles = readdirSync(adrDir)
  .filter((f) => f.endsWith('.md') && f !== 'index.md')
  .sort()

function indexLinks(rel) {
  const text = read(rel)
  const links = []
  const re = /\((\d{4}-[a-z0-9-]+\.md)\)/g
  let m
  while ((m = re.exec(text)) !== null) links.push(m[1])
  return links
}

const zhLinks = indexLinks('docs/adr/index.md')
const enLinks = indexLinks('docs/en/adr/index.md')

const fileSet = new Set(adrFiles)
const zhSet = new Set(zhLinks)
const enSet = new Set(enLinks)

for (const f of adrFiles) {
  if (!zhSet.has(f)) issues.push(`docs/adr/index.md: 漏列了 ${f}`)
  if (!enSet.has(f)) issues.push(`docs/en/adr/index.md: 漏列了 ${f}`)
}
for (const f of zhSet) {
  if (!fileSet.has(f)) issues.push(`docs/adr/index.md: 列了不存在的 ${f}`)
}
for (const f of enSet) {
  if (!fileSet.has(f)) issues.push(`docs/en/adr/index.md: 列了不存在的 ${f}`)
}

// 两份索引的链接集合必须逐条对应——标题文案本来就不同（中文 vs 英文），所以只比文件名。
if (zhSet.size !== enSet.size || [...zhSet].some((f) => !enSet.has(f))) {
  issues.push(
    `ADR 索引中英不对称：中文 ${zhSet.size} 条 / 英文 ${enSet.size} 条，差异 ${
      [...zhSet].filter((f) => !enSet.has(f)).map((f) => f).join(', ') ||
      [...enSet].filter((f) => !zhSet.has(f)).map((f) => f).join(', ')
    }`
  )
}

// ------------------------------------------------------------ ④ 扫描频率 ----

const INTERVAL_WORDS = {
  daily: '每天',
  weekly: '每周',
  monthly: '每月',
}

const dependabot = read('.github/dependabot.yml')
const intervals = [
  ...new Set([...dependabot.matchAll(/interval:\s*"([^"]+)"/g)].map((m) => m[1])),
]

if (intervals.length === 0) {
  issues.push('.github/dependabot.yml: 读不到任何 interval')
} else if (intervals.length > 1) {
  issues.push(
    `.github/dependabot.yml: 出现了多个 interval（${intervals.join('、')}），SECURITY.md 一句话说不清`
  )
} else {
  const interval = intervals[0]
  const word = INTERVAL_WORDS[interval]
  if (!word) {
    issues.push(`.github/dependabot.yml: 未知 interval "${interval}"，请在本脚本里补一个中文说法`)
  } else {
    const security = read('SECURITY.md')
    const lines = security.split('\n').filter((l) => l.includes('Dependabot'))
    if (lines.length === 0) {
      issues.push('SECURITY.md: 找不到提到 Dependabot 的句子')
    }
    const others = Object.entries(INTERVAL_WORDS)
      .filter(([k]) => k !== interval)
      .map(([, w]) => w)
    for (const line of lines) {
      if (!line.includes(word)) {
        issues.push(
          `SECURITY.md: Dependabot 那句没说清频率——dependabot.yml 是 ${interval}，应当出现「${word}」`
        )
      }
      for (const w of others) {
        if (line.includes(w)) {
          issues.push(`SECURITY.md: Dependabot 那句里的「${w}」与 dependabot.yml 的 ${interval} 不符`)
        }
      }
    }
  }
}

// ------------------------------------------------------------ ⑤ 工具数量 ----

// 工具清单的事实源是 cmd/mcp 的注册表，而注册表由 TestToolsRegistered 钉死。
// 随包分发的 skill 参考文档与文档站首页各自声称一个数量——三者必须相等，
// 否则装到用户机器上的文档、用户打开的文档站就在说谎。
const mcpTest = read('cmd/mcp/main_test.go')
const wantBlock = mcpTest.match(/want\s*:=\s*\[\]string\{([^}]*)\}/)?.[1]
const toolNames = wantBlock
  ? [...wantBlock.matchAll(/"(repotory_[a-z_]+)"/g)].map((m) => m[1])
  : []
if (toolNames.length === 0) {
  issues.push('cmd/mcp/main_test.go: 读不到 TestToolsRegistered 的工具清单')
} else {
  const toolCountClaims = [
    ['skill/repotory/references/mcp-tools.md', /(\d+)\s*个工具/],
    ['docs/index.md', /repotory-mcp<br\/>(\d+)\s*个工具/],
    ['docs/en/index.md', /repotory-mcp<br\/>(\d+)\s*tools/],
  ]
  for (const [file, re] of toolCountClaims) {
    const claimed = read(file).match(re)?.[1]
    if (!claimed) {
      issues.push(`${file}: 读不到声称的 MCP 工具数量`)
    } else if (Number(claimed) !== toolNames.length) {
      report(`${file} · 声称的 MCP 工具数`, toolNames.length, claimed)
    }
  }
}

// ------------------------------------------------------------ ⑥ Node 版本 ----

// .nvmrc 是 Node 版本的 SSOT：nvm 读它、actions/setup-node 的 node-version-file
// 读它、Dockerfile 的 ARG NODE_VERSION 抄它。这三处曾经各写一遍——CI 与 release
// 写着 node 20，而 Dockerfile 已经是 node:24-alpine。node 20 又已于 2026-04
// 结束支持（EOL），于是 CI 跑的是一个不再收安全更新的运行时。
//
// 抄写不掉的就不抄：Dockerfile 的 FROM 读不了文件，所以那里留一份 ARG 默认值，
// 由本检查兜住漂移；工作流则一律改成 node-version-file，连抄写的机会都不给。

const nodeMajor = (v) => Number(String(v).trim().replace(/^v/, '').split('.')[0])

if (!existsSync(join(root, '.nvmrc'))) {
  issues.push('.nvmrc: 不存在——它是 Node 版本的 SSOT')
} else {
  const nodeSSOT = read('.nvmrc').trim()
  if (!/^\d+(\.\d+)*$/.test(nodeSSOT)) {
    issues.push(`.nvmrc: 读不出版本号（读到 "${nodeSSOT}"）`)
  } else {
    const wantMajor = nodeMajor(nodeSSOT)
    const dockerArg = read('Dockerfile').match(/ARG\s+NODE_VERSION=([^\s]+)/)?.[1]
    if (!dockerArg) {
      issues.push('Dockerfile: 读不到 ARG NODE_VERSION')
    } else if (nodeMajor(dockerArg) !== wantMajor) {
      report('Dockerfile · ARG NODE_VERSION', `主版本 ${wantMajor}（.nvmrc = ${nodeSSOT}）`, dockerArg)
    }

    const wfDir = join(root, '.github/workflows')
    for (const f of readdirSync(wfDir).filter((f) => f.endsWith('.yml'))) {
      const rel = `.github/workflows/${f}`
      const lines = read(rel).split('\n')
      lines.forEach((line, i) => {
        if (!line.includes('actions/setup-node@')) return
        // 这一步的 with 块：从 uses: 往下读到下一个列表项为止
        const scope = []
        for (let j = i + 1; j < lines.length && j < i + 8; j++) {
          if (/^\s*-\s/.test(lines[j])) break
          scope.push(lines[j])
        }
        const block = scope.join('\n')
        if (/^\s*node-version\s*:/m.test(block)) {
          issues.push(
            `${rel}:${i + 1} 的 setup-node 还写着字面量 node-version —— 改成 node-version-file: '.nvmrc'`
          )
        } else if (!/node-version-file\s*:/.test(block)) {
          issues.push(`${rel}:${i + 1} 的 setup-node 既没有 node-version 也没有 node-version-file`)
        }
      })
    }
  }
}

// ------------------------------------------------------------ ⑦ Go 版本 ----

// 每个工作流的 go-version 必须完全相同，理由有二：
//
// ① 安全基线不能由 runner 镜像决定。`'1.26'` 解析到镜像里恰好带的那个补丁
//    版，而镜像**会往回走**：2026-11-03 同一棵树上 04:21 那次是 go1.26.10
//    （绿），06:40 那次是 go1.26.9（红，因为 1.26.9 还带着 GO-2026-6809）。
//    同一份代码在一小时里绿变红、中间什么都没改——这样的门不是门，是抛硬币。
// ② 发布用的工具链必须是被扫过的那一个。CI 用 A 编、release 用 B 出货，
//    意味着「绿」和「发出去」根本不是同一个工具链下的结论。
//
// 钉到确切补丁版之后，升级是一次有意识的动作；新补丁上的新公告会以「红」
// 的形式出现，而红的那句话就是「升 Go」。

const goVersions = new Set()
for (const f of readdirSync(join(root, '.github/workflows')).filter((f) => f.endsWith('.yml'))) {
  const rel = `.github/workflows/${f}`
  for (const m of read(rel).matchAll(/go-version:\s*'?([^'\s]+)'?/g)) {
    goVersions.add(m[1])
  }
}
const goList = [...goVersions]
if (goList.length === 0) {
  issues.push('.github/workflows/: 读不到任何 go-version')
} else if (goList.length > 1) {
  issues.push(
    `.github/workflows/: 出现了多个 go-version（${goList.join('、')}）——CI 测的工具链必须与 release 出货的同一个`
  )
} else if (!/^\d+\.\d+\.\d+$/.test(goList[0])) {
  issues.push(
    `.github/workflows/: go-version 写的是 "${goList[0]}"，不是确切补丁版——补丁版必须钉死，否则安全基线由 runner 镜像决定`
  )
}

// ------------------------------------------------------------------ 结论 ----

if (issues.length > 0) {
  console.error(`版本/计数漂移检测到 ${issues.length} 处：\n`)
  for (const line of issues) console.error(`  - ${line}`)
  console.error(
    '\n修法：版本类跑 ./scripts/bump-version.sh <X.Y.Z>（它会同步 TODO.md 的现状速查），' +
      '\n     ADR 集合在改完 docs/adr/ 后补进两份 index.md。'
  )
  process.exit(1)
}

console.log(
  `version drift OK: SSOT ${ssot}（10 处产物清单一致）、TODO 现状速查一致、` +
    `ADR ${adrFiles.length} 份中英索引对齐、扫描频率与 dependabot.yml 一致、` +
    `MCP ${toolNames.length} 个工具与 skill 文档一致、` +
    `Node ${read('.nvmrc').trim()} 与 Dockerfile/工作流一致、Go ${goList[0]} 各工作流一致。`
)
