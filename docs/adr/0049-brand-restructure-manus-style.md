# ADR-0049: 品牌按 Manus 式重排——母品牌换名、每个面独立名字、标识层冻结

状态：**Accepted（2026-11-05，用户在「品牌与 UI 怎么重新定义」的四条路线里选 (c)「全量 Manus 式重排」；同日定下三个词：母品牌 **Cinder** / 桌面 **Cinder Desktop** / Agent **Warden**——落地过程见文末「落地记录」）**
日期：2026-11-05
关联：[ADR-0037](0037-brand-legibility-and-ui-language.md)（品牌辨识度与界面语法——本篇**修订其决策 4 的适用范围**）、[ADR-0047](0047-resident-agent-separate-host.md)（两个产品各担一件事）、[ADR-0048](0048-core-contract-version.md)（契约版本）、[ADR-0034](0034-desktop-three-column-shell.md)（显示名与二进制名分层的先例）、[ADR-0006](0006-scope-freeze.md)（范围冻结与冻结标识）
起始 schema：v28（**本 ADR 不改 schema**）

## 背景

### 起因：一句提问，暴露的是一个结构问题

用户问：「**Repotory 这个品牌名真的好吗？现在的 UI 设计真的好吗？参考 Manus 重新思考一下**」。

这不是一个「换个名字」的请求，而是一次结构体检。查证过程中翻出四件互相咬合的事实，它们把「名字好不好」这个主观问题，收敛成了一个可判定的结构问题：**「Repotory」这个词同时指三样东西，而仓内自己的散文早就给这三样东西分好了角色。**

### 事实一：仓内已经做过一次品牌诊断，但它明确绕开了名字这一半

[ADR-0037](../../docs/adr/0037-brand-legibility-and-ui-language.md) 决策 4 写着：

> **品牌名 `Repotory` 不动**：冻结标识（ADR-0006）。「辨识度」问题在**视觉构造**上，不在名字上。

那不是托词——ADR-0037 的诊断做得很实（母图的「R」是全世界最通用的字母、hairline 在 26px 下只剩 0.46px、UI token 还是 Tailwind 蓝而母图是暖纸奶油）。但它回答的是**另一个问题**：那时用户提的是「辨识度不高」（视觉），今天问的是「名字好不好」（词）。**同一件事的两个切面，那份结论只覆盖一个。** 更关键的是，它把「不改品牌名」写成了**决策**而不是**待验证项**——于是今天这个新问题没法推翻它，只能被补充。

### 事实二：散文里已经分好层了，产品名没跟上

| 角色 | 仓内已有口径 | 产品名现状 |
|---|---|---|
| 引擎 / 记忆层 | 首屏口径：「The local-first memory layer for AI coding agents」（[README.md:8](../../README.md)）；产物表称 `repotory-mcp` 为「**Core agent memory engine**」（[README.md:64](../../README.md)） | `repotory-mcp` ✓ |
| 桌面工作台 | 产物表明确叫「**Repotory Desktop**」（[README.md:65](../../README.md)） | `wails.json` 的 `productName` 是「**Repotory**」——**面与引擎同名** |
| 第二个产品 | —— | 「**Repotory Agent**」（`repotory-agent` 仓 `tauri.conf.json`）——**限定词后缀型** |

也就是说：**「Repotory Desktop」这个口径只活在 README 里，App 自己不认**。这是与选词无关的一处现成不一致——**无论走哪条路线都得修**。

### 事实三：Manus 的可迁移之处不是「换个更好的词」

Manus 2026-09-28 之后的结构是：**母品牌（Manus）= 平台/引擎；第一个面 = Manus Studio（母品牌 + 角色限定词）；第二个面 = Cue（完全另一个词，上架名才是 "Cue by Manus"）**。

关键点：**引擎没拿到独立名字，名字给了「面」**。而我们今天是反过来的——「Repotory」既当引擎又当第一个面，第二个面叫「Repotory Agent」（字面意思是「服务于 Repotory 的 agent」，天然是附属品而不是独立产品）。**Manus 用一个独立词 Cue 解决的，正是我们给 Agent 撞上的这堵墙。**

### 事实四：语义直白的记忆词，恰好都被这个赛道占了

选词的第一直觉是挑一个「意思正好是记忆」的词。查证结果把这批词**全部否掉**：

