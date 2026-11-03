# ADR-0043: 交接指令进项目指令文件——把「开局读 / 收尾写」做成产品能力（受管区块）

- 状态：Accepted（2026-11-03，用户要求「按最贴合的设计进行改进」）
- 日期：2026-11-03
- 关联：[ADR-0007](0007-session-memory-protocol.md)（会话记忆协议：`repotory_handoff` / `repotory_context` 是那对端点）、[ADR-0010](0010-session-auto-capture.md)（会话结束自动捕捉，**默认关**）、[ADR-0009](0009-ide-presence.md)（一键注册 `repotory-init`；本篇补它缺的那一半——注册了工具，没有注册指令）、[ADR-0040](0040-unscoped-ask-orientation-and-handoff-evidence.md)（交接记录进证据层）

## 背景

TODO 2.1 的时间盒判据（连续 2 周 ≥ 5 条 handoff，2026-10-28 起算）在窗口内取到 **0 条**。
2026-11-03 逐条核实产地，结论是**不是「用户没干活」**：窗口内 `~/.claude/projects` 有 6 个会话文件，
handoff 一条没多。三条可能的产地全断：

| 产地 | 实测现状 | 断在哪 |
|---|---|---|
| ① agent 自己调 `repotory_handoff` | 全局 `~/.claude.json` 注册了 `repotory-mcp`，**工具可用** | **没有任何东西告诉 agent「收尾要调它」** |
| ② Claude Code SessionEnd hook | 全仓 `find`：**一个项目的 `.claude/hooks/repotory-handoff.sh` 都不存在** | 从没装过；且 hook 三层吞失败，**装了也可能静默 0 条** |
| ③ 桌面端手动「写一条交接」 | live 库 `claude_session_capture=1`（开着） | `handoff_capture.go:28` 明写 *"triggered only explicitly (no startup/hook automation on this path)"*——这个开关只是**允许**，不产生自动触发 |

存量的 4 条也对上了：3 条真交接 `source=mcp`、全在 2026-10-26（那次审计里 agent 被**明确要求**调工具写的），
1 条 10-18 的 `Test Handoff Note` 是手工测试。**「被明确要求的那一次就产生了，没被要求时一条没有」**——
这句话把根因指得很死：**工具在场，指令缺席**。

而这两句指令**早就写成了文档**：[本地 Agent 配方](../features/local-agent-recipe.md) 第 83-84 行
（「开始工作前调用 `repotory_context`… 结束工作前调用 `repotory_handoff`…」），
同一页的「边界」一节还自己承认了缺口：

> **不保证模型会主动调工具**。小模型需要把指令写死（「先调 `repotory_context`」）；把它写进客户端提示词或 hook 才是可靠做法。

**产品把这个判断写进了手册，却没有把它做成任何一个能点的东西。** 于是每个用户都得自己读到那页文档、
自己复制那两句、自己决定写进哪个文件——这就是 0 条的机制，而不是态度的问题。

### 为什么补的不是 hook

② 那条路径（SessionEnd hook）已经落码，但它有三个本篇不想继承的性质：

1. **客户端专有**：只有 Claude Code 有 hook 机制。Cursor / opencode / Windsurf / 任何别的 MCP 客户端**接不上**，
   而「跨 agent 的持久记忆层」正是本产品的定位（ADR-0007、Sprint 4.4 的第一屏口径）。
2. **失败静默**：hook 脚本的设计是「绝不打断会话」——`command -v claude || exit 0`、`[ -f .mcp.json ] || exit 0`、
   末尾 `>/dev/null 2>&1 || true`。三层吞掉一切失败。**「装了但没生效」与「没装」在观测上一模一样。**
3. **它会额外起一个 Claude Code 进程**，为记一条笔记再跑一轮模型，这个成本不该由一次会话结束来付。

