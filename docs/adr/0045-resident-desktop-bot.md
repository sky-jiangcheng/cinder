# ADR-0045: 桌面机器人常驻——关掉窗口仍在、能自己切模块、不锁项目

- 状态：Accepted（2026-11-03，用户直接提出并拍定形态）
- 日期：2026-11-03
- 关联：[ADR-0041](0041-draggable-agent-bot-and-shared-dashboard-scope.md)（可拖拽机器人 + 仪表盘作用域共享单一真值）、[ADR-0038](0038-desktop-information-architecture.md)（顶级入口收敛为五个）、[ADR-0034](0034-desktop-three-column-shell.md)（三栏外壳与 macOS 隐藏标题栏）、[ADR-0044](0044-ide-panel-windsurf-shape.md)（IDE 面板的 Windsurf 形态、provider 配置与项目识别）

## 背景

用户提出三件事，指向同一个判断：**桌面端的机器人不应该是「App 窗口里的一个浮层」，
而应该是一个能离开窗口独立存在的常驻助手。**

原文拆开是三条：

1. 「桌面 App 的机器人应该可以独立于桌面 App 使用」
2. 「在桌面 App 叉掉应用可以常驻状态栏并仍然显示机器人」
3. 「机器人可以自身切换各个功能模块」
   外加一条对齐：「基本上两个 IDE 插件的功能跟桌面 App 的机器人是可以差不多的，
   只是桌面机器人不限制项目的范围」。

### 现状一：机器人是窗口的子节点，关窗即消失

`web/src/components/AgentBot.tsx` 里的机器人是**主窗口 webview 里的一个定位浮层**
（52×52 拖拽球 + 420px 面板，位置记在 `localStorage`）。它跟主窗口同生共死：
叉掉应用，机器人就没了。用户要的是**叉掉之后还能用**。

### 现状二：Wails v2 没有托盘，而托盘库会破坏本仓的构建约束

Wails v2.17 的 `options.App` **没有 Tray 字段**，`pkg/application` 也没有 systray；
`pkg/menu` 只做应用菜单（macOS 顶栏那一条），不是状态栏图标。

`pkg/menu` 里确实有一个 `TrayMenu` 类型，但它是**死类型**：`internal/menumanager`
存它，而**没有任何平台前端渲染它，也没有任何公开 API 能到达它**（darwin / windows /
linux 的 `MenuSetApplicationMenu` 只处理应用菜单）。所以「Wails v2 没有托盘」这条
结论成立，只是理由要写准——不是「没写」，是「写了但没人接」。
外部库 `github.com/energye/systray` 提供 `RunWithExternalLoop`（正是为「程序还要显示
webview」设计的），但它的 darwin 实现 `import "C"`。

这个冲突此前被记成「与零 CGO 发布资产冲突」，**但那条账算错了一半**：
`CGO_ENABLED=0` 只用于 `repotory-mcp` 与 `repotory-server` 两个纯 Go 二进制
（`scripts/build-release-assets.sh:52`、`release.yml:229,256`、`Dockerfile:43`）；
**桌面 App 本身是 `wails build` 出来的，macOS 上本来就带 CGO**（WebKit）。
所以 systray 与桌面 App 并不冲突。

真正的冲突在别处：**CI 的 `go build ./...` 跑在裸 ubuntu-latest 上，没有 GTK/WebKit 头文件**
（`ci.yml` 的注释写明「no GTK/WebKit system packages are installed here, and none are needed」）。
systray 的 linux 实现同样需要 cgo 与 X11/GTK 开发包，
把它 import 进 `main.go` 会让**这条门在 CI 上直接编译失败**。
零新依赖是这里的正确解——不是因为 CGO，而是因为**这条门不许破**。

### 现状三：机器人不能切模块，它只会问

机器人的上下文由 `web/src/components/agent/agentContext.ts` 按**当前路由**产出
（仪表盘给「今日提交量」卡片、知识库给「待整理」卡片……）。
也就是说**上下文跟着页面走，机器人自己不导航**——用户停在仪表盘，
机器人就只会讲仪表盘。ADR-0041 的决策 ③ 写的是「上下文随路由」，
但那条只覆盖了「怎么回答」，没覆盖「去哪里回答」。

而顶层入口只有五个（ADR-0038：首页 / 仪表盘 / 知识 / 审核 / 设置），
清单就在 `web/src/components/SideRail.tsx` 的 `NAV_ENTRIES` 里——**机器人要切模块，
需要的就是这份清单**。

