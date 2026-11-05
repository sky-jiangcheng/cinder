# ADR-0047: 桌面 App 回归「关窗口即退出」，独立项目做 copilot——两个产品各担一件事

- 状态：Accepted（2026-11-04，用户在「另起项目」的三个选项中先选「本仓加 `cmd/`」，同日复盘后改为「另立独立仓做独立 copilot」，并把 ADR-0045 标记 Superseded（部分））
- 日期：2026-11-04
- 取代：[ADR-0045](0045-resident-desktop-bot.md)（桌面机器人常驻——关窗口不退出、macOS 用菜单栏当状态栏）**的决策①与决策②**（常驻与状态栏部分）
- 保留自 ADR-0045：决策③（机器人自己切模块）、决策④（作用域默认全库且可切）、决策⑤（「独立」的语义）、决策⑥（与两个 IDE 插件能力对齐）、决策⑦（不动 schema / 不新增 MCP 工具）——**但适用范围收窄为「工作台内的小机器人」**，见决策②
- 关联：[ADR-0046](0046-macos-reopen-handler.md)（macOS reopen 通道——本篇让它的**实现失去唯一消费者**）、[ADR-0035](0035-jetbrains-mcp-primary-channel.md)（MCP 主通道 + HTTP 补缺）、[ADR-0015](0015-w3-minimal-compile-loop.md)（批准是人的行为）、[ADR-0041](0041-draggable-agent-bot-and-shared-dashboard-scope.md)（可拖拽机器人 + 作用域共享单一真值）

## 背景

### 起因：一次「应用打不开界面」把常驻的代价照出来了

2026-11-04 用户报告「本地最新的 Repotory 应用打不开界面」。排查结论（ADR-0046 背景已详述）：
macOS 上「点 Dock 图标唤回」这条路径**无人实现**——Wails v2 的 `AppDelegate` 没有
`applicationShouldHandleReopen:hasVisibleWindows:`，而 Wails 的隐藏走 `[mainWindow orderOut:nil]`
（窗口被摘出屏幕列表），AppKit 默认的 unhide 唤不回它。

### 根因不在实现，在 ADR-0045 决策① 的归因错误

ADR-0045 决策① 的表格写着：

> `SingleInstanceLock` | 再次启动时唤起已有实例（显示窗口 + 打开机器人）

这句话在 macOS 上**只对了一半**。核实源码（`internal/frontend/desktop/darwin/single_instance.go`）：

- `SingleInstanceLock` 的交接走 **`NSDistributedNotificationCenter`**，只在「真的启动了第二个进程」时发生；
- 而 macOS 上用户点 Dock 图标时，LaunchServices **派发 reopen 事件给已有实例**，根本不启动第二个进程，
  所以 `SingleInstanceLock` **一次都不会被触发**。

**后果**：ADR-0045 决策① 认定「唤回能力已被覆盖」，实际只覆盖了三条唤回路径中的一条。
另外两条（菜单栏「显示 Repotory」、再次启动的单实例交接）是通的，唯独最常用的那条（点 Dock 图标）断了。
**ADR-0045 全文没有出现过一次「Dock」「reopen」「点击图标」——它压根没考虑过这条路径。**

### 更深一层：常驻的收益从未被兑现

ADR-0045 的背景写着用户的原始诉求：

> 「桌面 App 的机器人应该可以独立于桌面 App 使用」
> 「在桌面 App 叉掉应用可以常驻状态栏并仍然显示机器人」

**注意后半句：「仍然显示机器人」。** 但决策⑤ 自己承认了做不到：

> 「机器人面板本身是 webview 内的 DOM，它永远需要宿主进程活着」
> 「想要『关掉 App 还有个独立小窗在』，那是第二条原生窗口，Wails v2 的单窗口模型给不了，本篇不做」

核实确认这句断言是对的：`options.App` 无 `Windows []*WebviewWindow` 字段，`pkg/runtime` 无
`NewWebviewWindow`，全模块 grep `WebviewWindow` **零命中**。