指令文件那条路**同时治这三条**：协议中立（任何会读 `AGENTS.md` 的 agent 都吃到）、
写进去就看得见（是一条 diff，不是一个进程）、由**当前那个正在干活的 agent** 自己写，不多花一次推理。

## 决策

**① 产品把那两句指令写进项目的指令文件，以「受管区块」的形式。**
`agentinit` 新增一步：往 `AGENTS.md` 与 `CLAUDE.md` 各写一段被
`<!-- repotory:begin -->` / `<!-- repotory:end -->` 夹住的文本。
**已有该区块 → 原地替换；没有 → 追加到文件末尾；文件不存在 → 创建并报 `created`。**
区块之外的任何用户内容**一律不动**——这是「写进用户自己的文件」这件事唯一可接受的形态：
我们拥有那一段，不拥有那个文件。

**② 两个文件都写，而不是按客户端选文件。**
`AGENTS.md` 是跨 agent 的事实标准（Codex / opencode / Cursor / Amp… 都读），`CLAUDE.md` 是 Claude Code 专属。
一份没人读的指令文件是**惰性的**（它不产生任何行为，也不产生任何错误），
所以「写两份」的代价只是多一个文件，而「按客户端选」的代价是**用户换了个 agent 就悄悄失效**。
**报告逐文件列出**（`created` / `updated` / `unchanged`），让用户看见写了什么。

**③ 区块文本是给模型读的，用英文，且明确写清「不该记什么」。**
理由与 hook 那句 prompt 一致（先例已定）：**这是机器指令文本，不是界面文案**，
不进 `web/src/locales`，因此不受双语镜像与「英文 locale 不留中文」那两道门管辖——
界面文案照旧双语同改，指令文本只有一份、两份实现共用。
区块内容三句：开局 `repotory_context`（并说明它**以最新交接置顶**）、收尾 `repotory_handoff`
（点名 `gotchas` / `decisions` / `next_steps` 三个字段）、以及**一条反向判据**：
「不要记 commit log 已经显示的东西；记下一次会话否则会重新踩一遍的东西」。
第三句是本篇的实质：把上一轮讨论出来的「什么场景该产生 handoff」编码进指令本身，
否则指令只会让 agent 记活动流水，那正是 M6/M7 上游枯竭的形状（`idea` 0 条 / `manual` 1 条）。

**④ 卸载 = 删掉那两行标记之间的内容，不提供 `--remove` 开关。**
与 hook 脚本同一姿态（它的注释就写着「删掉这个文件与对应 settings 条目即卸载」）。
理由：受管区块对用户是**可读可改的文本**，不是一个需要产品代管的隐藏状态；
加一条 remove 路径等于再加一套必须与两份实现对齐的字节契约，收益不成比例。

**⑤ 这一步与 hook 是两个独立开关，默认都开。**
「接入 AI 客户端」设置页新增一个勾选（与既有的「同时安装 SessionEnd 钩子」并列）。
**两条路径不互斥、不重复计费**：hook 兜住「用户忘了收尾」，指令文件兜住「不用 Claude Code 的用户」。
两者都写进同一条 `repotory_handoff` 落库路径（`CreateHandoffNote`），
所以**不会出现两种形状不同的交接记录**。

**⑥ 不动 schema / 不新增 MCP 工具 / 不新增绑定 / 不改 `repotory_handoff` 与 `repotory_context` 的协议面 /
不改 `claude_session_capture` 的默认关语义 / 不动 ADR-0007·0010·0026 的任何决策。**
`SetupAgentClients` 的绑定**加一个参数**而不是新增一个绑定：它仍是同一次「接入」动作，
拆成两个按钮会让用户以为要做两次决定。前端 transport 的 arity 与后端同步改
（`internal/app/frontend_contract_test.go` 钉的是方法名存在性，本次不动方法名；
`internal/httpapi/rpc.go` 按位置映射参数，加一个位置即可）。