### 现状四：机器人的作用域跟着页面走

ADR-0041 决策 ③ 定的语义是「项目页问**该项目**、其余问**全库**」。
用户这次的要求是「桌面机器人**不限制项目范围**」。
两者不冲突——**区别在于「默认」与「能否切换」**：
项目页默认问该项目，但用户应当能随时切到全库，**并且切到全库不该被页面拽回去**。

## 决策

**① 常驻 = 进程不退出 + 窗口可再唤起，用 Wails 自带能力，不引新依赖。**
三个字段各司其职：

| 字段 | 作用 |
|---|---|
| `OnBeforeClose` | **拦截关闭**：macOS 隐藏窗口（`runtime.WindowHide`）、Windows / Linux 最小化（`runtime.WindowMinimise`），并返回 `true` 阻止退出——**只有「退出 Repotory」菜单项才真退出** |
| `options.App.Menu` | macOS 菜单栏常驻的应用菜单（见决策②） |
| `SingleInstanceLock` | 再次启动时唤起已有实例（显示窗口 + 打开机器人），而不是开第二个进程抢同一个 SQLite |

**`HideWindowOnClose` 不用——这一条是读源码后的更正。**
`options.App.HideWindowOnClose` 在 macOS 上做的**不是「隐藏窗口」，而是「隐藏整个应用」**：
Wails v2.17 的 `internal/frontend/desktop/darwin/WindowDelegate.m` 里
`windowShouldClose` 走的是 `[NSApp hide:nil]`。应用被 hide 之后菜单栏让给前台应用，
**Repotory 的菜单项就不在菜单栏里了**——正好与决策②要的「菜单栏常驻」相反。
而 `runtime.WindowHide` 走的是 `[mainWindow orderOut:nil]`：**窗口没了，应用还在，
应用仍是活动应用，菜单栏还在**。
所以拦截点放在 `OnBeforeClose`，由我们自己决定「隐藏窗口」还是「最小化」。

**② macOS 上菜单栏是「状态栏」；Windows / Linux 上不是，这一点必须说清。**
macOS 的应用菜单在顶栏，**应用不激活时也在**——所以「关掉窗口 → 菜单栏里仍有 Repotory →
点它窗口回来」就是 macOS 上的状态栏常驻。菜单三项：
**显示 Repotory**（唤起窗口）、**问 Repotory…**（唤起窗口 + 直接打开机器人面板）、
**退出 Repotory**（真退出）。
应用菜单之外还挂 Wails 自带的 `menu.EditMenu()` 与 `menu.WindowMenu()`：
**Edit 菜单不是可选项**——没有它，macOS 的 webview 里就没有 Cmd+C / Cmd+V。
自带的 `menu.AppMenu()` 反而不用：它的角色由系统渲染成**只读的标准应用菜单**，
`MenuItem.Append` 对角色项无效（`isSubMenu()` 为假），挂不进去我们自己的条目；
而 macOS 把菜单栏的**第一项**当作应用菜单，所以我们自己的「Repotory」菜单排在最前，
Windows / Linux 上它渲染成窗口内的一条菜单栏。
Windows / Linux **没有等价物**：Wails v2 不提供托盘，本篇不引 systray（决策①的理由）。
这两个平台上关窗口走**最小化**而不是隐藏，任务栏保留图标，进程同样常驻——
**用户找得回，但那是任务栏图标，不是托盘**。手册与设置页都要照实说。

**③ 机器人能自己切模块：面板顶部一排模块 chip，点击 = 主区域换路由 + 上下文跟着换。**
清单**复用 `SideRail` 的 `NAV_ENTRIES`**（搬到 `web/src/components/navEntries.ts`，
两边都从那里 import），
**不另写一份**——两份清单必然漂，而导航项一漂，机器人和侧栏就会指向不同的模块。
清单单独成文件而不是留在 `SideRail.tsx` 里 export：侧栏是组件（带 CSS module），
机器人为了一份纯数据把整个侧栏拖进依赖图没有必要。
chip 是机器人的**上下文选择器**，不是第二套导航：
它换的是「机器人在回答关于哪一块的问题」，与侧栏的导航是同一个路由、同一份数据。

**④ 桌面机器人的作用域默认全库，且可切到任意项目。**
`AgentBot` 面板顶部与 chip 并排放一个**作用域 chip**（`全库` / `本项目` / 具体项目），
默认 `全库`——这就是「桌面机器人不限制项目范围」的直译。
在项目页上默认**仍然是全库**（与 ADR-0041 的「项目页问该项目」不同，
本篇显式覆盖它：桌面端是全局助手，页面只是它当前的话题）。