**于是形成了一个空转的组合**：叉掉窗口 → 窗口被 `orderOut:` 摘掉 → **机器人（webview 内的 DOM）
跟着一起不可见** → 进程还活着占着单实例锁 → 用户点 Dock 图标 → **无人接收 reopen** →
用户看到「应用打不开」。

**常驻买到的是「进程不退出」，付出的是「窗口不可见时机器人也不可见 + 一个僵尸般的进程态」。
用户要的是「机器人仍然显示」，两者的缺口正是本次故障的体感来源。**

### 复盘时又查实两件事，它们各自否掉了一个原方案

**第一件：Wails v2 给不了托盘，而托盘正是「常驻」唯一兑现得出来的形态。**

ADR-0045 决策② 的方案是「macOS 菜单栏就是状态栏」，本篇初稿仍打算沿用
「agent 进程 = 本仓 `cmd/repotory-agent` = 另一个 Wails App」。核实结果是这条路走不通：

- `options.App` **无 Tray 字段**；`pkg/menu` 里的 `TrayMenu` 是**死类型**
  （`internal/menumanager` 存着它，没有任何平台前端渲染它，也没有公开 API 能到达它）；
- 外部 `energye/systray` 的 darwin 实现要 cgo，而 **CI 的 `go build ./...` 跑在裸 ubuntu 上**，
  没有 GTK/WebKit 头文件，systray 的 linux 实现同样要 cgo——import 进 `main.go` 会让那道门**编译失败**。

所以「同一个 Wails 栈里再做一个常驻进程」的结果是：**它能常驻，但唤回只能靠「再点一次 Dock 图标」，
而那条路刚刚被证明是断的。** 常驻再次变成一个兑现不了的收益。

**而 Tauri v2 自带托盘**：`tauri-2.12.5/src/tray/` 是内置模块（需开 `tray-icon` feature），
菜单栏图标、点击唤起、后台常驻全是现成 API。**这是 Tauri 相对 Wails 的硬优势，
也正好是 ADR-0045 当初想用「菜单栏当状态栏」却做不出来的那个东西。**

**第二件：MCP 工具面本来就没有 approve/reject，「工作台管审批、copilot 管读聊执行」不是妥协。**

核对 `cmd/mcp/tools_wiki.go`：MCP 面只注册了 `repotory_wiki_search` / `repotory_wiki_read`
**两个纯读工具**，注释写明闸门在**写侧**——「把内容发布进检索是人的行为」
（ADR-0015 决策③）。MCP 面的写工具只有 `repotory_notes_create` / `_update` / `_organize`
与 `repotory_handoff`。

**所以「谁能审批」从来不是技术问题，是产品分工**：审批是**人在工作台里做的事**。
一个跑在用户机器上的 copilot 读记忆、聊项目、执行命令，**它本来就不该替人批准知识**。

### 顺带查实：多进程写同一个库不是新风险

ADR-0047 初稿把「两个进程同写 SQLite」写成新增代价。核实后发现**这是既有事实**：

`cmd/mcp/main.go:27` 里 `repotory-mcp` **自己开库**（`db.InitDB(platform.GetDbPath())`），
而两个 IDE 插件（VS Code 的 `ide/vscode/src/mcpClient.ts`、JetBrains 的
`ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/McpStdioClient.kt`）
**都是拉起 `repotory-mcp` 子进程走 stdio**。

**即「桌面 App 开着 + MCP server 开着」今天已经同时在跑同一个 SQLite。**
copilot 走 MCP 通道，**不新增写者**（它自己不碰库，写操作由 MCP server 代劳）。

## 决策

### ① 桌面 App 回归「关窗口即退出」，撤掉常驻三件套

`main.go` 移除：`OnBeforeClose` 拦截、`SingleInstanceLock`、自建的菜单项。
桌面 App 回到 v2.28 之前的语义：**叉掉窗口 = 结束进程**，再点 Dock 图标 = 全新进程全新窗口。

