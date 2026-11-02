# VS Code 的 session 保存 / memory / handoff —— 与 Repotory handoff 的竞争关系调研

日期：2026-11-02 ｜ 对象：VS Code 1.144（文档日期 2026-10-28）+ Copilot Memory + session sync (Experimental)
参照系：Repotory 的 `repotory_handoff`（ADR-0007 会话记忆协议）与 ADR-0010（会话自动捕捉）
材料：VS Code 官方文档（sessions / memory / session-history / manage-sessions / agents 五页）+ microsoft/vscode issue #318855 + 本仓源码实读。**未在真 IDE 实测**，所有 VS Code 侧表述均转引自官方文档，本文不背书。

## 0. 一句话结论

**有竞争，但竞争的是 VS Code 的「session 存储 / 检索」这一层，不是「handoff 协议」这一层。**

VS Code 已经把「会话记录持久化 + 跨设备同步 + 跨会话检索」做到了产品级（`/chronicle:*`、session sync、Copilot Memory）。它抢走的是**「上一轮干了什么」这个问题的第一直觉答案**——用户不再需要 Repotory 来回答"上次那个 503 重试是怎么解决的"，因为 `Copilot → /chronicle:search 503` 就能答。

但 Repotory 的 handoff 在三个维度上仍然不可替代，且这三个维度**不是 VS Code 会去做的**（商业动机不同）：

1. **跨 agent 中立**：VS Code 的 session 存储绑定 Copilot 生态（Copilot / Claude Code / Codex / Copilot CLI 的 session 只能被「发现」并**打开**，不能被第三方 agent **读取**）。Repotory 的 handoff 落在本地 SQLite，**任何 MCP client 都能读**——这是 ADR-0007 明写的产品定位。
2. **写入口在 agent 手里，不在 IDE 手里**：VS Code 的 session 记录是 IDE 观测到的对话流水；Repotory 的 handoff 是 agent **主动结构化输出**（summary/changes/decisions/gotchas/next_steps 五段式）。前者是「过程日志」，后者是「结论」。
3. **本地优先、无云、无账号**：Copilot Memory 是**云端**服务、绑 GitHub 账号、有 28 天未用自动删除。Repotory 是本地 SQLite 单一事实源、无账号、不过网。

**风险不在"被替代"，在于"用户不知道两个东西的区别"。** 真正要做的是把差异化写进产品语言与 IDE 面板的措辞，而不是加功能。

## 1. VS Code 那边实际有什么（三层，别混为一谈）

调研中最容易犯的错是把下面三层当成一件事。它们是三套独立机制。

### 1.1 Session 存储层（本地 SQLite，VS Code 私产）

- `github.copilot.chat.localIndex.enabled` 默认 **true**，本地跟踪 session。
- 记录内容：**session 元数据**（仓库/分支/工作目录/时间戳/参与的 agent）+ **对话轮次**（用户消息 + 助手回复）+ **动过的文件**（来自 tool call 的路径）+ **外部引用**（PR 号 / issue 号 / commit SHA）。
- 存在**本地 SQLite**（与 Repotory 同一种技术选型）。
- 可用 `Chat: Export Chat...` 导出 JSON，或复制 Markdown。
- **第三方扩展读不了**：issue #318855（2026-05-28）明确说「扩展目前除活动会话外没有受支持的方式检查 chat session」，唯一可行路径是直接读私有存储（`workspaceStorage/{hash}/chatSessions/*.jsonl` + `state.vscdb`），而「这个私有存储格式已经在生产中弄坏扩展了」。该 issue 提议了 `vscode.chat.getSessions()` / `getSessionHistory()` 的只读 API，**状态：proposal，未落地**。

### 1.2 Session 同步层（云端，Experimental）

- `chat.sessionSync.enabled` 默认 **false**（opt-in）。
- 开启后 session 历史上传到你的 GitHub 账号，**跨设备访问**，也能分享给有仓库权限的人（只读）。
- 可按 `chat.sessionSync.excludeRepositories` 排除仓库。
- 企业策略 `CopilotSessionSync` 可以强制关闭。
- **注意**：同步的是 Copilot harness 的 session。VS Code 能「发现」Copilot CLI / GitHub Copilot app / Claude Code / Codex 的本地 session，但发现≠可读。