**⑦ 两份实现仍由同一份 golden fixture 钉住。**
`internal/agentinit` 与 `scripts/repotory-init/index.mjs` 必须产出**逐字节相同**的文件，
`internal/agentinit/testdata/contract.json` 新增这两个文件的期望内容，
`scripts/check-agent-init-contract.mjs` 的 `PRODUCED` 清单同步扩，
`ide/vscode/repotory-init/index.mjs` 那份副本由 `sync-init.mjs` 重新生成。
**区块文本的正确性靠测试钉（幂等 / 保留用户内容 / 缺失文件创建），不靠散文描述。**

## 后果

- **正面**：一次点击之后，`AGENTS.md` 里就有了那两句指令，**任何读它的 agent 都会吃到**——
  这是本产品「协议中立」定位第一次被做成一个按钮，而不是手册里的一段话。
  时间盒判据（2.1）从「等用户读文档」变成「等用户点按钮」，缺的那一条供给侧才可能真的动。
- **代价（接受）**：
  - **往用户仓库写了两个文件**（或改动两个已有文件）。这是 setup 动作、有勾选、有逐文件报告、有 `.repotory-init.bak` 备份，
    但它确实会进用户的 `git status`——**手册与区块注释都要说明这一点**，第一次点的人会看到多出来一条 diff。
  - **模型仍可能不服从指令**。这一条与 hook 那条路相比只是把「不保证」从「没有触发器」推进到「有触发器但不保证执行」。
    诚实的说法是：**指令文件让「记」变成一个有 80% 概率发生的事件，0 概率是它当前的下限**。
    因此 ② 那条 hook 路径**不废弃**，两条并存。
  - **`CLAUDE.md` 会与本仓库自己的 `AGENTS.md` 同族膨胀**。区块被刻意压在三句话以内，
    加长它之前先量一次它对 `repotory_context` 预算的影响。
  - **时间盒判据的读数会变难归因**：若窗口内开始出现 handoff，可能是指令文件的功劳、也可能是 hook 的、
    也可能只是用户点了「写一条交接」。**`source` 字段分得开**（`mcp` / `desktop`），
    但「同一条 prompt 让 agent 主动调」这件事与「hook 让 agent 调」在 `source=mcp` 里是同一个值——
    要分得更细需要给 handoff 加来源子类型，**本篇不做**（不新增协议字段）。
- **不能假装解决了的**：M6/M7 的上游枯竭（`knowledge` 38 / `idea` 0 / `manual` 1）不是靠这段指令治的。
  它治的是**「会话收尾时认知状态没人接住」**这一条，不是「知识类笔记从哪来」。

## 落地清单

| 面 | 文件 |
|---|---|
| 区块文本 + upsert 纯函数 | `internal/agentinit/instructions.go`（新） |
| Go 侧接线 | `internal/agentinit/agentinit.go`（`Options.WithInstructions` / `Report.Instructions`） |
| Go 侧回归 | `internal/agentinit/instructions_test.go`（新） + `agentinit_test.go`（契约 fixture 生成扩两文件） |
| golden fixture | `internal/agentinit/testdata/contract.json`（`-update` 重新生成） |
| 终端孪生 | `scripts/repotory-init/index.mjs`（`--with-instructions`）+ `ide/vscode/repotory-init/index.mjs`（`sync-init.mjs` 生成） |
| 契约门 | `scripts/check-agent-init-contract.mjs`（`PRODUCED` 扩两项） |
| service / 绑定 | `internal/service/agent_clients.go`、`internal/app/bindings.go`、`internal/app/surface.go` 理由文案 |
| 前端 | `web/src/api/endpoints.ts`、`web/src/api/types.ts`、`web/src/pages/settings/ActionsTab.tsx` + 其测试 |
| 双语词条 | `web/src/locales/{zh-CN,en}/common.json`（`settings.agentClientsInstructions` 等，**两份同改**） |
| 手册 | `docs/features/local-agent-recipe.md`、`docs/features/ai-integration.md`（中英双份） |
| TODO | 2.1 那条补一句「供给侧已接上，窗口内读数到 11-11 复看」 |