| 候选 | 撞上的东西 |
|---|---|
| **Engram**（神经科学里「记忆的物理痕迹」） | `Gentleman-Programming/engram`——**定位与我们一字不差**：「a standalone tool you can use with any MCP-compatible agent」，持久记忆层；另有 Sphere Inc 的 Engram Enterprise（企业级持久 AI 记忆） |
| **Trove**（一箱珍贵之物） | `Lowside-Labs/Trove`——「把收藏的网页变成本地库，然后让 Claude Code / Codex 对着它问」——**同一个叙事** |
| **Cairn**（路标石堆） | ① `Cairn: A Local Tool for AI Agent Architecture Memory`（Reddit，ClaudeWorkflows）——**又是同赛道**；② 开源 AI 渗透测试工具 Cairn，因被黑客大规模利用上了 Reuters |

**这条事实改变了选词策略**：AI-memory 这个赛道里，**语义直白的词已经被占了**——因为大家都在找同一批词。可行的只有两条路：① 挑一个**语义偏冷门但辨识度高**的真词（买辨识度，代价是要解释一次）；② 挑一个**来自别的行当的词**（像 Manus 的「by hand」、Cue 的「give it a cue」——**含义来自故事，不来自定义**）。

### 事实五：仓名已经改过三轮，迁移机制是「一代一迁」

`internal/platform/platform.go` 的包注释写着：数据目录经历过 **gitboard → gitbuddy → reponest → repotory**；`dirName = "repotory"`，`legacyDirName = "reponest"`，注释明说「**One generation back is handled per migration, exactly as each earlier rename did in its day**」。

于是有一条必须写进决策的事实：**这是第四次改名，数据目录迁移要再写一代**，且跳代的用户（旧目录不是上一代）会失去自己的数据目录。同时，**这轮改名不能是又一次「改一半」**——ADR-0037 的背景里记着本品牌历史上「四次半落地」的教训（活动栏 / 工具窗 / favicon / README 各漏一次）。

### 事实六：冻结标识与显示名是两层，仓里已经分过一次

[README.md:221](../../README.md) 列着冻结标识：`repotory` = 二进制名、用户数据目录、MCP server `repotory-mcp`、工具前缀 `repotory_*`。而 [ADR-0034](../../docs/adr/0034-desktop-three-column-shell.md) 决策 6 已经做过一次**只改显示层、不动标识层**的改名：`wails.json` 的 `name` 改成 `Repotory`（`outputfilename` 仍是 `repotory`），**二进制名 `repotory` 是冻结标识，只改 bundle 目录这一层**。

**所以「改名很贵」是错觉**：贵的是品牌资产清零（母图 + ADR-0037 决策 1 的「可复述构造」要重新验证一次），不是迁移。迁移只动显示层。

## 决策

### ① 走路线 C：显示层全量重排

**品牌与仓的显示层全部重排**：母品牌换词、桌面工作台与 Agent 各自拿自己的名字、两个仓的显示名与文档站导航同步。

**路由层（工具前缀、二进制名、数据目录、MCP server 名）不动**（理由见决策③）。

### ② 三个槽位，各归其位

| 槽位 | 是什么 | 命名形态 |
|---|---|---|
| **母品牌** | 引擎 / 记忆层（`repotory-mcp` 背后的那份东西） | 一个独立词 |
| **桌面工作台** | Wails 桌面 App（浏览、编辑、审批、治理） | 母品牌 + 角色限定词（Manus Studio 式）**或**另一个独立词 |
| **Agent** | 独立仓的 copilot（读、聊、执行） | **完全另一个词**（Cue 式），**不再叫「X Agent」** |

**「X Agent」这种限定词后缀命名被否**：它把第二个产品读成第一个产品的附属品——而 ADR-0047 决策③ 已经把 Agent 定成**独立产品**（独立仓、独立栈、自己的托盘唤回）。**名字不能比决策更旧。**

### ③ 标识层冻结：工具前缀、二进制名、数据目录、MCP server 名一字不动

`repotory_*` / `repotory` / `repotory-mcp` / `repotory-server` / `~/…/repotory/repotory.db` **全部保留**。理由三条：