**理由**：常驻的收益（「进程不退出」）在本形态下**无法兑现为用户可见的价值**，
而它的代价（僵尸进程态 + 单实例锁竞争 + 唤回路径必须自己补）全部由用户承担。
**保留一个兑现不了的收益不如不留。**

**Edit 与 Window 两个角色菜单保留**——Wails 的 darwin 前端**总是**调 `SetAsApplicationMenu`，
菜单为空则 macOS webview 里没有 Cmd+C / Cmd+V。**第一项必须是 `menu.AppMenu()`**
（macOS 把第一项当应用菜单渲染，标题会换成 app 名），否则 Edit 整组被吞进应用菜单。

### ② 桌面 App 的小机器人保留，定位明确为「工作台的一部分」

那个 52px 可拖拽球**继续存在**（ADR-0041 的可拖拽机器人不受影响），它的定位是
**工作台的一个视图**：它问的是「这一页能回答什么」，答案来自这一页自己掌握的数据
（精确、即时、无需配 AI 端点），作用域与仪表盘**共享同一份真值**。

**它不再承诺「关掉 App 还能用」——它就是一个 App 内的浮层。**
两个 IDE 插件同理（它们的机器人本来就依附编辑器）。

**明确不承诺的两件事**：不做审批入口（审批是审核页的事，那是工作台的主面）；
不做常驻助手（那是 copilot 的事）。

### ③ 独立项目 `repotory-agent` 定位为**独立 copilot**，不复用工作台的身份

独立仓（原 `repotory-agent-shell`，本篇落地时改名）是一个**自己的产品**，
不是一个「脱离窗口的桌面机器人」。它与工作台的关系是**两个产品**，
不是主从、不是同一产品的两个前端。

**它是什么**：用户自己挑一个本地模型（Ollama 起，llama.cpp / MLX 走插件式后端），
对着当前项目聊天、读项目上下文、在白名单内执行命令，并从 Repotory 取回记忆。

**它不是什么**：

- **不是工作台的第二个前端**——它不共用 `web/` 的 React 前端，不复用 `AgentBot.tsx`；
- **不是审批者**——ADR-0015 的闸门在 MCP 面本来就在写侧，它走 MCP 就自动站在闸门外；
- **不是工作台的生命线**——工作台关掉，它照常跑；它关掉，工作台照常用。

**技术栈与形态**：Tauri v2 + Rust 后端 + TypeScript 前端。
**选 Tauri 的理由只有一条，但足够硬**：它自带托盘（决策⑤），
而 Wails v2 没有（第一件核实）。**Tauri 用的是系统 WebView，不是 Swift/AppKit 原生控件**——
它是「独立进程 + 真托盘 + 真分发渠道」，不是「原生 UI 框架」。

**「看用户喜好」的部分明确交给用户**：模型后端、是否常驻、界面形态都不由本仓决定。

### ④ 记忆通道走 MCP，不直连数据库

copilot 读 Repotory **只走 MCP 工具**（`repotory_context` / `repotory_notes_*` /
`repotory_projects_*` / `repotory_wiki_*` / `repotory_handoff` / `repotory_scan`），
**不直连 SQLite、不复制检索逻辑**。

**理由**：MCP 是 Repotory 对外公开的协议（ADR-0009 的薄客户端纪律、ADR-0035 的主通道），
两个 IDE 插件已经走同一条路。**走 MCP 意味着 copilot 与 IDE 插件是同一类客户端**，
能力对齐由协议保证；直连数据库则要重做一遍连接管理、迁移与锁竞争，且必然漂移。

**这一条同时免掉了 ADR-0015 的闸门冲突**：工作台（桌面 App）有 `internal/app` 的全部绑定，
审批、合并、作用域提升都在那里；copilot 走 MCP，工具面里**根本没有 approve/reject**，
它想越都越不过去。**分工不是靠约定维持的，是靠工具面划开的。**