## 落地记录（2026-11-03）

**已落码（2026-11-03）**：受管区块由新文件 `internal/agentinit/instructions.go` 承载——`upsertInstructions` 是纯函数（三种形状：已有区块原地替换 / 有内容则追加 / 无文件则整份为区块；半开标记只追加不吞后续内容），`installInstructions` 逐文件写并各自报告状态，一个文件失败不挡另一个。`Options.WithInstructions` → `Report.Instructions`，经 `service.SetupAgentClients` 的第 4 个参数到绑定与前端 transport（`args: [projectId, clients, withHook, withInstructions]`），设置页新增一个勾选（默认开），报告逐文件列出「新建 / 已改 / 未变」+ 备份路径。

**两份实现逐字节一致**（本仓最硬的那道跨语言门）：`scripts/repotory-init/index.mjs` 加 `--with-instructions`，`ide/vscode/repotory-init/index.mjs` 由 `sync-init.mjs` 重新生成，`testdata/contract.json` 用 `go test ./internal/agentinit -run TestContractFixture -update` 重新生成（含 `AGENTS.md` / `CLAUDE.md` 两个新键），`scripts/check-agent-init-contract.mjs` 的 `PRODUCED` 清单同步扩两项、并按**精确文本**比对这两个 Markdown 文件（同 hook 脚本那条，不像 JSON 那样按语义比）。契约脚本改前红（fixture 未更新）、改后绿：`agent-init contract OK: the CLI script and the Go implementation write the same files`。

**回归**：`internal/agentinit/instructions_test.go` 12 例（幂等、保留标记外内容、半开标记、缺失文件创建、备份、两文件都写、两个开关互不越界、区块带反向判据）；`internal/service/agent_clients_test.go` 加 2 例（走真实 `SetupAgentClients` 路径 + 二次点击必须 `unchanged`；hook 与 instructions 各自独立）；前端 `ActionsTab.test.tsx` 加 2 例（勾选独立成一个参数、逐文件报告且 `updated` 与 `created` 可分辨）。**全量**：`go test ./cmd/... ./internal/...` 全绿、`go vet` 干净、`npm test` 49 文件 **415 例**通过、`npm run lint`（0 error 0 warning）、`npm run build`、`check-i18n-keys`（920 静态 key，两份同改）/ `check-docs-mirror` / `check-version-drift`（ADR 43 份中英索引对齐）全绿。

**CLI 端到端实测**（`/tmp` 隔离目录，一个写着 `# My project\n\nBuild with make.\n` 的 `AGENTS.md` + 一个假 `repotory-mcp`）：首跑报 `AGENTS.md: updated (backup: AGENTS.md.repotory-init.bak)` / `CLAUDE.md: created`；用户原文仍在文件顶部；二跑报 `already contains the repotory block, no change`，且 `cmp` 证明**两次产出的字节完全相同**（幂等不是「看起来没变」而是 sha 一致）；备份文件内容与原文逐字节相等。

**记一条与本篇无关的观察**：全量 `npm test` 首跑出现过一次 `NoteSection.test.tsx > keeps the stored note when the tidy call fails` 单例红灯，**单文件复跑两次全绿、全量复跑亦绿**——形状与本仓 2026-11-03 已修的那族「等 mock 被调用当成了等数据落地」竞态一致（ADR-0026 记的就是这件事），本篇未触碰该文件，**不在本轮处理**，记在这里以免下次被当成新问题重查。

**已知代价（落地时确认）**：区块文本会进用户的 `git status`——已在区块末尾自述「如何删掉它」并写明这一点；`CLAUDE.md` 与 `AGENTS.md` 内容重复是决策② 的故意选择（宁可重复，不要惰性）；模型服从仍不保证，因此 hook 那条路**不废弃**。