1. **它们是接口，不是品牌**——改它们会断掉每个用户机器上的 `claude mcp add` 配置、IDE 插件白名单、hook 脚本；换来的收益只是命令行里换个词。
2. **ADR-0006 的冻结纪律仍在**，且 ADR-0034 已有「只改显示层」的先例。
3. **真要动它们，正确做法是契约 major bump**（ADR-0048 刚立的语义：major = 破坏性）——**但那要等到有第二个真实消费者时再做**。在用户数还在个位数的个人工具上零用户先造一次迁移，是把纪律提前用在了没有收益的地方。

**代价要写清**：改完之后，**品牌名与命令行里的名字会不一致**（品牌是新词，二进制仍是 `repotory-mcp`）。这是**有意的**：接口名一旦发出去就是别人的依赖，改名是破坏性变更的活儿。

### ④ 仓显示名与文档站导航同步

两个仓（`sky-jiangcheng/repotory` 与 `repotory-agent`）的**显示名**、README 首屏、文档站首页与侧栏、IDE 插件的 `displayName` 与命令标题、Homebrew cask 的 `name`、安装脚本的提示文案——**同一批一起改**。

**GitHub 仓库的 slug 改名**：GitHub 会自动重定向旧 URL，因此**可做**，但会让旧 clone 的 `git remote` 需要手动改一次——**这一条要写进发布说明**。

**历史文档不改写**：ADR / CHANGELOG 里出现的旧名是历史记录，按仓库惯例保留原样（**只改当前面，不改历史面**）。

### ⑤ 选词硬约束（八条）

任何候选词必须同时满足：

1. **不与 `repository` / `repotory` 形近**（旧词的问题不能重犯）；
2. **英语无政治含义、无种族/地域忌讳**；
3. **2–3 音节，听一遍能拼对**（重音必须唯一）；
4. **`.com` 之外至少一个 TLD 可注册**、GitHub org / 用户名可拿；
5. **npm / crates.io / PyPI 上无同名包**（本仓分发的 `repotory-init`、agent 的 Tauri 产物、随包分发的 skill）；
6. **中文读音无歧义**（本仓主要用户是中文开发者）；
7. **不与仓内既有概念撞名**——例如 `hermes` 已经是本仓的**一个 import 来源名**（Nous Research 的 Hermes agent 的记忆），因此 **Hermes 不可用**；
8. **能当动词或名词读出来**：`<brand>_context` / `<brand>-mcp` / `~/…/<brand>/` 三处都要顺口。

### ⑥ 词的选择（候选池与占用排查）

**已定（见文末「落地记录」）：母品牌 Cinder / 桌面 Cinder Desktop / Agent Warden。**

**母品牌（引擎）候选**：

| 词 | 语义出处 | 优势 | 风险 |
|---|---|---|---|
| **Lichen**（地衣） | 地衣贴附岩石生长、记录环境、极长寿；**地衣测年法**就是靠地衣生长量给岩石断代 | 近乎无人占用的「会记录时间的活物」；两音节 | 语义冷门，要解释一次；npm 已有 `@lichen-ai` scope |
| **Cinder**（余烬） | 火灭之后仍然红着的那一点 | 短、无歧义、「持久但不再燃烧」的意象 | 偏冷；未见明显占用 |
| **Attic**（阁楼） | 旧东西堆放的地方 | 一开口就懂；`github.com/attic` 当前 404（可注册） | 暗示「陈旧 / 被遗忘」，与「记忆是活的」有张力 |
| **Steward**（管家 / 受托人） | 替人保管产业的 | 语义正：记忆层替人保管项目上下文 | 偏「人」的名词；中文音译与人名相撞 |
| **Colophon**（版本说明页） | 书末那行「这本书是怎么来的」 | 极独特、几乎无人占用；正对「出处与来源」 | 三音节、偏文学 |

**桌面工作台候选**：`母品牌 + 限定词`（`Bench` / `Desk` / `Board`——`Studio` 已被 Manus 占用、且与 JetBrains Studio 撞）或另一个独立词（**Counter** / **Atrium** / **Concourse** / **Agora**）；**零争议备选**是沿用 README 已有口径的「母品牌 + Desktop」。

**Agent 候选（Cue 式独立词）**：**Warden**（守夜人）/ **Torch**（提灯）/ **Corvid**（鸦科，动物界以记忆著称）/ **Ranger**（巡林人）/ **Sherpa**（引路人——**附伦理提示：它是一个真实民族的名字，商用需谨慎**）。**Hermes 已被排除**（见约束⑦）。

### ⑦ 不动 schema、不改 MCP 工具面、不涨契约版本