**MCP server 自己开库**（`cmd/mcp/main.go:27`）——copilot 不新增写者，
「桌面 App + MCP server 同写一个 SQLite」是**今天已经在跑的状态**（两个 IDE 插件都如此）。

### ⑤ 唤回与常驻：交给 copilot 自己的托盘，桌面 App 不再需要 reopen 钩子

copilot 常驻时用 **Tauri 托盘图标**（`tray-icon` feature）做唤回入口——
这是 Tauri 的现成能力，也是当初 ADR-0045 想要而 Wails 给不了的那个东西。

**桌面 App 不再有「窗口被隐藏但进程还活着」的状态**，所以 ADR-0046 的 reopen 钩子
**失去了唯一消费者**：它解决的是「Wails 隐藏的窗口唤不回来」，而桌面 App 已经不隐藏了；
copilot 是 Tauri，不走 Wails 的 `orderOut:` 那条路。

**处置**：`internal/platform/darwinreopen/` **从仓里删除**，ADR-0046 标记 **Superseded**。
**理由**：仓里第一次有了 Objective-C 源码（`.m` + cgo + 依赖 Wails 内部类名 `AppDelegate`），
留着一段**已验证正确但没有消费者**的注入代码，成本是持续的（cgo 构建面、Wails 升级即失效的脆弱性），
而它解决的问题已经不存在。

**这段代码从未进过 git 历史**——它只活过 ADR-0046 那一轮的工作区，还没提交就被本篇删掉了。
所以**没有「从历史取回」这回事**（`git log -- internal/platform/darwinreopen/` 是空的）：
**留下来的只有 ADR-0046 的分析**。
若将来真有 Wails 侧常驻需求，那篇分析会省掉一次排查，但**代码要重新写一遍**——
这是「留代码」的真实成本（cgo 构建面 + Wails 升级即失效的脆弱性）与「留结论」的收益（省一次排查）之间的取舍，
**本篇选后者**。

**ADR-0046 的分析结论仍然全部成立**（Wails v2 没有 reopen 通道、`orderOut:` 唤不回来、
`@encode(BOOL)` 要按架构生成）——那部分是知识，不是代码。

### ⑥ ADR-0045 标记 Superseded（部分），但适用范围收窄

ADR-0045 的**决策①②**（常驻 + 菜单栏状态栏）被本篇取代；
**决策③～⑦继续有效，但只适用于「工作台内的小机器人」**——
即：模块 chip（`NAV_ENTRIES` 单一真值）、作用域默认全库且可切、「独立」= 不依赖当前页面、
与两个 IDE 插件能力对齐、不动 schema 与 MCP 工具面。

**copilot 不继承这五条**：它是独立产品，不是工作台的一个前端。
它有自己的模型后端与自己的界面，不需要模块 chip，也不需要工作台的作用域语义。

### ⑦ 不动 schema、不新增 MCP 工具面

两个方向都不新增 MCP 工具：copilot 是**消费**现有工具面，不是扩展它。
`internal/app` 的绑定不变，MCP 工具注册表不变（`TestToolsRegistered` 钉死的工具数不变），
schema 不变。

**唯一的例外是 copilot 侧的 MCP 客户端实现**（在另一个仓里，不在本仓）。

## 后果

- **正面**：
  - **桌面 App 回到可预期的形态**：叉掉 = 结束，与 v2.28 及之前一致。
    「应用打不开界面」这类故障**从根上消失**——不再有「进程活着但窗口没了」的僵尸态。
  - **工作台与 copilot 各归其位**：工作台管「浏览、编辑、审批、治理」，
    copilot 管「读、聊、执行」。**两者不重叠、不争抢、不需要谁给谁让路。**
  - **托盘这个硬需求有着落了**：copilot 用 Tauri 托盘，
    「关掉工作台还有一个常驻助手在」**从「做不到」变成「做得到」**。
  - **审批权留在人手里**：这是工具面划出来的，不是纪律维持的。
  - **ADR-0046 的分析没有浪费**：Wails v2 没有 reopen 通道这个结论，
    会在下一个想给 Wails 加常驻的人身上省掉一次排查。
