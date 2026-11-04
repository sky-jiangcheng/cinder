# ADR-0046: macOS reopen 通道——Dock 图标点击能把隐藏的窗口唤回来

- 状态：**Superseded（2026-11-04，[ADR-0047](0047-resident-agent-separate-host.md) 决策⑤）**——**本篇的实现从仓里删除**（`internal/platform/darwinreopen/`），因为它**失去了唯一消费者**
- 日期：2026-11-04
- 关联：[ADR-0045](0045-resident-desktop-bot.md)（桌面机器人常驻：关窗口不退出、macOS 用菜单栏当状态栏——**其决策①②已被 [ADR-0047](0047-resident-agent-separate-host.md) 取代**）、[ADR-0047](0047-resident-agent-separate-host.md)（桌面 App 回归关窗口即退出、独立项目做 copilot）

> **2026-11-04 后续（ADR-0047 之后）**：本篇治的病是**「Wails 隐藏的窗口唤不回来」**，
> 而 ADR-0047 决策① 让桌面 App **不再隐藏窗口**（关窗口即退出），
> 决策⑤ 让常驻助手落到 **Tauri 的托盘**上——**Tauri 不走 Wails 的 `orderOut:` 那条路**，
> 它自带托盘图标与点击唤起。**于是这段 reopen 钩子没有消费者了。**
>
> **处置**：实现（`internal/platform/darwinreopen/`）**从仓里删除**。
> **它从未进过 git 历史**——只活过本篇那一轮的工作区，还没提交就被 ADR-0047 删掉了，
> 所以**没有「从历史取回」这回事**（`git log -- internal/platform/darwinreopen/` 是空的）。
> **留下来的只有本篇的分析。**
> **为什么删而不是留**：仓里第一次有了 Objective-C 源码（`.m` + cgo + 依赖 Wails 内部类名
> `AppDelegate`），留着一段**已验证正确但没有消费者**的注入代码，成本是持续的
> （cgo 构建面、Wails 升级即失效的脆弱性），而它解决的问题已经不存在。
>
> **本篇的分析结论仍然全部成立**（Wails v2 没有 reopen 通道、`orderOut:` 唤不回来、
> `@encode(BOOL)` 要按架构生成）——**那部分是知识，不是代码**，
> 下一个想给 Wails 加常驻的人会因为这段分析省掉一次排查。
>
> 另见 ADR-0047 的第一件核实：**Wails v2 没有托盘**
> （`options.App` 无 Tray 字段，`pkg/menu` 的 `TrayMenu` 是死类型），
> 而外部 `systray` 要 cgo、会让 CI 的裸 ubuntu `go build ./...` 编译失败——
> 这才是「常驻助手」必须换宿主的**硬理由**（Tauri v2 自带 `tray` 模块）。

## 背景

ADR-0045 让桌面端常驻：叉掉窗口不退出进程，macOS 上靠菜单栏的「显示 Repotory」唤回。
**但那条唤回路径只有一条**，而用户报告「应用打不开界面」——排查后确认根因链是：

1. **残留的常驻实例占着单实例锁**（旧版本没有锁机制）；
2. 再次启动时新实例立刻退出，用户看到的是「双击了图标，什么都没发生」；
3. **macOS 上没有任何通道能把那个还活着的实例的窗口唤回来**。

第 3 条是本篇要治的。

### 现状一：macOS 的 reopen 事件有标准入口，Wails v2 没有实现

macOS 有一条标准约定：用户点击运行中应用的 Dock 图标时，系统派发
`applicationShouldHandleReopen:hasVisibleWindows:` 给 NSApplication 的 delegate。
这个 delegate 由 Wails v2 自己创建——`internal/frontend/desktop/darwin/Application.m`
的 `Run()` 里：

```objc
AppDelegate* delegate = [AppDelegate new];
[app setDelegate:(id)delegate];
```

而 `internal/frontend/desktop/darwin/AppDelegate.m` **全文没有这个方法**。
`AppDelegate.h` 声明的是 `@interface AppDelegate : NSResponder <NSApplicationDelegate, NSTouchBarProvider>`，
`NSApplicationDelegate` 协议里 `applicationShouldHandleReopen:hasVisibleWindows:` 是**可选方法**，
不实现就没有任何回调。