本篇是**命名与显示层**的决策。工具名一个不改 → **契约版本不动**（ADR-0048 的 `CoreVersion` 保持 `1.0.0`）。数据目录迁移是**文件改名**，不是 schema 变更。

## 代价（接受）

- **品牌资产清零**：母图 `build/icon.svg` 里那个「R」字母不再是品牌字母，ADR-0037 决策 1 的「可复述构造」要重新验证一次（新字母 + 同一套 hairline 规则是否还成立）。**这是本篇最大的成本**，不是迁移。
- **名字与命令行会不一致**：品牌是新词，`repotory-mcp` 仍是旧名。**这是决策③的有意代价**，但它要求文档每一处都写清「这是同一个东西的两个名字」。
- **第四次数据目录迁移**：要再写一代 `legacyDirName`，且跳代用户（数据目录停在 `reponest` 或更早）会失去数据目录——**这一代之后没有退路**，所以本篇之后应当**冻结改名**。
- **三个词要一次拍齐**：母品牌、桌面、Agent 三者要么一起换，要么都不换；只换一半比不改更糟（会多出第四种称呼）。
- **历史文档不改写**：仓里会同时存在新旧两个名字，这是对的，但读历史的人可能困惑。
- **GitHub 仓 slug 改名**会要求老 clone 手动改一次 remote。

## 未决（本篇明确不决）

- **三个词具体叫什么**：待用户拍板（本篇已给候选池与占用排查结果）。
- **桌面工作台用「母品牌 + 限定词」还是完全独立词**：与选词同时定。
- **母图是否重画**：字母随品牌换，但 hairline 三域规则与「印出来的卡」构造**保持**——本篇不动 `build/icon.svg`，重画随落地批次一起做。
- **UI 减法与主次重构**（元数据压内容、常驻右栏、悬浮机器人压正文）：那是**独立的一篇 ADR**，与命名无关，不在本篇范围。
- **仓 slug 是否真改**：倾向改，但可先只改显示名，slug 留到发布批次。

## 落地清单

1. **选词**：用户从候选池拍定三个词 → 补进本篇「落地记录」段。
2. **主仓显示层**：`wails.json`（`name` / `productName`，**`outputfilename` 不动**）、`web/index.html`（title / description / apple-mobile-web-app-title）、`web/src/locales/*` 的产品名词、`packaging/homebrew/Casks/*.rb` 的 `name` / `app` stanza、`scripts/install.sh` / `install.ps1` 的 `APP_NAME` 与提示文案、`scripts/build-release-assets.sh` / `build.sh` 的抬头、`.github/ISSUE_TEMPLATE/*`。
3. **主仓文档面**：README（首屏 + 产物表 + 冻结标识表）、README.zh-CN、文档站首页与侧栏、`docs/**` 与 `docs/en/**` 的当前面（**历史 ADR / CHANGELOG 不改**）。
4. **IDE 面**：`ide/vscode/package.json` 的 `displayName` 与各命令 `title`、`ide/jetbrains` 的 `plugin.xml` 的 `<name>`（**`<id>` 不动**——它是插件标识）。
5. **Agent 仓**：`tauri.conf.json` 的 `productName`（**`identifier` 不动**）、README（中英）、`docs/product.md`（中英）的称谓表。
6. **数据目录迁移**：`internal/platform/platform.go` 增一代 `legacyDirName`（`repotory` → 新名），沿用既有 `migrateLegacyData` 的失败姿态（**改名失败不影响启动**）。
7. **守门**：新增/扩展 `scripts/check-version-drift.mjs` —— **品牌名在产品面必须只有一个真值**（新旧名并存处必须是显式白名单，例如冻结标识与历史文档）；`scripts/check-docs-mirror.mjs` 继续守中英双份。
8. **同批写**：CHANGELOG（中英各一条，**首行是加粗标题**）、`TODO.md` 现状速查、本 ADR 的「落地记录」段、ADR 索引（中英各一行，下一个编号从 `0050` 递增）。

## 落地记录（2026-11-05 补记）

### 三个词

| 槽位 | 词 | 出处 |
|---|---|---|
| 母品牌（引擎 / 记忆层） | **Cinder** | 候选池里的「火灭之后仍然红着的那一点」——持久，但不再燃烧 |
| 桌面工作台 | **Cinder Desktop** | 沿用 README 已有口径「母品牌 + 限定词」，不新造词 |
| Agent（独立仓 copilot） | **Warden** | 候选池里的「守夜人」——独立产品，不再是「X Agent」 |