### 1.3 Memory 层（两套，Copilot vs Local harness）

这是最容易被误读成"竞争"的一层，实际威胁最大但**和 handoff 不是同一个东西**。

**Copilot Memory（云端）**

- 两种 scope：**Repository**（仓库事实，需仓库写权限，随仓库共享给有 Copilot Memory 权限的人）/ **User**（跨仓库的个人偏好，只对自己生效）。
- 用自然语言让 agent 记；agent 用 Store Memory 工具存，验证过就 upvote，过时就 downvote 并存修正版。
- **28 天未使用自动删除**；成功验证与使用会重置计时器。
- 存在 GitHub 云端，绑账号。

**Local memory files（本地，Local harness）**

- 三个 scope，各有固定路径：

  | Scope | 路径 | 跨会话 | 跨工作区 | 用途 |
  |---|---|---|---|---|
  | User | `/memories/` | 是 | 是 | 偏好、模式、常用命令 |
  | Repository | `/memories/repo/` | 是 | 否 | 代码库约定、项目结构、构建命令 |
  | Session | `/memories/session/` | 否 | 否 | 任务临时上下文、进行中的计划 |

- **User memory 前 200 行在每次 session 开始时自动载入 agent 上下文**。
- Plan agent 用 session memory 把实现计划写进 `plan.md`。
- 命令：`Chat: Show Memory Files`、`Chat: Clear All Memory Files`。

### 1.4 检索层：Chronicle

`/chronicle:*` 命令（本地 session 索引上跑）：

| 命令 | 作用 |
|---|---|
| `/chronicle:standup` | 把近期会话汇总成站会报告（按分支与仓库分组，默认 24 小时） |
| `/chronicle:tips` | 分析历史会话，建议更高效地用 Copilot |
| `/chronicle:cost-tips` | 找出省 token 的机会 |
| `/chronicle:search <query>` | 按关键词 / 文件路径 / PR / issue 检索会话 |
| `/chronicle:reindex` | 重建本地 session 索引 |

也能用自然语言问（"我昨天改了哪些文件？"），Copilot 用语义理解找相关会话。

**这是对 Repotory 最直接的替代**：用户想"回忆上次干了什么"，现在有个一等公民的 UI 入口，而且已经在 VS Code 里默认开着（localIndex 默认 true）。

### 1.5 VS Code 自己的 "Hand off a session"

VS Code 确实用了 "handoff" 这个词，但**语义和 Repotory 完全不同**：

> Handoff continues ongoing work with a different agent configuration and carries the full conversation history and context with it. A handoff can change the harness, execution environment, or agent role.

即：**把一个正在进行的 session 换 harness / 换执行环境 / 换角色**（Plan agent 产计划 → 交给 implementation agent；Local harness → Claude/Codex）。它携带**完整对话历史**，是**运行时续接**，不是**持久化的交接记录**。

同名不同物。这是本轮调研最容易出的沟通事故——如果 Repotory 的界面或文案里也用 "handoff" 指代"上一轮的结论记录"，用户会以为它和 VS Code 那个按钮是一回事。

## 2. 与 Repotory handoff 的逐项对比

| 维度 | VS Code session/memory | Repotory handoff | 谁占优 |
|---|---|---|---|
| 存什么 | 对话流水 + 元数据 + 文件路径 + PR/commit 引用 | **结构化结论**：summary / changes / decisions / gotchas / next_steps | **Repotory**（流水 ≠ 结论） |
| 谁写 | IDE 观测，agent 不参与 | **agent 主动写**（`repotory_handoff` MCP 工具） | **Repotory**（结论只有 agent 知道） |
| 粒度 | 一次 session 一条记录 | 一条 handoff = 一次会话的退出记录，按 `handoff` tag 排序置顶 | 各有 |
| 读取方 | 仅 VS Code 自己的 UI / agent（同一 harness 内） | **任何 MCP client**（Claude Code / Cursor / Codex / 任意 agent） | **Repotory** |
| 跨 agent | 能「发现并打开」外部 session，**不能读取其内容** | 落本地 SQLite，**协议中立** | **Repotory** |
| 跨项目 | 用户偏好可跨仓库；repo memory 按工作区隔离 | 按 **project_id** 组织，一个项目一份；跨项目需显式检索 | 各有 |
| 冷启动 | session 恢复靠"选一个 session 打开" | `repotory_context` 一次调用 → **最新 handoff 全文 + 更早的折成一行时间线** | **Repotory**（单调用、无需人工挑） |
| 人在环 | 无需审核，agent 自己写/自己存 | **人工闸门 ADR-0015**：机器写的措辞要人审 | **Repotory** |
| 本地优先 | session 索引本地 ✓；**Copilot Memory 在云** ✗ | **全本地**（本地 SQLite 单一事实源） | **Repotory** |
| 需要账号 | Copilot Memory / session sync 需 GitHub 账号 | **不需要** | **Repotory** |
| 保留期 | Copilot Memory **28 天未用即删** | 无自动删除（用户自己管） | 各有 |
| 可编程 | 扩展**读不了**（#318855 仍是 proposal） | **18 个 MCP 工具**，公开稳定 | **Repotory** |

