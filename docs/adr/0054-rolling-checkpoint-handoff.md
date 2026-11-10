# ADR-0054: Handoff 升级为滚动检查点——应对 credit 耗尽与非正常终止

状态：**Accepted（2026-11-10，用户拍定「请开始生成 ADR 和代码改动」并同日落地；生效前提与残余断链见文末「落地记录」）**
日期：2026-11-10
关联：[ADR-0010](0010-claude-session-capture.md)（SessionEnd hook 与 transcript 捕捉的隐私门）、[ADR-0043](0043-agent-instructions.md)（指令块教 agent 读写记忆层）、[ADR-0040](0040-handoff-as-channel.md)（handoff 作为独立证据通道）、[ADR-0048](0048-core-contract-version.md)（MCP 工具面变更要涨契约版本）
起始 schema：v30 → **v30（无 schema 变更）**；`CoreVersion` 1.2.0 → **1.3.0**（`repotory_handoff` 新增可选字段 `milestone`）

## 背景

### 问题一：handoff 功能空转

2026-11-10 排查发现：Cinder 的 handoff 功能代码完整（MCP 工具、SessionEnd hook、证据注入、UI 全部实现并有单测覆盖），但实际运行中**已空转 15 天**——`repotory.db` 里最后一条 handoff 笔记停在 2026-10-26，此后 189 个 commit（v2.15.0 → v2.33.5）再无一条交接记录。三个断点同时存在：

1. **SessionEnd hook 未安装**：`cinder/.claude/settings.json` 不存在；全局 `~/.claude/settings.json` 的 SessionEnd hook 是 iTerm2 的 `it2 handle-agent-hook`，不是 Cinder 的 repotory-capture
2. **指令块未写入**：`cinder/AGENTS.md` 里没有 "Before you finish, call repotory_handoff" 的指令块（`SetupAgentClients` 本该写入的）
3. **MCP 路径断**：`cinder/.mcp.json` 指向 `build/bin/repotory-mcp`，该二进制不存在

即使这三处都修好，还有一个更深的设计缺陷。

### 问题二：credit 耗尽场景下 handoff 必然丢失

当前 handoff 写侧有两条路径：

| 路径 | 机制 | credit 依赖 |
|---|---|---|
| Agent 主动调用 `repotory_handoff` | 需要 agent 在会话结束前"想起来"去写 | 消耗 agent 的推理 credit |
| SessionEnd hook → `claude -p` | 会话结束后再跑一次 model 调用来总结 transcript | **消耗 credit**（与主会话同一池） |

当 agent 因 credit 耗尽被硬杀时：

- **主动路径失效**：agent 没有机会做最后一次调用
- **hook 路径也失效**：`claude -p` 需要调 model，credit 已耗尽，hook 的总结调用同样失败
- **结果**：agent 干到 80% 断气了，什么都没留下。下一个 agent 接手时只能从 git commit 反推意图，中间决策和未完成事项全部丢失

而 `repotory-capture` 二进制（`handoffFromSession` → `CreateHandoffNote`）全程不调 model——它直接从 transcript 解析最后一条 assistant 消息、工具列表、git 分支写成 handoff。**这是一个零 credit 的降级路径，但当前 hook 脚本完全没用它。**

### 问题三：handoff 是"会话末一次性快照"，不是"滚动检查点"

当前设计假设 agent 会正常结束会话并在结束时写一条完整 handoff。但 agent 的终止是随机的——credit 耗尽、超时、崩溃、用户强制中断。正确的模型应该是：handoff 在整个会话过程中持续更新，每次完成一个有意义的工作单元就写一条检查点，形成滚动快照。这样无论会话在哪个点被掐断，最后一条 handoff 都是"当前状态"。

## 决策

### D1 Handoff 双类型：milestone（滚动检查点）+ final（会话总结）

`repotory_handoff` MCP 工具新增可选参数 `milestone`（bool，默认 false）：

