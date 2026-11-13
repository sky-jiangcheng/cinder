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
// 十道检查：
//   ① 版本一致 —— wails.json 的 info.productVersion 是 SSOT，各产物清单必须与它相等
//      （打包清单按命名空间层发现，新增项目不必改本文件）
//   ② TODO 现状速查 —— TODO.md 顶部那行的版本必须与 SSOT 相等（由 bump-version.sh 同步）
//   ③ ADR 集合 —— docs/adr/ 的文件集合 = 中英两份索引列出的集合，且两份索引逐条对应
//   ④ 扫描频率 —— SECURITY.md 承诺的扫描频率必须与 .github/dependabot.yml 一致
//   ⑤ 工具数量与 fixture —— scripts/mcp-tools.fixture.json 与 cmd/mcp 测试的 want 清单
//      逐项相等；skill 参考文档、README 与文档站里每一处「N 个工具」声称 = fixture 长度
//   ⑥ Node 版本 —— .nvmrc 是 SSOT，Dockerfile 的 ARG 默认值必须与它相等，
//      且所有 setup-node 步骤必须读 .nvmrc 而不是各写一份字面量
//   ⑦ Go 版本 —— 每个工作流的 go-version 必须钉到同一个确切补丁版，
//      go.mod 的 toolchain 与 Dockerfile 的 ARG 必须与它相等
//   ⑧ 核心契约版本 —— internal/version/core.go 的 CoreVersion 必须与
//      cmd/mcp 测试里钉死的那一份相等（两者一起描述同一个工具面）
//   ⑨ 契约版本语义 —— 声明的变更类型（major/minor/patch）必须与版本号实际走了多远一致
//   ⑩ 文档工具名集合 —— 文档里出现的 repotory_* 名字 ⊆ fixture（兑现 ADR-0051 D1：
//      两张面的对齐关系写进断言，允许只列子集，但列出来的必须是真工具）
//
// 运行：node scripts/check-version-drift.mjs
// 结果：全过 → exit 0；发现漂移 → 打印清单并 exit 1。

import { readFileSync, readdirSync, existsSync, globSync } from 'node:fs'
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
]

