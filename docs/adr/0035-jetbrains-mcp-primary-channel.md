# ADR-0035: JetBrains 插件的通道分层——MCP 主通道 + HTTP 补缺

状态：**Accepted（2026-10-30）**——两轮拷问后由项目所有者拍定：① 目标通道 = MCP 主通道 + HTTP 补缺；② 深链 = 尽力而为，`repotory-server` 保持可选
日期：2026-10-30
关联：[ADR-0009](0009-ide-presence.md)（本 ADR **修订**其决策 1 的措辞）、[ADR-0027](0027-jetbrains-plugin.md)（本 ADR **修订**其决策 1 与决策 4 的定位）、[ADR-0007](0007-session-memory-protocol.md)（MCP 协议与工具面）、[ADR-0017](0017-one-kernel-two-entry-points.md)（一核两入口）、[ADR-0032](0032-approved-wiki-page-visibility.md)（MCP 孪生的先例）、VS Code 扩展（`ide/vscode`，同构参照）
起始 schema：v28（**本 ADR 不改 schema**）

## 背景

本 ADR 回答一个具体的可用度问题：**为什么 JetBrains 插件做不到 VS Code 扩展那样「装上就能用」**。答案不是某一个 bug，而是三个各自成立、叠在一起的事实。

### 事实一：JetBrains 插件把「服务在不在」变成了「插件能不能用」