`options.App` 的 `mac.Options` 里也**没有任何 reopen 钩子**
（全集只有 `TitleBar` / `Appearance` / `ContentProtection` / `WebviewIsTransparent` /
`WindowIsTranslucent` / `Preferences` / `DisableZoom` / `About` / `OnFileOpen` /
`OnUrlOpen` / `DisableEscapeExitsFullscreen`），
`pkg/runtime` 也没有对应的运行时方法。
**结论：Wails v2 在 macOS 上没有 reopen 通道，这是能力边界，不是配置问题。**

### 现状二：为什么 AppKit 默认的「unhide」也救不回来

macOS 对「应用在跑但没有可见窗口」有默认处理：把应用 unhide 出来。
**但 Wails 的隐藏方式不是 unhide。**

`internal/frontend/desktop/darwin/WailsContext.m` 里：

```objc
- (void) Hide { [self.mainWindow orderOut:nil]; }
- (void) Show { [self.mainWindow makeKeyAndOrderFront:nil];
                [NSApp activateIgnoringOtherApps:YES]; }
```

`orderOut:` 把窗口从窗口列表里摘掉——**窗口还在，但不在屏幕上的窗口列表里**。
AppKit 的默认 reopen 行为恢复的是「应用的窗口」，不是「被 orderOut 掉的窗口」。
所以点击 Dock 图标的结果是：**应用被激活了（菜单栏回来了），但屏幕上什么都没有。**
这与用户描述的「点了没反应」完全吻合。

### 现状三：`showWindow()` 静默失败

`main.go` 里唤回窗口的三个函数都长这样：

```go
func showWindow() {
	ctx := runtimeContext()
	if ctx == nil {
		return          // ← 静默返回，一行日志都没有
	}
	wailsruntime.WindowShow(ctx)
}
```

`runtimeContext()` 在 `OnStartup` 之前返回 nil。
**用户看到的就是「点了没反应」，而日志里一个字都没有**——排查时无从下手。
这条静默失败还多一层硬风险：`pkg/runtime` 的 `getFrontend(ctx)` 在 ctx 为 nil 时
**直接 `log.Fatalf` 退出进程**，所以那个 `if ctx == nil` 分支不只是「没做事」，
它是**在替一个会崩进程的调用挡刀**——挡得住，但挡得无声无息。

## 决策

**① 装 reopen 钩子走 Objective-C runtime 注入，不改 ADR-0045 的隐藏方式。**

delegate **实例**归 Wails 所有（它自己 new 出来并 setDelegate），换不掉。
但 delegate 的**类**可以动——Objective-C runtime 的 `class_addMethod`
能把缺失的 selector 追加到类上，而这个实现可以回调进 Go。

于是新增 `internal/platform/darwinreopen/` 包：

| 文件 | 职责 |
|---|---|
| `reopen.go` | 公共层：handler 存储（`sync.RWMutex`）+ `trigger()` 桥接 + 三个哨兵错误 |
| `reopen_darwin.go` / `reopen_darwin.m` | darwin+cgo 实现：runtime 注入 + C 回调 |
| `reopen_other.go` | 非 darwin 或无 cgo 时的桩（返回 nil，不做事） |

`main.go` 在 `wails.Run` **之前**调 `darwinreopen.Install(showWindow)`——
Objective-C 类在二进制加载时就注册好了，早装比晚装稳（不依赖任何 delegate 实例已存在）。

**② 钩子只做「唤回窗口」，不自动打开机器人面板。**
Dock 图标点击的语义是「把应用叫回来」，不是「替用户发起一次对话」。
机器人面板是应用的首页，**但那不等于每次被叫醒都要弹面板**——
菜单里的「显示 Repotory」和「问 Repotory…」正是这两件事分开做的先例。
reopen 对应前者。