- **代价（接受）**：
  - **两个产品要各自维护**：两套 UI、两套构建、两套发布。
    **它们不共用前端组件**——这是「独立产品」的代价，也是「独立」的兑现。
    （初稿曾打算让两者共用 `AgentBot.tsx` 来避免漂移；定位拆开后这条不成立，
    因为它们回答的根本不是同一批问题。）
  - **copilot 的能力是 MCP 工具面的子集**：它没有审批、没有作用域提升、
    没有笔记归口。它能做的比工作台少——**这是分工，不是缺陷。**
  - **版本耦合需要显式处理**：copilot 消费的 MCP 工具集随 Repotory 演进。
    **不做能力协商的话，「哪个版本配哪个版本」是一个隐式约束**，
    必须写进 copilot 的 README 与安装器（不能只在脑子里）。
  - **ADR-0046 的实现从仓里消失**：那段 ObjC 注入**从未进过 git 历史**
    （`git log -- internal/platform/darwinreopen/` 为空）——将来若真有 Wails 侧常驻需求，
    **要重新写一遍**；留下来的只有 ADR-0046 的分析。
  - **copilot 的托盘已落地，但「常驻」还没兑现成产品形态**：独立仓里托盘已经实现
    （`src-tauri/src/tray.rs`：左键唤回窗口，右键 Show / Quit 菜单）。
    **但托盘是「已经在跑的进程」的唤回入口，不是「窗口藏起来只留一个图标」那种常驻**——
    关窗口仍然结束应用（与决策① 同一口径）。**全局快捷键、窗口常驻可见都还没做**，
    唤醒交互的完整形态仍待使用者反馈（见「未决」）。
- **不能假装解决的**：
  - **「原生」这个词有歧义**：Tauri 用系统 WebView，不是 Swift/AppKit 原生控件。
    copilot 是「独立进程 + 真托盘 + 真分发渠道」，**不是原生 UI 框架**。
  - **copilot 能不能真的独立跑，取决于 Repotory 装没装**：
    它需要 `repotory-mcp` 二进制存在（这是它的记忆来源）。
    **不装 Repotory 就没有记忆**——这一点必须在首屏说清楚，不能假装。
  - **两个产品共用一个 SQLite**，写延迟是真实存在的（但如上所述，
    这是既有状态，不是新增的）。

## 未决（本篇明确不决）

- **copilot 的分发渠道**：单独下载？Homebrew cask？随工作台一起发？
- **copilot 的唤回交互**：托盘图标 / 全局快捷键 / 窗口常驻可见？
- **copilot 与工作台的版本兼容策略**：能力协商，还是语义化版本约束？
- **copilot 要不要执行写操作**：本篇定的是「走 MCP，只有一组写工具」，
  但「要不要让 copilot 写笔记」是产品决定，不是技术决定。

## 落地清单

| 面 | 文件 / 动作 |
|---|---|
| 桌面 App 撤常驻 | `main.go`（删 `OnBeforeClose` / `SingleInstanceLock` / 自建菜单项；**保留 AppMenu + EditMenu + WindowMenu**） |
| 删 reopen 实现 | `internal/platform/darwinreopen/` 整个目录删除（**注：它从未进过 git 历史，见决策⑤**） |
| ADR | 本篇（ADR-0047）+ ADR-0046 状态行改 Superseded + ADR-0045 状态行补适用范围收窄 + 两份索引 |
| 前端删死订阅 | `web/src/api/transport.ts` 的 `listenAskBot` **整个删掉**（不是改注释）——`app.ask-bot` 的唯一发送方是随决策① 一起撤掉的菜单项，**全仓已无发送方**；`web/src/components/AgentBot.tsx` 的订阅与 `openPanelRef` 一并删 |
| 独立仓改名 | GitHub repo `repotory-agent-shell` → `repotory-agent`；本地目录、Rust 包名与 lib 名、`tauri.conf.json` 的 `productName` / `identifier`、全部文档引用 |
| copilot 定位 | 独立仓的 `docs/product.md` / `docs/roadmap.md` / `README` / `AGENTS.md` 重写为「独立 copilot」 |
| copilot 门禁 | 独立仓补 `.github/workflows/`（docs mirror + 前端 build + `cargo check` / `clippy` / `test`） |
| copilot 图标 | 换掉 Tauri 默认图标，走主仓母图生成器（品牌三域：OS 应用域用 squircle） |
| MCP 工具副本 | 独立仓 `src-tauri/src/memory_layer.rs` 里那份硬编码工具清单**已漂移**（少列 7 个真实工具），改为动态拉取 |
| copilot 托盘 | 独立仓 `src-tauri/src/tray.rs`（**已落地**）：左键唤回窗口，右键 Show / Quit；`shell_status` 暴露 `tray.installed`，首屏有「唤回入口」卡片 |
| schema / MCP 工具面 | **不动** |