三个词**一次拍齐**（决策②的代价：只换一半比不改更糟）。母品牌与 Agent 的语义出处刻意不同——**引擎与助手不是同一个角色**，这正是「名字不能比决策更旧」。

### 与落地清单的两处偏差（都往「冻结」那一侧收）

落地清单第 6 条与决策③**自相矛盾**：清单要写下一代 `legacyDirName`（数据目录 `repotory` → 新名），而决策③明写「工具前缀、二进制名、**数据目录**、MCP server 名一字不动」。**以决策③为准**：

1. **数据目录不迁移**（第四次迁移取消）：`internal/platform/platform.go` 的 `dirName = "repotory"` 与 `legacyDirName = "reponest"` **一字未动**，只补了一条说明注释（为什么品牌换了、目录名没换）。**代价**：用户的数据目录仍叫 `repotory`，命令行里仍叫 `repotory-mcp`——这正是决策③写下的「有意的代价」。**收益**：没有第四代迁移，没有跳代用户（数据目录停在 `reponest` 或更早）丢目录，**这一代之后也没有「再迁一次」的余地了**。
2. **DB sentinel 不动、schema 不 bump**：库里那个 `repotory://memory/default` 是**数据**不是 schema，但它同样是「别人机器上的字符串」——按标识层处理，一字不动，**schema 版本保持 v28**。落地讨论中曾一度打算改它并 bump schema，**最终按标识层冻结办**；此处写明，免得下一个人以为它是漏掉的。

### 母图与图标

- `build/icon.svg` 的字母 **R → C**：字形取自 IBM Plex Sans Bold（© IBM Corp., SIL OFL 1.1）的 C，描边后缩放到 320 单位 cap height，**按视觉重心居中而不是按包围盒**——C 的开口朝右，所以墨迹落在包围盒中心右侧 9 单位（它替下的 R 用的是同一条规则的一半：6 单位）。**hairline 框（9 单位、inset 26）与纸面底色 `#F5F0E4` 一个字没动**——ADR-0037 决策 1 的「可复述构造」保住的是那套规则，不是那个字母。
- 全部派生件重生成：品牌域（favicon / 商店 / JetBrains 工具窗内的品牌标记）、OS 应用域（appicon / 阶梯 / ico）、字形域（VS Code 活动栏 24px、JetBrains 侧栏 16px + `_dark`、macOS 托盘 22px 单色透明底）。
- **`scripts/generate-icons.mjs` 新增 `--agent-repo <path>`**：一条命令把独立仓整套 Tauri 图标派生出来（15 个 PNG + `icon.ico` + `icon.icns` + `tray-22.png`）。**此前那是个真缺口**：独立仓的 AGENTS.md 明令「图标是生成物，别手改 PNG」，而主仓的生成器**根本没有输出到它那儿去的路径**——那套图标是手画的，与母图不是同一份母图。现在是了。
- 生成出的 `icon.icns` 用 `iconutil -c iconset` 反解回 10 个槽位验过（macOS 原生可读；踩过一个坑：ICNS 的 chunk 长度字段**含 8 字节 chunk 头**，写成不含就只有一个槽位解得出来）。

### 显示层（主仓）

`wails.json` 的 `name` / `productName`（`outputfilename` 不动）、`web/index.html`、locales、Homebrew cask 的 `name`、安装与构建脚本、ISSUE_TEMPLATE、README（中英）、文档站、VS Code 插件 `displayName` 与命令标题、JetBrains `plugin.xml` 的 `<name>`（`<id>` 不动）。

**DMG 窗口那块画布也是当前面**：`build/dmg-background.svg` 上原来写着旧产品名 + 一句早就不对的副标题（「Git 代码提交统计面板」——那是三代以前的定位），改成新桌面名 + 「AI 编码 Agent 的本地记忆层」。

**删了 `build/dmg-readme.txt`**：无人引用的死文件，内容是更老的一代品牌名。

### 显示层（独立仓）

`tauri.conf.json` 的 `productName` 与窗口标题、中英 README、`AGENTS.md`、`docs/product.md` 中英双份、界面文案，**`identifier` 不动**。

顺手修掉的**真缺陷**（不是风格问题，是改名带出来的）：