## 3. 真实竞争点：只有三处

### 竞争点 1：`repotory_context` 的"上一轮干了什么"这一格

`repotory_context` 的最大卖点之一是"最新 handoff 排在最前、全文渲染"。VS Code 的 `/chronicle:search` 和"恢复一个 session"在做同一件事。

**但入口不同**：VS Code 的入口是**用户在 IDE 里手动点**（选一个 session / 跑一个命令）；Repotory 的入口是**agent 在冷启动时自动调用**。用户不需要知道 Repotory 存在，agent 自己会用 MCP 拿上下文。这个差别是结构性的——**记忆的价值在于被自动注入，不在于被检索**。

### 竞争点 2：Copilot Memory 的 Repository scope

Copilot Memory 的 Repository scope 存的是"仓库事实与约定，随仓库共享给有权限的人"——这正是 Repotory 知识库的一部分定位（项目知识）。

**差别在审核与归属**：Copilot Memory 的仓库事实由 agent 自动存、upvote/downvote 维护、**28 天不用就删**；没有人工闸门、没有项目级组织、没有「这条知识属于哪个项目」的显式归属（靠 GitHub 仓库）。Repotory 的知识有 project_id、有 kind（knowledge/log/idea/todo）、有 source（mcp/manual/auto-captured）、有人工审核闸门。

而且 **Copilot Memory 只对 Copilot harness 生效**。用 Claude Code / Cursor 的用户在 VS Code 里得不到它——**这正是 Repotory 的生态位**。

### 竞争点 3：VS Code 能发现 Claude Code / Codex 的 session

这是最微妙的一处。VS Code 现在能发现并打开 Claude Code、Codex、Copilot CLI 的本地 session。

**但"发现并打开"≠"读出来喂给我的 agent"**：
- 打开 = 把那个 session 载入 VS Code 的 session 列表，你能读它的对话；
- 读取 = 让**你的** agent 拿到**结构化的**交接结论。

所以它压缩的是"用户回忆"的成本，不是"agent 冷启动"的成本。后者仍是 Repotory 的位置。

## 4. 建议：做什么 / 不做什么

### 4.1 不做（明确划界）

- **不做**会话自动捕捉的替代品。Repotory 已有 `repotory-capture`（Claude Code SessionEnd hook，ADR-0010）+ `internal/service/handoff_capture.go`。VS Code 的 session 索引是 IDE 私产、扩展读不了，**它替代不了 Repotory 的跨 agent 捕捉**。
- **不做** `/chronicle:*` 的对标。Chronicle 查的是"对话流水里出现过什么字样"；Repotory 查的是"结论"。语义不同，检索能力也不是同一维度（Repotory 有 FTS5 + sqlite-vec + `repotory_ask` 的证据问答）。
- **不做**云同步 / 跨设备。ADR-0017 的「一核两入口」与本地优先是产品级承诺，Copilot Memory 是**云端 + 28 天过期**，这两条路走不通。

### 4.2 做（三条，按性价比排序）

**① 把"两个 handoff"的歧义从产品语言里消掉（最高优先级，零开发成本）**

VS Code 的 "Hand off a session" 是运行时换 harness；Repotory 的 `repotory_handoff` 是持久化交接记录。**同名不同义**。