## 补注（2026-11-05）

**称谓统一**：对外产品名一律 **Repotory Agent**（独立仓 `repotory-agent`）；「copilot」只作**角色词**，
首次出现处消歧（**不是** GitHub Copilot）。**本篇正文里的「copilot」一律按此读**——它指独立仓那个产品。
本仓 README 与文档站首屏已各加一行双向关系句（工作台 vs 助手，审批在 Repotory 那边）。

**末尾「未决」四项的现状**（详见独立仓 `docs/product.md`）：

| 当时未决 | 现在的结论 |
|---|---|
| 版本兼容策略 | **已定**（2026-11-05）：**运行时自证 + 人话警告**——接住 `initialize` 响应里的 `serverInfo.version`（服务端本来就在报，客户端此前把它整个丢了），首屏显示「Repotory vX.Y.Z · N 工具」，低于下界时提示升级、**不硬拒绝**；**不做真能力协商**（它只解决「工具被删/改名」，不解决「工具新增」——而新增已经由动态拉取覆盖） |
| 要不要执行写操作 | **已定**（2026-11-05）：**分层**——`handoff` / `scan` 自动写；**长期知识（`notes_create` / `notes_update` / `notes_organize`）过一次人确认**（note 写完立刻进检索，agent 能往自己下一轮要读的记忆里写东西；ADR-0015 的原话正是这个形态） |
| 分发渠道 | **仍未决**——独立仓 Phase 6 |
| 唤回交互 | **仍未决**——全局快捷键 / 窗口常驻可见，等使用者反馈 |

**另定一条（记忆层不可用时的行为）**：**聊天继续 + 显式横幅说明**——上下文装配失败必须让用户知道，
**绝不静默降级**（静默等于让模型在没有依据的情况下回答，而用户看不出它这次没有依据）。

## 补注二（2026-11-05）：仓不拆，先立仓内边界

**问题**：本仓同时装着**工作台**（Wails 桌面 App）与**服务**（无头 HTTP server + MCP server）——
要不要把 server 之外的部分拆出去？

**结论：不拆。** 关键事实是——**「server」并不是这个仓的核心**：真正共享的是
`internal/service` + `internal/db`，server 只是通往它的**三扇门之一**（桌面 Wails、无头 HTTP、
MCP stdio）；**桌面与 MCP 都是直接开同一个 SQLite、直接调 service，并不经过 HTTP server**。
拆仓只会把「同一份业务逻辑的三扇门」变成跨仓发布协调。

**为什么拆不出好处**：**IDE 插件与 CLI 本来就是纯 HTTP 客户端**（`ide/vscode` 是 TypeScript、
`ide/jetbrains` 是 Kotlin、`scripts/repotory-init` 是 node），**跟 Go 代码零耦合**——它们
**已经拆出去了**，只是还住在同一个仓里。真正的耦合是 **Go module 级**的：一个 `go.mod`、
一个版本号（`wails.json` 是 SSOT）、一次 release 同时产出桌面 bundle + server bundle + MCP 二进制 +
VSIX + JetBrains 插件。拆仓之后 **schema 迁移要跨仓发布**（桌面必须等 core 发版才能吃到新迁移），
对单人维护是纯开销。