| 类型 | `milestone` | 标题 | tag | Next Steps 语义 | 写入时机 |
|---|---|---|---|---|---|
| 里程碑检查点 | `true` | `Milestone Handoff` | `handoff, milestone` | 当前未完成的工作（"接下来要做的 X"） | 每完成一个有意义的工作单元 |
| 会话总结 | `false`（默认） | `Session Handoff` | `handoff` | 下次会话的完整计划 | 会话正常结束前 |

**为什么用 tag 而不是新 kind**：`project_notes.kind` 只有 `other/knowledge/log` 三值，`handoff` 笔记当前 kind=knowledge、tag=handoff。增加 tag `milestone` 让检索和过滤走现有 tag 通道（`',' || REPLACE(LOWER(tags), ' ', '') || ',' LIKE '%,handoff,%'`），不引入新的分类维度。读取侧 `ListRecentHandoffNotes` 按 `updated_at DESC` 排序，最新置顶——milestone 写得更频繁，最新的自然就是当前状态，不需要特别区分。

**为什么 `milestone` 是可选参数而不是新工具**：按 ADR-0048 的契约规则，新增可选字段走 minor bump，不破坏现有调用方。所有现有调用（包括 `claude -p` hook 路径）不传 `milestone`，行为不变。

### D2 指令块改为滚动检查点语义

`instructions.go` / `index.mjs` 的 `instructionsBlockBody` 从"Before you finish, call repotory_handoff"改为三条规则：

1. **开始工作前**调 `repotory_context` 读上一session的 handoff（不变）
2. **每完成一个有意义的工作单元**（一个功能、一个修复、一组测试），调 `repotory_handoff(milestone=true)` 记录当前进度和未完成事项——这是滚动检查点
3. **会话结束前**调 `repotory_handoff` 写最终总结（`milestone=false`）
4. **credit 不足时**优先写 `milestone=true` 的检查点再继续

第三条（credit 预警）依赖 agent harness 暴露 credit 状态，不是所有 harness 都支持，所以它是补充指令而非强制。

### D3 SessionEnd hook 零 credit 优先

`hookScript` 改为两级路径：

```
主路径：repotory-capture（读 transcript 直接落盘，零 credit）
  ↓ 失败（二进制不在 PATH / 配置未开 / 项目不匹配）
降级路径：claude -p（调 model 写结构化 handoff，花 credit）
```

`repotory-capture` 全程不调 model（`handoffFromSession` 只取 transcript 解析结果），所以即使 credit 已耗尽也能工作。`claude -p` 降级路径保留，因为它能写更结构化的 handoff（总结、决策、gotchas 都更完整），在 credit 充足时效果更好。

### D4 读取侧适配：latest milestone 优先于 final

`evidenceHandoffs`（wiki_evidence.go）当前按 `updated_at DESC` 取最新 3 条置顶。milestone 写得更频繁后，最新的自然优先。但内容渲染需要适配：

- 最新的一条如果是 milestone，渲染为"当前状态"（`clipRunes(content, handoffEvidenceRunes)`）
- 旧的 collapse 为一行时间线（`oneLineSummary`），标注 `[milestone]` 或 `[final]`
- `repotory_context` 的 `BuildProjectContext` 同步适配

### D5 契约与版本

- `CoreVersion` 1.2.0 → 1.3.0（`repotory_handoff` 新增可选字段 `milestone`，按 ADR-0048 属 minor bump）
- `wantCoreVersion` 在 `cmd/mcp/main_test.go` 同步更新
- Schema 不变（v30）——milestone 是 tag + bool 字段，不落 DB 列
- Go 和 mjs 两份实现同步改，`testdata/contract.json` 同步更新 pin 字节

## 代价