`ide/jetbrains/` 落地 7 个 Kotlin 源文件，其中**所有**数据访问都经 `RepotoryClient.rpc()` 打 `POST {serverUrl}/api/rpc`（[RepotoryClient.kt:302-320](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/RepotoryClient.kt#L302-L320)）。插件用的绑定是：`SearchAll`（notes + todos）、`SearchWikiPages`、`GetNote`、`GetWikiPage`、`ListProjects`、`OrganizeNote`、`UpdateNoteFull`、`CreateNoteWithMeta`、`MoveNote`。

于是**检索、读笔记、写笔记、改归属项目全部要求 `127.0.0.1:18765` 上有东西应答**。服务不在时，工具窗里连一次搜索都发不出去（`RepotoryClient.search` 的 `offline = true` 分支，[RepotoryClient.kt:246-254](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/RepotoryClient.kt#L246-L254)）。

**而装桌面应用救不了这个**：桌面端是 Wails 进程，不监听任何端口（ADR-0001 决策 M1），`repotory-server` 才是那个监听 18765 的东西；`scripts/install.sh` 只装 `repotory` + `repotory-mcp`，**不装 `repotory-server`**。

### 事实二：VS Code 扩展早就是「MCP 主 + HTTP 补缺」——它才是同构的参照

核 VS Code 侧的真实形态（不是它的文档描述，是它的调用点）：

| 能力 | VS Code 扩展走哪条 | 备注 |
|---|---|---|
| 检索、笔记读/写/建、组织、wiki 读写、projects 列表 | **MCP stdio**（`repotory-mcp`） | `callTool(...)` 共 21 处调用点（[extension.ts:346-670](file:///workspace/ide/vscode/src/extension.ts#L346-L670)） |
| `MoveNote`（改笔记所属项目） | **HTTP** `/api/rpc` | `moveNoteViaRpc`（[extension.ts:700-708](file:///workspace/ide/vscode/src/extension.ts#L700-L708)） |
| `ResolveWorkspacePath`（工作区解析） | **HTTP** `/api/rpc` | `refreshScope`（[extension.ts:267-270](file:///workspace/ide/vscode/src/extension.ts#L267-L270)） |
| 深链（跳浏览器 web UI） | **HTTP** | `openHit`（[extension.ts:527-537](file:///workspace/ide/vscode/src/extension.ts#L527-L537)） |

VS Code 侧 `MoveNote` 走 HTTP 的理由写在代码注释里：**「re-filing knowledge is a human decision an agent must not make」**（[extension.ts:695-698](file:///workspace/ide/vscode/src/extension.ts#L695-L698)）——这条能力**故意**不进 MCP。而且它在服务不可用时**降级**而非中断：「Fields saved, but moving the note failed」（[extension.ts:660-665](file:///workspace/ide/vscode/src/extension.ts#L660-L665)）。

**结论：VS Code 侧真正的形态就是「MCP 主通道 + HTTP 补缺」**。JetBrains 侧的问题不是选错了传输层，是**把 VS Code 里两个可以降级的 HTTP 依赖点，变成了唯一的路径**。

### 事实三：「MCP 是唯一执行接口」这个措辞漂移了

仓库的规范定义是「MCP 是唯一的 **AI** 执行接口」（[docs/features/ai-integration.md:117](file:///workspace/docs/features/ai-integration.md#L117)、[docs/product/2026-08-24-scope-reduction.md:274](file:///workspace/docs/product/2026-08-24-scope-reduction.md#L274)）——**限定词是「AI」**，指 agent 侧。

但 [ADR-0009 决策 1](file:///workspace/docs/adr/0009-ide-presence.md#L20) 写成「MCP 保持唯一执行接口」，同一句里又说「一切新 IDE 资产……经 headless HTTP **或** MCP stdio 调共享服务层」；[ADR-0027 决策 1](file:///workspace/docs/adr/0027-jetbrains-plugin.md#L21) 更直接：既说「插件……经 headless HTTP 调共享服务层」，又说「不新增执行接口（MCP 保持唯一执行接口）」——**同一句话自相矛盾**。

读法有两种：

- **窄读**：「唯一执行接口」指 agent 侧的**执行**通道。人侧界面调的是**绑定面**，不是执行接口。于是 JetBrains 走 HTTP 不违规。ADR-0027 的作者显然持这一读法。
- **宽读**：凡是外部触达 Repotory 能力的通道都算执行接口，MCP 是唯一的。那 ADR-0027 决策 1 自身就违规了。

**本 ADR 取窄读并把限定词钉死**——否则后面每一条取舍都没有共同语言。注意 ADR-0009 决策 1 的字面**已经授权**了两种通道，所以「给 JetBrains 加 MCP 通道」不是新增接口，是回到已授权的形态。

### 事实四：深链硬依赖 `repotory-server`，而它从来没有过任何分发渠道

插件「打开选中项」跳浏览器 SPA，而 web UI 只在 `repotory-server --web-dist` 里。server 没带 `web/` 时链接落 404，插件只能弹窗警告「Repotory is running API-only — this link would land on a 404」（[RepotoryToolWindow.kt:1060-1066](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/RepotoryToolWindow.kt#L1060-L1066)）。

而 `repotory-server` 的资产虽然发在 release 里（`repotory-server-<target>.tar.gz`，含二进制 + `web/`），**没有任何安装渠道**：install.sh 不装它，Homebrew / Scoop 只有 `repotory` 与 `repotory-mcp`（[packaging/README.md:8-18](file:///workspace/packaging/README.md#L8-L18)）。

于是出现一个闭环缺口：`packaging/README.md` 给 `repotory-server` 登记的消费者一栏写的就是 **"JetBrains plugin one-click start"**——它唯一的分发路径是插件自己，而 ADR-0027 决策 4 的边界 ① 明文禁止插件下载二进制。

**VS Code 侧从来没有这个缺口，因为它从来没有承诺过深链可用**——它的 `openHit` 直接 `openExternal` 一个打不通的 URL，没有预检。

### 事实五：MCP 工具面与 HTTP 绑定面同源，但有两处真实差异

MCP 工具与 HTTP 绑定打的是**同一个 service 层**，所以「换传输」多数时候不是「换能力」：

| JetBrains 现有绑定 | MCP 孪生 | 同源？ |
|---|---|---|
| `SearchAll`（notes + todos） | `repotory_notes_search` | ✅ |
| `SearchWikiPages`（scope 0，跨项目） | `repotory_wiki_search`（省略 `project_id` / `path` 即跨项目） | ✅ |
| `GetNote` | `repotory_notes_read` | ✅ |
| `GetWikiPage` | `repotory_wiki_read` | ✅ **同一个 `service.WikiPageDetail`**，`out_links` / `in_links` / `source_note_ids` 全在（[wiki_browse.go:61-64](file:///workspace/internal/service/wiki_browse.go#L61-L64)、[tools_wiki.go:112](file:///workspace/cmd/mcp/tools_wiki.go#L112)）——插件详情面板的「链接 / 反向链接 / 来源笔记」不会丢 |
| `ListProjects` | `repotory_projects_list` | ✅ |
| `OrganizeNote` | `repotory_notes_organize` | ✅ |
| `UpdateNoteFull`（含 `pinned`） | `repotory_notes_update`（**无 `pinned`**） | ⚠️ **差异一** |
| `CreateNoteWithMeta` | `repotory_notes_create`（`project_id` / `title` / `content` / `category` / `tags`） | ✅ 参数同构 |
| `MoveNote` | **无孪生**（`cmd/mcp` 零命中） | ⚠️ **差异二**（VS Code 同样走 HTTP，理由见事实二） |

**差异一（`pinned`）**：`db.UpdateNoteFull(db, noteID, content, title, tags, kind, pinned)` 有 `pinned`，MCP 的 `repotory_notes_update` 没有。插件的笔记编辑器确实在写它（`pinned = draft.pinned`，[NoteEditorDialog.kt:67](file:///workspace/ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/NoteEditorDialog.kt#L67)）。**不补就是功能回退。**

**差异二（handoff 不可编辑）**：`repotory_notes_update` 对 `handoff` 标签的笔记按 append-only 处理（会话记忆协议的退出记录不许被改写，[main.go:297-302](file:///workspace/cmd/mcp/main.go#L297-L302)）；HTTP 的 `UpdateNoteFull` 没有这条 guard。所以迁到 MCP 后，**IDE 里将不能编辑 handoff 笔记**——而 VS Code 现在也不能。这是收窄，且是朝正确方向的收窄。

## 决策

### 决策 1：术语钉死——「执行接口」指 AI 侧；人侧界面走绑定面

**「MCP 是唯一执行接口」一律读作「MCP 是唯一的 AI 执行接口」。** 人侧界面（桌面端 / 两个 IDE 插件 / 浏览器）调的是**绑定面**，两种通道都合法。

本 ADR 据此**修订** [ADR-0009 决策 1](file:///workspace/docs/adr/0009-ide-presence.md#L20) 与 [ADR-0027 决策 1](file:///workspace/docs/adr/0027-jetbrains-plugin.md#L21) 的措辞：补上「AI」限定词。ADR-0009 决策 1 的实质授权（「经 headless HTTP **或** MCP stdio 调共享服务层」）不变。

### 决策 2：JetBrains 插件改走 **MCP 主通道 + HTTP 补缺**，与 VS Code 扩展同构

能力路由表——**八走 MCP，两留 HTTP**：

| 能力 | 通道 | 具体 |
|---|---|---|
| 检索（notes + todos） | MCP | `repotory_notes_search` |
| 检索（已批准 wiki 页） | MCP | `repotory_wiki_search`（省略 `project_id` / `path`，保持插件现有的跨项目口径） |
| 读笔记 | MCP | `repotory_notes_read` |
| 读 wiki 页 | MCP | `repotory_wiki_read` |
| 项目列表 | MCP | `repotory_projects_list` |
| 建笔记 | MCP | `repotory_notes_create` |
| 改笔记（内容 / 标题 / 标签 / 类别 / 置顶） | MCP | `repotory_notes_update`（置顶参数见决策 3） |
| 知识库整理 | MCP | `repotory_notes_organize` |
| **改笔记所属项目** | **HTTP** | `MoveNote`——human-only 决策，**不进 MCP**（理由同 VS Code 侧） |
| **服务存在性 / web UI 探测、深链** | **HTTP** | 裸 HTTP 路径，非绑定 |

### 决策 3：给 `repotory_notes_update` 加可选 `pinned` 参数

否则迁移就是**功能回退**（插件现在能写置顶）。

实现必须遵守该工具既有的 **partial-update 纪律**：省略 = 保持原值。`db.UpdateNoteMeta` 会 wholesale 替换 `title` / `tags` / `kind` / `pinned`，所以工具侧必须先读笔记再合并（现有实现已是这个形状，[main.go:287-295](file:///workspace/cmd/mcp/main.go#L287-L295)——那段注释记着一次真实事故：只传 `category` 曾把标题、标签清空并把笔记 unpin）。**`pinned` 必须作为可选布尔参数加入这个合并逻辑，不得因省略而取消置顶。**

这是对 agent 面的**增量**（既有工具加一个可选参数，不新增工具、不改既有语义）。

### 决策 4：接受 handoff 笔记在两个 IDE 里都不可编辑

迁到 MCP 后 IDE 不能编辑 `handoff` 笔记。**这是有意的收窄**，且与 VS Code 现状一致：handoff 是会话记忆协议的退出记录，改正它要走 `repotory_handoff` 写新记录。插件在遇到这类笔记时给出指向该流程的提示，不得静默失败。

### 决策 5：降级语义——服务不可用时，插件**大部分可用**，且要说清哪一条不可用

| 能力 | 无 `repotory-server` 时 |
|---|---|
| 检索 / 读 / 建 / 改 / 整理 / 项目列表 | ✅ **照常可用**（走 MCP） |
| 改笔记所属项目 | ⚠️ 不可用——**保存成功但归属未变**，提示须写明这一点（对齐 VS Code 的「Fields saved, but moving the note failed」） |
| 深链 | ⚠️ 不可用——明确提示「需要本地服务」 |

**「保存成功但归属未变」不得写成「保存失败」**：内容确实已经落库，把两者混为一谈会让人以为要重写一遍。

### 决策 6：深链降为**尽力而为**；不为此新建 `repotory-server` 分发渠道

深链是**增强**，不是一等能力。两个插件对它的姿态统一：

- JetBrains 侧保持现状（探测 `servesWebUi`，没有就提示 + 提供获取路径）；
- **VS Code 侧补同样的预检**——现在它是直接 `openExternal` 一个可能打不通的 URL，没有解释（这是本 ADR 顺带修掉的一个体验缺口）。

`repotory-server` **继续保持「用户自行获取的可选增强」**，本 ADR **不新建** install.sh / Homebrew / Scoop 渠道。理由：VS Code 扩展从来没有 `repotory-server` 也照样发布了——深链不是任何一个客户端的可用性前提，把它变成前提是 JetBrains 侧独有的过度承诺。

**连带修正**：[packaging/README.md:16-18](file:///workspace/packaging/README.md#L16-L18) 给 `repotory-server` 登记的消费者一栏写着 "JetBrains plugin one-click start"，与本决策不符（插件不下载、也无渠道可指），应改为「手动获取（深链增强）」。

### 决策 7：`repotory-mcp` 二进制的查找顺序对齐 VS Code 侧

插件按 **设置项 → `REPOTORY_MCP_BIN` 环境变量 → `PATH` → 常见安装目录**（含 Homebrew / Scoop / `~/.local/bin`）解析，解析不到时在设置页给「选择可执行文件」入口，并提供与 VS Code 侧同形的诊断（列出尝试过的路径与失败原因）。

**插件不下载二进制**（沿用 ADR-0027 决策 4 的边界 ①，只是不再适用于 `repotory-server`）。

### 决策 8：不动 schema，不新增绑定，不新增 MCP 工具

本 ADR 只换传输层 + 给既有工具加一个可选参数。**不新增 `internal/app` 绑定**（因此不需要改 `docs/api/reference.md` 的绑定清单），**不新增 MCP 工具**（因此 `TestToolsRegistered` 钉死的工具数不变）。

## 后果

- **正面**：JetBrains 插件达到与 VS Code 扩展同等的可用度——装了 `repotory-mcp`（install.sh / Homebrew / Scoop **三条渠道都有**）就能检索、读写、编辑、建笔记；服务不再是使用前提。两个 IDE 分发面从此同构（同一套 MCP 工具面、同一套降级语义）。「装桌面 App 但插件不可用」这个现象消失。
- **负面**：插件要新增一个 Kotlin 版 MCP stdio 客户端（`initialize` 握手 + `tools/call` + JSON 结果解析）。**注意 MCP 侧多数工具走 `makeJSONResult` 返回结构化 JSON**（[results.go:20](file:///workspace/cmd/mcp/results.go#L20)），比 VS Code 侧解析 `repotory_notes_search` 文本输出的做法干净——但 `repotory_notes_search` / `repotory_wiki_search` 返回的是文本，需要各写一个解析器。
- **负面（agent 面）**：`repotory_notes_update` 加 `pinned` 参数 = 动了 agent 可见的工具面。需同步 `docs/api/reference.md`（中英双份）与 `docs/features/ai-integration.md`（工具描述）。属**增量**，不改既有语义。
- **负面（行为收窄）**：handoff 笔记在 IDE 里不可编辑（决策 4，与 VS Code 现状一致）；改笔记所属项目在无服务时不可用（决策 5，且已保存的内容不丢）。
- **中性**：桌面端仍不监听任何端口；`repotory-server` 仍是独立二进制、仍随 release 发布、仍**无分发渠道**。
- **遗留**：① `repotory-server` 的分发渠道（是否要装、装到哪）——记为独立待办，本 ADR 不决；② `MoveNote` 是否该进 MCP——**本 ADR 明确否**（human-only），若将来产品口径变了须另立 ADR；③ VS Code 侧补 `servesWebUi` 预检属本 ADR 决策 6 的连带项，与插件改造同批落地。

## 落地清单（实施要点，非实施计划）

1. `ide/jetbrains/`：新增 MCP stdio 客户端；`RepotoryClient` 的八项数据调用改走 MCP；`MoveNote` 与 web UI 探测保留 HTTP；降级提示按决策 5 的措辞。
2. `cmd/mcp`：`repotory_notes_update` 加可选 `pinned`（partial-update 合并）；同步 `docs/api/reference.md` 中英双份 + `docs/features/ai-integration.md`。
3. `ide/vscode/`：`openHit` 补 `servesWebUi` 预检。
4. `packaging/README.md`：`repotory-server` 消费者一栏改为「手动获取（深链增强）」。
5. `docs/adr/0009`、`docs/adr/0027`：补「AI」限定词（本决策 1）。

## 落地时的更正（2026-11-01，实施记录）

本 ADR 的**决策一条未改**；下面两处是实施时核对代码发现的**事实偏差**，按本仓「加注不改写」的惯例记在这里——照着原文去实施会写出错的代码。

**更正一：两个搜索工具返回的是 JSON，不是文本。**

「后果」一节写「`repotory_notes_search` / `repotory_wiki_search` 返回的是文本，需要各写一个解析器」。逐个核对 `cmd/mcp` 后确认**不成立**：两者都走 `makeJSONResult`，返回结构化 JSON（[main.go:135](file:///workspace/cmd/mcp/main.go#L135)、[tools_wiki.go:87](file:///workspace/cmd/mcp/tools_wiki.go#L87)）。**文本只出现在错误路径**（`makeTextResult`：`query is required` / `error: …` / `note not found: …` / `refusing to update note N: …`）。

所以实现上**没有「两个文本解析器」，而是「一个 JSON 解析器 + 一条错误分支」**：解析不成 JSON 的文本 = 工具在拒绝，必须**报错**，绝不能当成空结果。空结果是用户唯一分不清「没匹配」和「调用失败」的答案——比报错更糟。落地时这条被钉成了 8 个工具映射各自的回归用例（`ide/jetbrains/src/test/kotlin/dev/repotory/jetbrains/McpPayloadsTest.kt`）。

**更正二：要同步的是 MCP 工具目录，不是 `docs/api/reference.md`。**

「落地清单」第 2 条与「后果」一节都写了「需同步 `docs/api/reference.md`（中英双份）」。核对后：`docs/api/reference.md` 记的是**绑定层**（`internal/app` 的绑定清单），其中 `UpdateNoteMeta(noteID, title, tags, kind, pinned)` **早已含 `pinned`**；MCP 工具面根本不在该文件里，而本 ADR 决策 8 又明确「不新增绑定」。所以该文件**本次无需改动**。真正要改的是：

- `docs/features/ai-integration.md` 的 MCP 工具目录（中英双份）——工具参数在这里；
- 随包分发的 skill 工具签名文档：`SKILL.md`、`skill/repotory/references/mcp-tools.md`、`skill/repotory/SKILL.md`（该文件自称签名「取自 `tools/list` 实际响应」，加参数后必须同步，否则就是漂移）。

**落地记录（2026-11-01）**：① `cmd/mcp` 的 `repotory_notes_update` 加了可选布尔 `pinned`（省略 = 保持原值；`false` 与省略是两回事，按参数**是否存在**判定）；② `ide/jetbrains/` 新增 `McpStdioClient.kt`（协议层）、`McpPayloads.kt`（解析层）、`McpBinaryResolver.kt`（二进制查找）、`RepotoryMcp.kt`（8 项能力门面），八项数据调用改走 MCP，`MoveNote` 与 web UI 探测留在 HTTP；③ 设置页新增「MCP binary」栏与「Check MCP channel」诊断；④ VS Code 侧 `openHit` 补上 `servesWebUi` 预检；⑤ 插件新增 27 条 JVM 单测并进了 CI（`gradle test`）。