**先立边界——第一刀已落地（2026-11-05）**：`internal/app` 曾经 import Wails runtime，只为两个副作用
（`EventsEmit` 发事件、`BrowserOpenURL` 开浏览器），而 `cmd/server` 也链接 `internal/app`——
于是**整个 webview 前端栈（13 个包）被拖进了一个永远不开窗口的二进制**。现在这两个副作用改成**注入**
（`app.WithEmitter` / `app.WithOpener`），Wails 实现放回桌面入口 `main.go`，
`internal/app` 不再 import Wails。**行为逐字不变**：无 emitter 则事件丢弃（与旧的 nil-ctx 判断同结果），
无 opener 则 `OpenBrowserURL` 仍报同一句「browser open unavailable outside the desktop runtime」。
**守门**：`cmd/server/wails_free_test.go` 从 `cmd/server` 出发走**模块内 import 图**，断言整条依赖链
不含任何 `github.com/wailsapp/*`；并带一个**反向对照**（从仓根出发必须能找出桌面那份 Wails 导入），
防止这道门因为「什么都没走」而假绿。

**触发拆仓的条件只有两个**：① 有第三方需要**以 Go 库**的方式消费 core（而不是走 MCP / HTTP）；
② 桌面的发版节奏必须与 schema 迁移解耦。**「仓看起来太大」不是理由**——那是心情，不是约束。

## 补注三（2026-11-05）：那个「版本」是契约版本，不是发布版本

上面补注里写下的首屏文案「**Repotory vX.Y.Z · N 工具**」需要一处订正。
[ADR-0048](0048-core-contract-version.md) 把 MCP `initialize` 响应里的 `serverInfo.version`
定成了**记忆层的契约版本**（core version），不再是发布版本号。

于是客户端首屏该显示的是「**记忆层 vX.Y.Z · N 工具**」——而那个 X.Y.Z 回答的是
**「工具面变了没」**，不是「构建新不新」。发布版本号仍然拿得到（`/health` 的 `version` 键与启动日志），
但它对「我能依赖什么」这个问题**没有答案**：契约面很少动、构建面经常动，
一个 2.30.0 读不出工具面有没有变过。

**原文保留作历史。**

## 补注四（2026-11-05）：那个故障是旧版的，当前版实测三条 Dock 路径全通；并加一道守门

**起因**：用户报「之前的版本 Repotory 在 dock 栏无法退出也无法显示主窗口」。
这两个症状正是 ADR-0045 常驻版的两个特征，所以第一件事是查**装在机器上的到底是哪个版本**。

**查实**：`/Applications/Repotory.app` 是 **v2.29.0**（2026-11-04 发布），
二进制里留着 `com.repotory.desktop` 这个字符串——那是 ADR-0045 决策① 里
`SingleInstanceLock` 的 `UniqueId`。**所以用户手上跑的就是常驻版**。
而撤掉常驻的那次提交（`5f205b7`，本篇决策①）**是在 v2.30.0 才进的 release**：
`git merge-base --is-ancestor 5f205b7 v2.30.0` 为真，**v2.29.0 为假**。
**结论不是「代码没修」，是「装着的还是修之前的那一版」。**

**旧版为什么恰好是这两个症状**（机制已逐行核实，不是推测）：

- **「无法退出」**：`OnBeforeClose` 返回 `true` 时，Wails 的 `frontend.Quit()` 走的是
  `if !f.frontendOptions.OnBeforeClose(f.ctx) { f.mainWindow.Quit() }`——
  返回 true 就**永远不退出**。而 Dock 的「退出」与 Cmd+Q 走的是
  `applicationShouldTerminate:` → `processMessage("Q")` → 同一个 `frontend.Quit()`，
  **同一条拦截**。常驻版还自建了一个「Quit Repotory」菜单项，但它绕不开这道拦截吗？绕得开——
  它先置 `quitting` 再调 `runtime.Quit`，`OnBeforeClose` 见到 `quitting` 才放行。
  **也就是说旧版其实有得退，只是入口藏在一个自建菜单项里**；
  用户在 Dock 上右键「退出」走的是系统那条路，被拦了。
