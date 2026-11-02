# ADR-0007: 会话记忆协议（context / handoff 双工具）

- 状态：Accepted
- 日期：2026-09-28
- 关联：[ADR-0006](0006-scope-freeze.md)（核心闭环优先）、[ADR-0005](0005-service-layer.md)（服务层统一）

## 背景

ADR-0006 将核心闭环定为「发现 → 理解 → 记录 → 检索 → AI 使用」，但闭环的两端在 agent 视角下是断的：

1. **会话开始**：agent 拿到一个新任务后，需要自己链式调用 `projects_list` → `notes_search` → `notes_read` 3-4 次才能拼出项目上下文；多数 agent 直接跳过这一步，带着零上下文开工。
2. **会话结束**：agent 学到的东西（决策、坑、下一步）随会话消失。`notes_create` 存在但没有协议——agent 不知道何时写、写什么结构，人类也无法预测产出格式。

「AI 项目记忆层」的叙事要成立，记忆必须在**会话边界自动发生**，而不是依赖用户记得去保存。

## 决策

新增三个 MCP 工具，构成会话记忆协议（含冷启动）：

### `repotory_context`（会话开始）

一次调用返回项目完整上下文的 Markdown 文档：

- 技术栈 / README 摘要 / 语言占比 / 依赖 / 贡献者 / 活跃度（来自 `repo_meta` 挖掘缓存，未缓存时后台异步挖掘并在文档中声明）
- 最近提交（实时 git log）
- 开放待办
- 高相关知识笔记；**带 `handoff` 标签的笔记排最前**（它们记录上次会话如何结束）

项目解析协议（`ResolveProject`）：`project_id` 精确解析 → `project_name` 名称/路径模糊匹配 → 无参且仅一个项目时自动解析。多匹配时返回项目目录（含 ID 表格）让 agent 选择，绝不猜测注入错误项目的上下文。

> **协议增补（2026-10-13）：路径解析阶。** 上面这条协议在今天多了一个前置输入，且原三步全部保留：
> `repotory_context` 与 `repotory_handoff` 现在接受可选的 `path`（IDE 工作区目录或 agent 的 cwd），由
> `ResolveWorkspace` 统一裁决——**显式的 `project_id` / `project_name` 永远优先**，只有它没能定出项目时才用
> `path` 兜底。顺序不是风格问题：让调用方点名的项目被它恰好所在的目录改写，就是本条协议一直禁止的"注入错误
> 项目的上下文"，只不过换了一副看起来更聪明的面孔。
>
> 兜底之所以必要，是因为原协议第 2 步的模糊匹配方向是「项目 root_path 包含查询串」，于是多仓库项目的**子目录**
> （`…/CBiPay/pay-common`）三步全不命中，而 `repositories` 表明明记着这个目录却从未被查询过。路径解析按四阶
> （仓库精确 → 仓库最深祖先 → 项目精确 → 项目最深祖先，细则见 [ADR-0021](0021-temporal-graph-page-versions.md)
> 同批的 `internal/service/workspace.go` 与其测试）补齐这一格，并且把「匹配上了什么」随结果返回
> （`repository-exact` / `project-ancestor` / `unresolved` …），使「绝不猜测」这句承诺变成可检查的事实而非态度。

### `repotory_handoff`（会话结束）

结构化交接协议，入参为 `summary`（必填）+ `changes` / `decisions` / `gotchas` / `next_steps`（数组，至少一项非空）+ `agent`（写入方标识）。渲染为固定 Markdown 模板落库（`kind=knowledge`、`source=mcp`、tags 自动含 `handoff`）。

固定模板是刻意的：标题结构是写入方与所有未来读者（人类 + agent）之间的契约，`repotory_context` 靠 `handoff` 标签将其排序置顶。

### `repotory_scan`（冷启动）

会话记忆协议要成立，前提是数据库里已有项目。原先只有桌面应用能完成「播种扫描根 + 扫描」，导致文档宣称的「MCP 不需要桌面应用」是空话——纯 MCP 安装的 `repotory_context` 只会返回「无项目」。为此把扫描能力下沉到服务层并暴露为第三个工具：

- `Service.EnsureDefaultScanRoots`：首次运行播种平台默认扫描根（`scan_roots_seeded` 标记；已有扫描根时不覆盖）；
- `Service.ScanNow`：同步执行完整扫描管线并返回仓库/项目计数（`TriggerScan` 保留给桌面端的后台异步场景，二者共用 `scanning` 互斥）；
- `repotory_scan`：调用两者，使首次可用漏斗从「安装→扫描→收藏→刷新→使用」压缩为「安装→`repotory_scan`→`repotory_context`」。