在 IDE 面板、`repotory_handoff` 工具描述、ADR-0007 里，把 Repotory 侧的措辞锚定到"会话交接记录 / 上一轮的结论"，避免与 VS Code 的 handoff 按钮撞名。工具描述里已有 "records what the session accomplished"，可以更明确。

**② 在 Repotory 的产品语言里明确"我们做的是跨 agent 的那部分"**

VS Code 的 session 能力全部锚在 Copilot 生态（Copilot harness / Copilot Memory / session sync 到 GitHub 账号）。Repotory 的差异化是**协议中立 + 无账号 + 本地**。这三点在 README / docs 的定位段应该被显式讲出来——目前文档讲的是"本地优先"，没有讲"agent 中立"。

**③ 观察 Copilot Memory 的 28 天过期窗口，看它是否值得我们做"衰减"**

VS Code 的做法（未使用自动删除、验证可重置计时器）是一种**有界记忆**设计。Repotory 目前无自动删除、无衰减。这不是说要做——本仓的纪律是"事实不抄写、计数不手抄"，且删除策略涉及人工闸门——但值得在 TODO 里记一笔，作为「知识库无限增长」这个已知代价的对照方案。

### 4.3 一个可观察的信号

`/chronicle:standup` 的输出形态值得跟踪：它把近期会话按**分支与仓库**分组成站会报告。这几乎就是 Repotory `handoff` 的"时间线"视图，只是内容是流水不是结论。如果它进一步做到"每个会话给出一段结构化总结"，那它就在向 Repotory 的形态靠拢——**那时才是需要认真重估的信号**。

## 5. 事实清单（可核查）

VS Code 侧（官方文档，2026-10-28 版）：
- session 存储：本地 SQLite，默认开启（`github.copilot.chat.localIndex.enabled` = true）
- session sync：`chat.sessionSync.enabled` 默认 false（Experimental），需 GitHub 账号
- Copilot Memory：云端，默认开启（`chat.copilot.memory.enabled`），Repository + User 两种 scope，**28 天未用自动删**
- Local memory files：User `/memories/`、Repository `/memories/repo/`、Session `/memories/session/`；**User memory 前 200 行每次 session 自动载入上下文**
- Chronicle：`/chronicle:standup|tips|cost-tips|search|reindex`
- VS Code 的 "Hand off a session"：换 harness / 执行环境 / 角色，携带完整对话历史
- 扩展读 session：**无受支持 API**（microsoft/vscode#318855，2026-05-28，提议中）
- 版本：1.144，文档日期 2026-10-28

Repotory 侧（本仓源码实读）：
- `internal/service/handoff.go`：五段式结构化输入（Summary 必填 + Changes/Decisions/Gotchas/NextSteps 至少一项非空），落 `kind=knowledge`、`source=mcp`、tags 含 `handoff`；写入边界（summary ≤ 4000 字符、每段 ≤ 20 项、每项 ≤ 1000 字节）
- `internal/service/context.go`：`BuildProjectContext` 把 handoff 置顶——**最新一条全文渲染**（≤ 4000 字符），更早的折成一行时间线（≤ 140 字符）
- `internal/service/handoff_capture.go` + `cmd/repotory-capture`：Claude Code SessionEnd hook 自动捕捉，tag `auto-captured`
- MCP 工具面：**18 个**（`repotory_handoff` / `repotory_context` 在内），跨 client 可用

## 6. 结论

**不做功能对抗，做定位澄清。**

VS Code 已经把「会话记录保存 + 检索 + 同步」做成了 IDE 内建能力，Repotory 在这一层**没有胜算，也不该去打**——对手是 IDE 自带、默认开启、账号绑定的产品级功能。

Repotory 的位置在 VS Code 能力的**边界之外**：跨 agent、协议中立、无账号、全本地、agent 主动写结论而非 IDE 被动记流水、人在闸门。VS Code 的 session 能力越强，这个位置反而越清晰——**因为它把"会话记录"这件事定义成了 IDE 的内部实现细节，而 Repotory 把它定义成一份可被任意 agent 读取的知识资产。**

需要立刻做的只有一件事：**消除 "handoff" 一词的歧义**。其余可进 TODO 观察。
