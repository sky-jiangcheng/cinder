# Cinder 差异化定位 —— 对比 TencentDB Agent Memory

> 生成日期：2026-10-13。对比对象：[TencentDB Agent Memory](https://github.com/TencentCloud/TencentDB-Agent-Memory)（腾讯云开源，20k+ star，v2.0.0，MIT）。
> 本文承接 [positioning-brief.md](positioning-brief.md) 的定位口径，回答一个问题：**当市场上出现一个热门的「agent 记忆」项目时，Cinder 的差异化价值是什么。**
> 仅中文（研究性文档，不进手册镜像，与 product-review/ 同级）。

## 一、一句话结论

**两者不在同一层，不构成竞品。** TencentDB Agent Memory 是 **agent 运行时的上下文引擎**（插件嵌入 OpenClaw/Hermes，管理单个 agent 的任务上下文与用户画像）；Cinder 是 **跨 agent、跨会话、人机共用的项目知识库**。前者优化"一个 agent 这一轮干得省不省 token"，后者解决"下一个 agent（哪怕换了工具）还记不记得这个项目"。

可以把它理解为上下两层：

```
┌────────────────────────────────────────────┐
│  运行时上下文层（对方的主场）                │
│  任务内的 token 卸载、Mermaid 符号化、       │
│  L0→L3 用户画像提炼                          │
├────────────────────────────────────────────┤
│  项目知识层（Cinder 的主场）               │
│  跨工具交接、仓库挖掘、笔记/图谱、           │
│  人可审阅的版本与审计                        │
└────────────────────────────────────────────┘
```

## 二、维度对比

| 维度 | TencentDB Agent Memory | Cinder |
|---|---|---|
| 记忆主体 | Agent 任务状态 + 用户画像（agent-centric） | 代码项目的知识与判断（project-centric） |
| 核心问题 | 单 agent 长任务 token 膨胀、跨会话个性化 | 跨工具上下文蒸发：Claude 写的交接，Cursor 直接读 |
| 记忆形成 | LLM 全自动提炼：L0 对话 → L1 原子 → L2 场景 → L3 画像；工具日志 → Mermaid 符号 | 结构化录入 + 仓库挖掘：handoff 模板、笔记、todo、扫描、知识图谱（关系八型/社区摘要） |
| 运行依赖 | Node ≥22.16 + 常驻 Gateway + **必须 LLM API key** | 单 Go 二进制、零 CGO、**离线零 LLM**（语义检索可选、默认关，过 `abeval` 门才开） |
| 接入面 | OpenClaw / Hermes 插件（纯 agent 侧） | MCP 工具集 + 桌面 GUI + VS Code 扩展 + llms.txt，**人可直接读写** |
| 存储 | DB（底层证据）+ Markdown 文件（顶层结构）双层 | 单 SQLite（FTS5 + sqlite-vec）单一事实源，一次备份迁移全部带走 |
| 可信度建设 | 公开 benchmark（token −61.38%、通过率 +51.52%、PersonaMem 48→76%） | 版本快照、`repotory_integrity` 六项审计、可逆 schema 迁移、ADR 全量留档（见 [adr/index.md](adr/index.md)）、评测门控 |
| 生态规模 | 大厂背书、20k+ star、Discord、npm 分发 | 小众单人维护，有 Homebrew/Scoop 发布链 |

## 三、Cinder 的差异化支柱（对方未覆盖）

1. **人是第一用户**。对方是纯 agent 侧存储——顶层 Markdown 人类可读，但没有产品界面。Cinder 有桌面 GUI、块编辑器、行级 diff、一键恢复：知识是人能审、能改、能回滚的。这条不可让渡：**人不信任的记忆，agent 用了也不敢全信**。
2. **项目维度的结构化知识**。技术栈（20+ manifest）、依赖树、贡献者、commit 流、monorepo 分组、页面版本快照——对方完全没有 repo 挖掘这条线。agent 记得"用户喜欢什么"，Cinder 记得"这个项目为什么长成这样"。
3. **零依赖、可迁移、离线**。对方要 Node 22、Gateway 进程、LLM key 才能工作；Cinder 离线单文件跑起来，数据是自己手里一个 db 文件，不绑任何云或模型供应商。
4. **克制的工程纪律**。语义检索默认关、等标注集过 A/B 门；分析工具走一次性副本；冻结标识保护用户数据迁移史；范围冻结防外部契约破坏。这种"不轻易打开功能"的纪律在对方 README 中看不到，但恰恰是**知识库产品可长期信任的前提**（错了能回滚、索引漂移能审计发现）。
5. **跨 agent 交接是显式契约**。`repotory_handoff` 的五段结构（summary/changes/decisions/gotchas/next_steps）是稳定的、可检索的、跨工具的；对方的记忆提取是全自动黑盒，画像会漂移，且只在自己的插件体系内生效。
6. **协议中立，不绑 harness**。任何会讲 MCP 的编码 agent（Claude Code / Cursor / Codex / Copilot 等）读写的是**同一份**记忆——记忆层不是某个插件的私有存储，而是这些 agent 共享的一层。**不需要账号、不需要云端**：整份记忆是本机一个 SQLite 文件。这一条是 2026-11-02 补上的（此前文档只讲「本地优先」，没讲「agent 中立」，而这正是各家 agent / IDE 自己的会话记录**够不着**的那一层——VS Code 的 session 索引扩展至今读不到，见第七节）。

## 四、对方更强、值得借鉴（不改变定位）

- **量化评测是最大差距**。他们拿 WideSearch/SWE-bench/PersonaMem 说话；我们的 `abeval` delta 还卡在 Phase 0（待真实标注 query 集）。**知识库产品同样需要"检索质量可量化"的门**，应优先推进。
- **运行时 token 优化**（Mermaid canvas 卸载 + `node_id` 下钻）是完全没做的方向。`repotory_context` 目前是一次性注入全量，未来若上下文预算紧张，可借鉴"顶层摘要 + node_id 下钻"的渐进披露，而非照搬其架构。
- **自动提炼的分层（L1/L2）** 可作为我们 handoff/笔记沉淀的上游候选：用对方的自动提炼喂我们的结构化交接，人再审。

## 五、对路线的具体影响

| 事项 | 结论 |
|---|---|
| Hermes sessions 导入（TODO 现有项） | **优先级提升**。对方插件原生支持 Hermes，落地后会在本机产生格式明确的会话数据——届时以他们的存储格式作真实样例，比猜活格式靠谱。执行计划见 [outputs/process-logs/Cinder-Hermes导入与Phase0标注执行计划-2026-10-13.md](../outputs/process-logs/Cinder-Hermes导入与Phase0标注执行计划-2026-10-13.md)。 |
| M3 语义检索 Phase 0 标注 | **优先级提升**。竞品用 benchmark 立信，我们至少要有 `abeval` delta 为正的公开证据。执行计划见 [outputs/process-logs/Cinder-Hermes导入与Phase0标注执行计划-2026-10-13.md](../outputs/process-logs/Cinder-Hermes导入与Phase0标注执行计划-2026-10-13.md)。 |
| ADR-0014/0015（LLM wiki 编译） | 维持克制。全自动 LLM 提炼是对方的主场优势，我们在没有评测门之前不应正面跟随；先把手写知识+图谱做扎实。 |
| 是否做"用户画像/Persona" | **不做**。画像属于运行时个性化层，与"项目知识库"定位冲突；坚持 project-centric。 |


## 七、另一个易混淆的邻居：VS Code 的 session / memory（2026-11-02 补）

> 完整调研见 [VS Code session 保存 / memory / handoff 与 Cinder handoff 的竞争关系](product-review/2026-11-02-vscode-session-memory.md)（VS Code 1.144 官方文档 2026-10-28）。本节只记**定位结论**。

**它不是竞品，是邻居——而且是另一种邻居。** TencentDB Agent Memory 是**另一个产品**（可安装、可选型）；VS Code 的 session 能力是 **IDE 自带、默认开启、账号绑定**的产品级功能。后者的对手是「另一个 agent 记忆产品」，而**Cinder 连这一层的赛道都不在**。

**VS Code 已经内建了什么**（本地 SQLite session 存储默认开 / session sync 实验性默认关、需 GitHub 账号 / Copilot Memory 云端、**28 天未用自动删** / `/chronicle:*` 把近期会话按分支与仓库分组）。**它在竞争「session 存储与检索」这一层，不在竞争「handoff 协议」这一层。**

**关键的一处同名不同义**：VS Code 的 "Hand off a session" 是**运行时换 harness / 执行环境 / 角色并带走完整对话历史**；Cinder 的 `repotory_handoff` 是**持久化的交接记录**（一条 tag `handoff` 的知识笔记，下次由 `repotory_context` 读回）。已在 [ADR-0007](adr/0007-session-memory-protocol.md) 加注澄清，并落进 MCP 工具描述、两个 IDE 面板与桌面端文案——**协议名与工具名一律不改**（改名会破坏既有 agent 集成与自动捕捉 hook），消歧落在描述与措辞上。

**为什么不打这场仗**：对手是 IDE 自带、默认开启、账号绑定的功能；扩展**读不到** VS Code 的 session（microsoft/vscode#318855 仍是提议，私有存储格式已在生产中弄坏过扩展）。**打不赢，也不该打。**

**为什么它越强，我们的位置越清晰**：因为它把「会话记录」定义成了 **IDE 的内部实现细节**，而 Cinder 把它定义成**一份可被任意 agent 读取的知识资产**。区别在四个词上——**跨 agent**（VS Code 的能力全部锚在 Copilot 生态，Claude Code / Cursor 的用户在 VS Code 里得不到 Copilot Memory）、**协议中立**、**无账号**、**人在闸门**（Copilot Memory 由 agent 自动存、upvote/downvote 维护，28 天不用就删；Cinder 的知识有 project_id、有 kind、有 source、有人工审核）。

**要真被重估的信号只有一个**：`/chronicle:standup` 若进一步做到「每个会话给出一段结构化总结」，那它就在向 Cinder 的形态靠拢——**那时才是需要认真重估的信号**。在此之前，唯一该做的是**别把词说混**。

## 六、边界条件：什么时候它才真变成竞品

以下任一发生时，本文档需要重写：

1. 对方开始支持 **MCP 通用接入**（不绑 OpenClaw/Hermes），把记忆开放给任意 coding agent；
2. 对方加入 **多项目/仓库维度的结构化挖掘**（技术栈、依赖、commit 流）；
3. 对方提供 **人可操作的产品界面**（GUI/CLI 之外的记忆审阅与回滚）。

（VS Code 的 session 能力**不在**这张触发清单里：它不是竞品，是邻居，见第七节。它的重估信号另记——`/chronicle:*` 开始输出结构化总结。）

在此之前，对它的正确姿态是：**分层互补 + 借鉴其评测方法 + 等它的 Hermes 数据格式成为我们的导入样例**。