- **「无法显示主窗口」**：关闭时旧版调 `wailsruntime.WindowHide(ctx)`，
  Wails 的隐藏把窗口 `orderOut:`——**摘出屏幕列表**，不是「藏起来还在」。
  点 Dock 图标派发的是 reopen 事件，而 **Wails v2 没有 `applicationShouldHandleReopen`**
  （ADR-0046 的核实结论，至今成立）→ **无人接收**。

**当前版实测（三条路径全通，实测而非推演）**：用当前代码构建的桌面 App 打成 `.app` bundle，
以 `LSEnvironment` 隔离 HOME 跑起来，然后用**用户真实手势**（System Events 点 Dock 图标、
点 Dock 右键菜单项）逐条验：

| 手势 | 结果 |
|---|---|
| 点窗口左上角红按钮 | **进程退出**（`pgrep` 查不到） |
| Dock 右键菜单 →「退出」 | **进程退出** |
| Dock 菜单「隐藏」→ 再点 Dock 图标 | **主窗口回来**（同一区域截图前后对比：隐藏后是后面的窗口，点击后是 App 界面） |
| `osascript` 的 `quit` | 进程退出，但 AppleScript 侧报 **-128「用户已取消」**——那是 Wails 的 `applicationShouldTerminate:` 返回 `NSTerminateCancel` 的回声，**不是故障**（进程照样退出） |

**新增守门**：`main_test.go` 的 `TestDesktopAppMustNotBecomeResident`——
用 `go/parser` 解析 `main.go`（不是 grep 文本：注释掉的那行不该决定门红不红），
断言 `options.App` 上**不得出现** `OnBeforeClose` / `SingleInstanceLock`、
`mac.Options` 上**不得出现** `HideWindowOnClose`、
且**不得调用** `runtime.WindowHide` / `runtime.WindowMinimise`。
**负例已验**：三个字段分别塞回去，测试各红一次，恢复后绿。
另带一条**反向对照**：`options.App` 字面量找不到就红——
防止这道门因为「什么都没看」而假绿。

**为什么这道门值得加**：常驻那条路**看起来像个合理功能**（「用户去浏览器时机器人还留着」），
而它产生的后果（不能退、不能唤回）恰恰是这次用户报的那个故障。
**决策① 当时靠的是一次人工复盘，没有任何东西拦着它被「顺手加回来」。**

**明确不做**：**不把 ADR-0046 的 reopen 钩子加回来**。决策⑤ 的理由仍然成立——
桌面 App 不再有「窗口被隐藏但进程还活着」的状态，reopen 钩子**没有消费者**；
真要加，等于把一段「已验证正确但没有消费者」的 cgo + Objective-C 注入代码请回来。
**（真正需要常驻唤回的是独立仓那个 copilot，而它走 Tauri 托盘，不走 Wails 的 `orderOut:` 那条路。）**

**处置**：用户机器上的 `Repotory.app` 升级到 v2.31.1。
**数据目录一个字不动**（标识层冻结，`~/Library/Application Support/repotory` 原样保留），
**schema 不变、契约版本不变**。

## 后续路线

1. 独立仓改名 + 定位重写 + 门禁 + 图标（让 copilot 先成为一个「能构建、能自证」的仓）。
2. copilot 落 MCP 客户端（`initialize` + `tools/list` + `tools/call`），
   顺带把那份漂移的工具清单改成动态拉取。
3. 落 Tauri 托盘（**已落地**）+ 常驻，把「关掉工作台还有助手在」兑现出来。
   **剩下的常驻形态**（全局快捷键 / 窗口常驻可见）待反馈。
4. copilot 的形态与分发渠道由使用者反馈决定，不预设。