// The packaging manifests live under a namespace layer (one directory per
// project), so they are DISCOVERED rather than listed. A new project must not
// have to edit this file for its version to be checked — the whole point of
// the namespace is that dropping a directory in is enough.
for (const file of [
  ...globSync('packaging/*/homebrew/Casks/*.rb'),
  ...globSync('packaging/*/homebrew/Formula/*.rb'),
  ...globSync('packaging/*/scoop/*.json'),
]) {
  versionSites.push(
    file.endsWith('.json')
      ? [
          file,
          (t) => {
            const j = JSON.parse(t)
            return { version: j.version, 'url 里的 tag': j.url?.match(/\/v([^/]+)\//)?.[1] }
          },
        ]
      : [file, (t) => ({ version: t.match(/\n\s*version\s+"([^"]+)"/)?.[1] })]
  )
}

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

// 只认「现状速查」那一行（以 "- 品牌 **Cinder**" 开头），不扫全文——TODO.md 里
// 还有大量「随 v2.15.0 发布改为……」这类历史叙述，它们本来就该停在过去。
const todoLine = read('TODO.md')
  .split('\n')
  .find((l) => l.startsWith('- 品牌 **Cinder**'))
if (!todoLine) {
  issues.push('TODO.md: 找不到「- 品牌 **Cinder**」开头的现状速查行')
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

// --------------------------------------------- ⑤ 工具数量与 fixture ----

// 工具清单的事实源是 cmd/mcp 的注册表，而注册表由 TestToolsRegistered 钉死。
// 它有两份机器可读副本：want 数组（Go 测试里的事实源）与
// scripts/mcp-tools.fixture.json（给文档门禁用的 fixture）。两者必须逐项相等，
// 否则「改了工具清单却没更新 fixture」会让下面所有计数/名字检查建立在旧清单上。
// 随包分发的 skill 参考文档与文档站的每一处计数声称都必须等于 fixture 的长度，
// 否则装到用户机器上的文档、用户打开的文档站就在说谎。
// 为什么逐个文件写正则而不是全文搜数字：历史上「13 → 18」那次漂移正是散落在
// 七份手册里的计数各改各的，全文匹配会把 ADR 与过程记录里**当时的实测值**也算成
// 漂移——那些是历史记录，不许按今天重写（docs/features/local-agent-recipe.md 里的
// 「18 个 MCP 工具」是 2026-10-29 那次真机验证量到的，不在本清单里）。
// 每个文件取**全部**匹配而不是第一个：一处改对、另一处漏改，才是这类漂移的常见形状。
const mcpTest = read('cmd/mcp/main_test.go')
const wantBlock = mcpTest.match(/want\s*:=\s*\[\]string\{([^}]*)\}/)?.[1]
const toolNames = wantBlock
  ? [...wantBlock.matchAll(/"(repotory_[a-z_]+)"/g)].map((m) => m[1])
  : []
if (toolNames.length === 0) {
  issues.push('cmd/mcp/main_test.go: 读不到 TestToolsRegistered 的工具清单')
}

// ① fixture 与 want 逐项相等（顺序也算：fixture 是清单的镜像，不是集合）。
// 只解析 want 清单，不去 grep 工具注册代码反推实现 —— 那样会把注释和字符串里的
// 名字也算进去。fixture 是唯一的、纳入 git 的机器可读副本。
const fixtureRel = 'scripts/mcp-tools.fixture.json'
let fixtureTools = []
try {
  const parsed = JSON.parse(read(fixtureRel))
  if (
    !Array.isArray(parsed) ||
    parsed.some((n) => typeof n !== 'string' || !/^repotory_[a-z_]+$/.test(n))
  ) {
    issues.push(`${fixtureRel}: 必须是「repotory_* 工具名」字符串数组`)
  } else {
    fixtureTools = parsed
  }
} catch (err) {
  issues.push(`${fixtureRel}: 读不到或不是合法 JSON（${err.message}）`)
}
if (fixtureTools.length > 0 && toolNames.length > 0) {
  const sameOrder =
    fixtureTools.length === toolNames.length &&
    fixtureTools.every((n, i) => n === toolNames[i])
  if (!sameOrder) {
    const fixtureSet = new Set(fixtureTools)
    const wantSet = new Set(toolNames)
    const onlyFixture = fixtureTools.filter((n) => !wantSet.has(n))
    const onlyWant = toolNames.filter((n) => !fixtureSet.has(n))
    const hint =
      onlyFixture.length || onlyWant.length
        ? `fixture 独有：${onlyFixture.join('、') || '无'}；want 独有：${onlyWant.join('、') || '无'}`
        : '集合相同但顺序不同（fixture 必须与 want 同序）'
    issues.push(
      `${fixtureRel}: 与 cmd/mcp/main_test.go 的 want 清单不一致 —— ` +
        `fixture ${fixtureTools.length} 项 / want ${toolNames.length} 项；${hint}`
    )
  }
}

// ② 计数声称指向 fixture 的长度，而不是再抄一遍数字。
if (fixtureTools.length > 0) {
  const toolCountClaims = [
    ['README.md', /(\d+)\s*MCP tools/],
    ['README.zh-CN.md', /(\d+)\s*个\s*MCP\s*工具/],
    ['skill/repotory/references/mcp-tools.md', /(\d+)\s*个工具/],
    ['skill/repotory/SKILL.md', /(\d+)\s*个工具，按意图选/],
    ['docs/index.md', /(?:cinder|repotory)-mcp<br\/>(\d+)\s*个工具/],
    ['docs/en/index.md', /(?:cinder|repotory)-mcp<br\/>(\d+)\s*tools/],
    ['docs/features-overview.md', /stdio 服务器，(\d+)\s*个工具/],
    ['docs/en/features-overview.md', /stdio server, (\d+)\s*tools/],
    ['docs/storage-optimization.md', /的 (\d+)\s*个工具来消费它/],
    ['docs/en/storage-optimization.md', /the (\d+)\s*tools of/],
    ['docs/getting-started.md', /(?:cinder|repotory)-mcp` (\d+) 个 MCP 工具/],
    ['docs/en/getting-started.md', /with (\d+)\s*tools/],
    ['docs/architecture.md', /(?:cinder|repotory)-mcp · (\d+) 个 MCP 工具/],
    ['docs/en/architecture.md', /(?:cinder|repotory)-mcp · (\d+) MCP tools/],
    ['docs/features/ai-integration.md', /(\d+)\s*个工具/],
    ['docs/en/features/ai-integration.md', /(\d+)(?: MCP)? tools/],
  ]
  for (const [file, re] of toolCountClaims) {
    const text = read(file)
    const claimed = [...text.matchAll(new RegExp(re.source, `${re.flags}g`))].map((m) => m[1])
    if (claimed.length === 0) {
      issues.push(`${file}: 读不到声称的 MCP 工具数量`)
      continue
    }
    for (const c of claimed) {
      if (Number(c) !== fixtureTools.length) {
        report(`${file} · 声称的 MCP 工具数`, fixtureTools.length, c)
      }
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
//
// 抄写不掉的就不抄（与 ⑥ Node 同一形状）：本地开发机读不到工作流文件，所以
// SSOT 落在 go.mod 的 `toolchain` 行——没有它，`go 1.26.0` 只是**下限**，
// 开发机跟着包管理器走。实测踩过：Homebrew 装 1.26.7 时，本机 govulncheck 扫出
// 11 个 stdlib 漏洞（1.26.9 之前修的），而 CI 全绿——两边说的不是同一个工具链。
// Dockerfile 的 FROM 读不了 go.mod，所以那里留一份 ARG GO_VERSION 默认值。

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

const pinnedGo = goList.length === 1 ? goList[0] : null

// go.mod 的 toolchain 行 = 本地开发机那一份（GOTOOLCHAIN=auto 读它）。
const toolchainLine = read('go.mod').match(/^toolchain\s+go(\d+\.\d+\.\d+)\s*$/m)?.[1]
if (!toolchainLine) {
  issues.push(
    'go.mod: 缺 `toolchain goX.Y.Z`——`go 1.26.0` 只是下限，开发机跟着包管理器的补丁版走，' +
      '本地 govulncheck 与 CI 扫的就不是同一个工具链'
  )
} else if (pinnedGo && toolchainLine !== pinnedGo) {
  report('go.mod · toolchain', pinnedGo, toolchainLine)
}

// Dockerfile 的 ARG GO_VERSION = 出货镜像那一份（FROM 读不了 go.mod）。
const dockerGo = read('Dockerfile').match(/ARG\s+GO_VERSION=([^\s]+)/)?.[1]
if (!dockerGo) {
  issues.push(
    'Dockerfile: 读不到 ARG GO_VERSION——`FROM golang:1.26-alpine` 这类浮动标签跟着镜像缓存走，' +
      '出货二进制可能带着没人扫过的补丁版'
  )
} else if (!/^\d+\.\d+\.\d+$/.test(dockerGo)) {
  issues.push(`Dockerfile: ARG GO_VERSION="${dockerGo}" 不是确切补丁版——补丁版必须钉死`)
} else if (pinnedGo && dockerGo !== pinnedGo) {
  report('Dockerfile · ARG GO_VERSION', pinnedGo, dockerGo)
}

// -------------------------------------------------- ⑧ 核心契约版本 ----

// 记忆层的契约版本（core version）与发布版本各走各的：它描述的是「客户端
// 可以依赖什么」，所以它与工具面一起动。事实源是 internal/version/core.go，
// cmd/mcp/main_test.go 里钉了同一份字面量（就在工具清单旁边）——两份必须相等，
// 否则「版本号说没变、工具面变了」这件事就没人能发现了。
const coreSrc = read('internal/version/core.go')
const coreVersion = coreSrc.match(/const CoreVersion\s*=\s*"([^"]+)"/)?.[1]
const pinnedCoreVersion = mcpTest.match(/const wantCoreVersion\s*=\s*"([^"]+)"/)?.[1]
if (!coreVersion) {
  issues.push('internal/version/core.go: 读不到 const CoreVersion')
} else if (!/^\d+\.\d+\.\d+$/.test(coreVersion)) {
  issues.push(`internal/version/core.go: CoreVersion = "${coreVersion}"，不是 X.Y.Z 三段式`)
}
if (!pinnedCoreVersion) {
  issues.push('cmd/mcp/main_test.go: 读不到 const wantCoreVersion——契约版本必须与工具清单钉在同一个地方')
} else if (coreVersion && pinnedCoreVersion !== coreVersion) {
  report('cmd/mcp/main_test.go · 钉死的契约版本', coreVersion, pinnedCoreVersion)
}

// ------------------------------------- ⑨ 契约版本语义（ADR-0055 D1） ----

// 第⑧门只比两份字面量相等，查不出「删了工具却只涨 minor」。core.go 现在把
// 上一版与本次变更类型一起写成常量，这里校验「声明的类型」与「版本号实际
// 走了多远」一致。工具清单 diff 的自动判定仍属 TODO 12.4，不在本门范围。
const declaredPrev = coreSrc.match(/const CoreVersionPrevious\s*=\s*"([^"]+)"/)?.[1]
const declaredKind = coreSrc.match(/const CoreVersionChange\s*=\s*"([^"]+)"/)?.[1]
const parseVer = v => {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(v || '')
  return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null
}
const prevVer = parseVer(declaredPrev)
const currVer = parseVer(coreVersion)
if (!prevVer || !currVer || !declaredKind) {
  issues.push('internal/version/core.go: 契约版本语义三件套（CoreVersion / CoreVersionPrevious / CoreVersionChange）缺失或不是 X.Y.Z')
} else {
  const [pMaj, pMin, pPat] = prevVer
  const expectedByKind = {
    major: `${pMaj + 1}.0.0`,
    minor: `${pMaj}.${pMin + 1}.0`,
    patch: `${pMaj}.${pMin}.${pPat + 1}`,
  }
  if (!(declaredKind in expectedByKind)) {
    issues.push(`internal/version/core.go: CoreVersionChange = "${declaredKind}"，只允许 major / minor / patch`)
  } else if (coreVersion !== expectedByKind[declaredKind]) {
    report('internal/version/core.go · 契约版本语义', expectedByKind[declaredKind], coreVersion)
  }
}

// ----------------------------------- ⑩ 文档工具名集合（ADR-0051 D1） ----

// ADR-0051 决策 D1 承诺「两张面的对齐关系写进 check-version-drift 的断言」，
// 代价一节又写「靠 check-version-drift 的断言兜底」。这里兑现它：文档是绑定面 /
// 工具面的**声称面**，凡是写出一个 repotory_* 名字，就必须在 fixture 里存在。
// 允许只列子集（根 SKILL.md 的速查表只列 13 个是有意的），但列出来的每一个都得是
// 真工具 —— 拼错、改名漏改、旧名残留都会在这里报红。docs/api/reference.md
// （及英文镜像）记录的是 Wails 绑定面与 MCP 孪生关系，一并纳入，正是「两张面对齐」
// 被断言的那一面。
if (fixtureTools.length > 0) {
  const fixtureSet = new Set(fixtureTools)
  const toolNameDocs = [
    'README.md',
    'README.zh-CN.md',
    'SKILL.md',
    'skill/repotory/SKILL.md',
    'skill/repotory/references/mcp-tools.md',
    'docs/api/reference.md',
    'docs/en/api/reference.md',
  ]
  for (const file of toolNameDocs) {
    const names = [
      ...new Set([...read(file).matchAll(/\brepotory_[a-z_]+\b/g)].map((m) => m[0])),
    ].sort()
    const unknown = names.filter((n) => !fixtureSet.has(n))
    if (unknown.length > 0) {
      issues.push(`${file}: 列出了 fixture 里不存在的工具名 ${unknown.join('、')}`)
    }
  }
}


// ------------------ ⑪ 行为对等 fixture（ADR-0059 门 B / TODO 13.2） ----

// 三端行为契约的 fixture（scripts/shared-chat-conformance.fixture.json）是
// 目标契约：settled 规则三端都要断言，skipped 规则显式挂起（TODO 11.1-11.3）。
// 这道门管「同一份契约」的三件事：
//   ① 文件可解析、结构自洽（规则数 = contract_version.rules）
//   ② 规则清单与三处叙述同源——fixture、internal/conformance 的守卫测试
//      名单、两份 ADR-0059 记录的规则 id 列表一致，一处加规则别处不跟即红
//   ③ skipped 规则必须带 assert_forbidden 与 TODO 编号（防 runner 偷跑）
{
  const fPath = 'scripts/shared-chat-conformance.fixture.json'
  if (existsSync(fPath)) {
    let parsed = null
    try {
      parsed = JSON.parse(read(fPath))
    } catch (e) {
      issues.push(`${fPath}: 不是合法 JSON（${e.message}）`)
    }
    if (parsed) {
      const rules = Array.isArray(parsed.rules) ? parsed.rules : []
      if (!rules.length) {
        issues.push(`${fPath}: 没有任何规则——空契约是门不存在的可笑写法`)
      } else {
        const ids = rules.map((r) => r.id)
        if (new Set(ids).size !== ids.length) {
          issues.push(`${fPath}: 规则 id 重复`)
        }
        const declared = parsed.contract_version?.rules
        if (declared !== rules.length) {
          issues.push(`${fPath}: contract_version.rules = ${declared}，文件里有 ${rules.length} 条规则`)
        }
        for (const r of rules) {
          if (r.status === 'settled') {
            if (r.expect === undefined) {
              issues.push(`${fPath}: settled 规则 ${r.id} 没有 expect`)
            }
            if (r.skip_reason || r.assert_forbidden) {
              issues.push(`${fPath}: settled 规则 ${r.id} 不该带 skip 字段`)
            }
          } else if (r.status === 'skipped') {
            if (!r.skip_reason || !r.skip_reason.includes('11.')) {
              issues.push(`${fPath}: skipped 规则 ${r.id} 的 skip_reason 必须点名它等的 TODO 11.x`)
            }
            if (r.assert_forbidden !== true) {
              issues.push(`${fPath}: skipped 规则 ${r.id} 必须设 assert_forbidden——没有这道闸，runner 可以把未拍板的差异当标准断言`)
            }
            if (r.expect !== undefined) {
              issues.push(`${fPath}: skipped 规则 ${r.id} 带着永远不跑的 expect`)
            }
          } else {
            issues.push(`${fPath}: 规则 ${r.id} 的 status 是 ${JSON.stringify(r.status)}，只认 settled / skipped`)
          }
        }
        // 规则 id 与叙述同源：TODO 13.2 条目里点名的规则 id ⊆ fixture，
        // fixture 里每个 id 都在 TODO 里出现过（或属内部派生名）。
        const todo = read('TODO.md')
        const idRegex = /(?:pairing|fallback|scope)\.[a-z_]+(?:\.[a-z_]+)?/g
        const todoIds = new Set(todo.match(idRegex) ?? [])
        for (const id of ids) {
          if (!todoIds.has(id)) {
            issues.push(`${fPath}: 规则 ${id} 在 TODO.md 里没有出现——契约与叙述漂了（改 TODO 13.2 的清单或撤掉这条规则）`)
          }
        }
      }
    }
  } else {
    issues.push(`${fPath}: 不存在——ADR-0059 门 B 的 fixture 缺失，三个 runner 将无从读起`)
  }
}

// ------------------------------------------------------------------ 结论 ----

if (issues.length > 0) {
  console.error(`版本/计数漂移检测到 ${issues.length} 处：\n`)
  for (const line of issues) console.error(`  - ${line}`)
  console.error(
    '\n修法：版本类跑 ./scripts/bump-version.sh <X.Y.Z>（它会同步 TODO.md 的现状速查），' +
      '\n     ADR 集合在改完 docs/adr/ 后补进两份 index.md；' +
      '\n     改了 MCP 工具清单就同步 scripts/mcp-tools.fixture.json 与各文档的「N 个工具」声称。'
  )
  process.exit(1)
}

console.log(
  `version drift OK: SSOT ${ssot}（${versionSites.length} 处产物清单一致）、TODO 现状速查一致、` +
    `ADR ${adrFiles.length} 份中英索引对齐、扫描频率与 dependabot.yml 一致、` +
    `MCP ${fixtureTools.length} 个工具（fixture 与 want 逐项相等、文档计数与工具名一致）、契约版本 ${coreVersion} 与测试钉死的一致、` +
    `Node ${read('.nvmrc').trim()} 与 Dockerfile/工作流一致、Go ${goList[0]} 各工作流一致、` +
    `行为 fixture ${(() => { try { const p = JSON.parse(read('scripts/shared-chat-conformance.fixture.json')); const st = p.rules.filter(r=>r.status==='settled').length; const sk = p.rules.filter(r=>r.status==='skipped').length; return `${st} settled / ${sk} skipped`; } catch { return '??'; } })()} 三端同源。`
)