覆盖的**只是「自由提问的作用域」这一句**（ADR-0041 决策③ 的末句），
**不是它的快捷问答表**：`agentContext` 里把两件事拆开——
**快捷问答跟路由走**（项目页给「项目概览 / 最近提交 / 技术栈」，那是这一页自己的答案），
**自由提问跟作用域 chip 走**。选了某个项目时，问答两侧一起切到那个项目；
留在「全库」时，项目页仍然给项目自己的卡片，但提问问的是全库。
ADR-0041 决策 ⑤ 的仪表盘作用域共享单一真值**不动**——
那是仪表盘的数字口径，与机器人的提问范围是两件事。

**⑤ 「独立于桌面 App 使用」= 不依赖主窗口的当前页面，而不是脱离进程。**
机器人面板本身是 webview 内的 DOM，它永远需要宿主进程活着；
**能在窗口关闭后继续被唤起、在任意模块下继续工作，就是可用的「独立」**。
真要一个脱离进程的常驻服务，那是另一个产品形态（`repotory-server` 已经是它了，
但它没有 UI）——本篇不把它混进来。

**⑥ 两个 IDE 插件与桌面机器人的能力对齐，差异只有作用域这一条。**
对齐的部分（ADR-0044 已落地）：面板结构、问答能力、provider 配置、项目识别、写笔记/整理。
**唯一差异**：IDE 插件锁定当前工作区的项目（那是编辑器场景的默认），
桌面机器人默认全库且可切。**除此之外不允许再有别的差异**——
「插件是阉割版」不是产品定位，是没做完。

**⑦ 不动 schema / 不新增 MCP 工具 / 不改 ADR-0038·0039·0041 的任何决策 /
不改 `ai_chat_*` 配置键 / 不动品牌三域结构。**
菜单项是**进程 chrome**，不是知识能力，因此不进 MCP 工具面。

## 后果

- **正面**：
  - 叉掉窗口后 Repotory 仍在；macOS 上从菜单栏一点就回来，
    还能「问 Repotory…」直接唤起机器人面板。
  - 机器人第一次能**带路**：不用先去点侧栏，它自己知道有哪几个模块，并能把你带过去。
  - 桌面机器人与 IDE 插件的能力差收敛到一条（作用域），两边讲的是同一套东西。
- **代价（接受）**：
  - **Windows / Linux 上没有托盘**。这是 Wails v2 的能力边界，不是本篇偷懒；
    写进手册比假装有托盘强。
  - **「关窗口不退出」会改变用户的预期**：过去叉掉=结束，现在叉掉=还在跑。
    必须有明确的「退出 Repotory」入口（菜单项 + 任务栏右键），否则用户会觉得进程没死干净。
  - **机器人多了两排控件**（模块 chip + 作用域 chip）。
    机器人在桌面端本来就是浮层，加两行 chip 会让它更重——
    所以 chip 行**只在面板打开时出现**，折叠状态下仍是那个 52px 的球。
  - **机器人的默认作用域从「项目页=该项目」变成「总是全库」**，
    这是对 ADR-0041 决策 ③ 的一处**显式覆盖**。
    代价：项目页上问「这个项目最近踩了什么坑」，用户得自己切作用域——
    **机器人面板顶部的 chip 会显示当前作用域**，不假装。
- **不能假装解决的**：**机器人不能脱离进程**（决策⑤）。
  想要「关掉 App 还有个独立小窗在」，那是第二条原生窗口，
  Wails v2 的单窗口模型给不了，本篇不做。

## 落地清单

| 面 | 文件 |
|---|---|
| 常驻与菜单 | `main.go`（`OnBeforeClose` / `SingleInstanceLock` / `Menu`） |
| 菜单 → 前端 | Wails 事件（`runtime.EventsEmit`，事件名 `app.ask-bot`）→ `web/src/api/transport.ts` 的 `listenAskBot` |
| 模块切换 | `web/src/components/navEntries.ts`（模块清单）、`web/src/components/SideRail.tsx`、`web/src/components/AgentBot.tsx`（模块 chip） |
| 作用域 | `web/src/components/AgentBot.tsx`、`web/src/components/agent/agentContext.ts` |
| 双语词条 | `web/src/locales/{zh-CN,en}/common.json`（**两份同改**） |