- **指令块变长**：从 3 条变 4 条，每个 agent 每次会话都读，多占几行 context。控制在 15 行以内。
- **milestone 笔记可能过多**：一次会话可能产生 3-5 条 milestone。`handoffEvidenceLimit=3` 限制了读取侧最多取 3 条，且 `ListRecentHandoffs` 的 `defaultRecentHandoffLimit=10` 限制了 UI ring 的条目数。超出窗口的旧 milestone 不会被读取，但会留在 DB 里。
- **`repotory-capture` 的局限**：它只能取 transcript 的最后一条 assistant 消息 + 工具列表，无法生成结构化的 decisions/gotchas。这是零 credit 的代价——结构化程度不如 `claude -p` 路径。降级路径保留就是为了在 credit 充足时补上这个差距。
- **credit 预警不是通用能力**：D2 的 credit 预警指令依赖 harness 暴露 credit 状态，Workbuddy/Qoder/Deepseek Harness 不一定支持。不暴露时该指令不生效，但不影响其他三条。

## 落地记录（实现偏差）

**同日全部落地（2026-11-10）**：

- **D1**：`internal/service/handoff.go` 新增 `milestoneTag`、`HandoffInput.Milestone`、`handoffTitle()`（`Session Handoff` / `Milestone Handoff` 两名）、`HandoffResult.Milestone`；正文头部标注 `[Rolling checkpoint — not a session summary]`；`cmd/mcp/tools_context.go` 的 schema / 描述 / handler 贯通 `milestone`。老调用零改动。
- **D2**：`internal/agentinit/instructions.go` 与 `scripts/repotory-init/index.mjs`（SSOT）指令块改为四条规则 + 保留的负判据；`testdata/contract.json` 以 `go test -run TestContractFixture -update` 重生成，`check-agent-init-contract.mjs` 与 `sync-init.mjs` 双绿。
- **D3**：`hookScript` 主路径改为 `if command -v repotory-capture ... && repotory-capture ...; then exit 0; fi` 零 credit 兜底，降级仍走 `claude -p`。
- **D4**：**比方案少做了一步**——`oneLineSummary` 的 `[milestone]/[final]` 文字标注未单独落地：标题本身已写 `Milestone Handoff` / `Session Handoff`（`ListRecentHandoffNotes` 按 `updated_at DESC`、最新全文置顶），再加一层标记属重复编码；读取路径零改动（已实测排序行为）。
- **D5**：`CoreVersion` 1.2.0 → 1.3.0 两处钉版同步；schema 停在 v30；CHANGELOG `[Unreleased]`、中英 index 登记与英文 ADR twin 同批。
- **测试**：service 层两条新测（类型区分 + 上下文置顶顺序）、MCP 层一条（`milestone` 端到端）；`go test ./...`、docs-mirror、i18n-keys、version-drift 全绿。
- **桌面 UI 侧未动**：`HandoffComposer` 仍只写 final 类型——milestone 语义目前只经由 MCP/指令块/hook 生效，桌面手动补录入口留待后续（不影响本 ADR 的协议闭环）。

**生效前提（残余断链，部署动作而非代码缺陷）**：

1. hook 与指令块是**安装动作**——要在具体项目里跑 `repotory-init`（桌面端 `SetupAgentClients` 的 CLI 双胞胎）才会落盘；用户的 `Script`/`cinder` 工作区当前仍未安装（`.claude/settings.json` 不存在、AGENTS.md 无指令块，正是排查发现的现状）。
2. 零 credit 主路径受 ADR-0010 的 `claude_session_capture` 默认关约束——未开启时 hook 走降级，属预期门。
3. `build/bin/repotory-mcp` 不存在导致 `.mcp.json` 指向空位：那是构建产物路径问题，不在本 ADR 范围；本次只动协议面。

## 关联文档

- [ADR-0010: Claude session capture](0010-claude-session-capture.md)——SessionEnd hook 与 transcript 捕捉的隐私门
- [ADR-0043: Agent instructions](0043-agent-instructions.md)——指令块教 agent 读写记忆层
- [ADR-0040: Handoff as channel](0040-handoff-as-channel.md)——handoff 作为独立证据通道
- [ADR-0048: Core contract version](0048-core-contract-version.md)——MCP 工具面变更要涨契约版本