- **9 处缺空格**：批量替换把「拉丁词 + 空格 + 汉字」里的空格吃掉了（独立仓 8 处、主仓 1 处）——「Cinder Desktop是两个产品」这种。
- **1 处 ASCII 架构图歪了**：`docs/roadmap.md` 里 `Repotory` → `Cinder` 短了 2 列，箭头那一列跟着错位 → 补 2 个空格，列宽复原。
- **5 处断锚点**：**改标题就是改锚点**。改名后 4 处链接指向空处（另外 1 处「ADR-0015 人类闸门」少了一个「的」，是更早的手写笔误），一并修好。
- **1 处语义自相矛盾**：改完后有 4 处写成「Cinder 是**工作台**」，与 README 自己的角色表冲突（表里 Cinder 是记忆层）→ 改回「Cinder Desktop 是工作台」。

### 守门（两仓各一道）

| | 主仓 | 独立仓 |
|---|---|---|
| 真相源 | `build/brand.json` | `brand.json` |
| 脚本 | `scripts/check-brand.mjs` | `scripts/check-brand.mjs` |
| 入口 | CI `brand` job | CI `brand` job（第四道门）+ `npm run check:brand` |
| 查什么 | 产品名 10 处 = brand.json；冻结标识层 12 条逐条仍在原处；当前面不留旧品牌词（历史面按前缀放行）；App 里的 `BrandMark` 与母图同源（路径 `d`、组变换、hairline 宽度、inset、两种颜色） | 产品名 13 处 = brand.json（含 `productName`、窗口标题、`<title>`、首屏名）；冻结标识层 6 条；当前面不留旧品牌词；**文档锚点还指得到标题** |

**负例已验**（门不是只会绿）：主仓四道——产品名回退旧名、数据目录被改、README 漏出旧名、`BrandMark` 路径偏一位——每道都红；独立仓五道——`productName` 回退、`identifier` 被「顺手清理」、当前面漏旧名、锚点里的旧品牌词、桌面名不再与母品牌同源——每道都红。恢复后两仓复跑皆绿。

**两仓各有一份 `brand.json`**：CI 各跑各的仓，谁也看不到对方，所以**改词要同批改**。这是这份真相源唯一的结构性弱点，备忘在此。

### 旧名残留清单

- **故意保留（标识层）**：二进制名、数据目录、库文件名、MCP server 名、工具前缀、Go module、VS Code / JetBrains 插件 id、npm 包名、cask token、skill 名、Windows 安装目录、GitHub slug。
- **历史面按惯例保留**：历史 ADR / CHANGELOG / 评审记录 / `TODO.md` 的历史条目。
- **已修**：DMG 窗口画布、独立仓首屏与菜单提示（「点菜单栏的 R 唤回窗口」→「C」）。
- **已删**：`build/dmg-readme.txt`。
- **待办**：独立仓 `outputs/screenshots/` 的 9 张截图是**改名前**拍的（首屏写着旧名）——已加 `README.md` 说明它是历史证据，**未重拍**（重拍要跑起 Tauri 窗口 + 真 MCP server + 真 provider）。
- **与改名无关、更早就断的**：主仓 5 处文档内链（`docs/data-management.md` 等），本次未修。

### 未改的（要单独批准）

- **GitHub 仓 slug**：远端 `sky-jiangcheng/repotory` 与 `sky-jiangcheng/repotory-agent` **保持原样**。改 slug 会让每个老 clone 要手动改一次 remote，属于共享状态，要单独决定；**这一条要写进发布说明**。

### 验证

- 主仓：`node scripts/check-brand.mjs` 绿；`node scripts/generate-icons.mjs --check --tracked-only` 绿（母图与全部已提交派生件同源）。
- 独立仓：`npm run check:brand` 绿；`npm run check:docs` 绿；`npm run build`（`tsc` + `vite build`）绿；`cargo fmt --check`、`cargo clippy --all-targets --features test-fixtures -- -D warnings` 绿；`cargo test --all-targets --features test-fixtures` **80 passed / 0 failed**。

### 仍然未决（按原样留着）

- **UI 减法与主次重构**：元数据压内容、常驻右栏 288px、悬浮机器人压正文——已诊断，**独立的一篇 ADR**，与命名无关。
- **母图是否再画**：本轮只换了字母，hairline 三域规则与「印出来的卡」构造保持。