**③ 钩子装不上时，记日志，不崩。**
`Install` 返回错误（`ErrNoDelegate` / `ErrAlreadyInstalled` / `ErrInstallFailed`），
`main.go` 记一行日志继续跑。**常驻应用的唤回路径应该有主备**：
主路径（Dock 点击）装不上时，菜单栏那条路径还在。
装了之后如果 `class_getInstanceMethod` 查出这个 selector 已经存在
（将来的 Wails 自己去实现了），返回 `ErrAlreadyInstalled` 而**不覆盖**——
**谁拥有这个行为谁负责**，不由本仓抢。

**④ 三个 `ctx == nil` 分支全部补日志。**
`showWindow` / `askBot` / `quitApp` 各补一行 `log.Printf`，
写清「应用上下文还没就绪，窗口没唤回」。**静默失败是这次排查最大的时间黑洞**，
不能再有第二条。

**⑤ 不改 ADR-0045 的任何决策。**
「macOS 用 `WindowHide` 而不是 `HideWindowOnClose`」这条原文保留——
`HideWindowOnClose` 走的是 `[NSApp hide:nil]`（隐藏整个应用，菜单栏会让给前台应用），
与「菜单栏常驻」直接冲突，**那不是本篇能翻的账**。
本篇是在 ADR-0045 留的唤回路径上**补一条**，不是改那条。

**⑥ 不引新依赖，不新增 MCP 工具，不改 schema。**
钩子是进程 chrome 层面的事，与知识能力无关。

## 后果

- **正面**：
  - **点击 Dock 图标能唤回窗口了**——这是 macOS 上最自然的操作，
    之前完全不通。
  - 唤回失败不再无声无息：日志里能看见「上下文没就绪」或「钩子没装上」。
  - 唤回路径从一条变两条：Dock 点击（主）+ 菜单栏（备）。
- **代价（接受）**：
  - **仓里第一次有了 Objective-C 源码**（`reopen_darwin.m`）。
    `.m` 文件遵守 `//go:build`，本篇用 `darwin && cgo` 把影响面收在
    「macOS 桌面 App」这一处——`go build ./...` 在 ubuntu / windows /
    `CGO_ENABLED=0` 下都走 stub，**CI 那三道门不受影响**。
  - **依赖了 Wails 一个内部类名**（`AppDelegate`）。
    改名就会装不上——所以装不上要**记日志**而不是静默，
    下一个人一眼能从日志里看出「钩子没装上」。
  - **钩子只在 macOS 上存在**。Windows / Linux 的唤回路径没变
    （任务栏图标 + 第二实例），本篇不碰。
  - **selector 的类型编码在运行时拼**（`@encode(BOOL)`），
    因为 BOOL 在 x86_64 上编码成 `c`、arm64 上是 `B`——写死一个会让另一个架构炸。
- **不能假装解决的**：
  - **这不是「窗口起不来」的通用修复**。上一轮排查里另有一条根因是
    **残留常驻实例占着单实例锁**——那个靠清进程与锁文件解决，
    本篇的钩子管不着它。
  - **钩子装不上时，唤回就只剩菜单栏那一条**。这是可接受的降级，
    但**得让人看得见**——这就是决策③要记日志的原因。

## 落地清单（本篇已被取代，这张表记录的是**当时的计划**，不是现状）

> ⚠️ **下面三行从未进入过这个仓库。** 钩子在 ADR-0046 那一轮的工作区里写完并验证过，
> 但还没提交就被 ADR-0047 删掉了（决策⑤）——`git log -- internal/platform/darwinreopen/` 是空的。
> **这张表留在这里是为了说明「当时打算怎么做」，不是「现在有什么」。**

| 面 | 文件（**未入库**） |
|---|---|
| 钩子本体 | `internal/platform/darwinreopen/`（`reopen.go` / `reopen_darwin.go` / `reopen_darwin.m` / `reopen_other.go`） |
| 接线与日志 | `main.go`（`wails.Run` 前的 `darwinreopen.Install(showWindow)`；`showWindow` / `askBot` / `quitApp` 的 `ctx == nil` 分支补日志） |
| 单元测试 | `internal/platform/darwinreopen/reopen_test.go`（handler 桥接；平台钩子本身需真实 NSApplication，由实机验证覆盖） |