桌面端首启也改为复用 `Service.EnsureDefaultScanRoots`，两端行为一致。

## 理由

- **零参数即可用**：单项目安装直接 `repotory_context()` 拿全上下文，把「记忆加载」的成本降到一次调用。
- **跨 agent**：交接落在本地 SQLite 而非任何 agent 私有记忆格式，Claude Code 写的交接 Cursor 直接读。
- **服务层共享**：三个工具实现在 `internal/service`（`context.go` / `handoff.go` / `scan.go`），桌面端复用同一实现（ADR-0005 分层）。
- **多匹配不猜测**：错误上下文比没有上下文更危险，歧义时返回目录由 agent 二次选择。

## 后续方向（未决）

- 会话自动捕捉：解析 `~/.claude/projects/*/*.jsonl` 生成会话摘要（零人工参与，需评估隐私与体积）
- 多源记忆导入：Cursor、Codex、OpenCode 的记忆格式
- 语义检索：本地 embedding（评估 sqlite-vec），补充 FTS5 的字面匹配盲区
- Claude Code hook 集成文档：SessionEnd hook 自动触发 `repotory_handoff`

## 术语澄清：`handoff` = 持久化的会话交接记录（2026-11-02）

本节是对既有决策的**加注，不改写**上面的原文。触发它的是一份 VS Code session/memory 调研（VS Code 1.144 官方文档 2026-10-28），那份调研里的术语与本协议同名不同义——不澄清，用户会把 Repotory 的 handoff 误当成 IDE 里那个同名按钮。

**1. 本协议里的 `handoff` 专指「持久化的会话交接记录」。** 会话结束时写下的一条知识笔记（`kind=knowledge`、`source=mcp`、tags 含 `handoff`），落进项目知识库；下一次会话由 `repotory_context` 读回——**最新一条全文渲染，更早的折成一行时间线**。它是一份**存下来的记录**，读它的是任何一个 agent，不必是写它的那一个。

**2. 它不是「在 harness / 执行环境之间移交一场正在进行的对话」。** 那是 VS Code 内建功能 "Hand off a session" 的含义（VS Code 1.144，官方文档 2026-10-28）：换一个 harness、执行环境或角色，并把完整对话历史带过去，是**运行时的续接**。二者**不构成竞争**——后者是单一 IDE 内的运行时续接，前者是跨 agent、协议中立、无账号、全本地的持久记录；两边做的是不同的事。

**3. 协议名与 MCP 工具名 `repotory_handoff` 一律不改**，笔记标题模板 `Session Handoff <time>` 同样不改。理由是协议稳定性：重命名会破坏所有既有 agent 集成，也会破坏依赖它的 Claude Code SessionEnd 自动捕捉 hook（`scripts/repotory-init` 写出的 `.claude/hooks/repotory-handoff.sh`）。**歧义存在于描述与产品措辞，所以就在描述与产品措辞里修**——本节就是这个选择的落点。

**4. 落地位置清单**（本轮同步改）：

- MCP 工具描述：`cmd/mcp/tools_context.go` 的 `repotory_handoff`（写的是持久化记录、不是实时移交）与 `repotory_context`（上一轮的交接记录，最新一条全文 + 更早一条时间线）
- MCP 拒绝文案：`cmd/mcp/main.go` 的 handoff 覆盖拒绝（明写「session handoff record」）
- VS Code 面板文案：`ide/vscode/` 的命令标题、输入提示、输出通道与状态栏 tooltip
- 桌面端文案：见对应的前端文案批次

## 修订记录（2026-11-02，术语澄清）

本 ADR 的决策原文保留，本次只追加澄清，不改写任何既有决策：

- **追加「术语澄清」一节**（见上）——把本协议里的 `handoff` 钉死在「持久化的会话交接记录」上，并写明它与 VS Code "Hand off a session"（运行时换 harness / 执行环境 / 角色，带走完整对话历史）同名不同义、且不构成竞争。
- **协议名、MCP 工具名 `repotory_handoff`、笔记标题模板 `Session Handoff <time>` 均不更名**——重命名会破坏既有 agent 集成与 Claude Code SessionEnd 自动捕捉 hook；歧义在描述里修，不在标识符里修。
- **歧义清理落在描述与产品措辞**：MCP 工具描述、MCP 拒绝文案、VS Code 面板文案（命令标题 / 输入提示 / 输出通道 / 状态栏 tooltip）。`internal/service/handoff.go` 与 `internal/service/context.go` 的渲染逻辑与模板**不动**。
