# ADR-0040: 无作用域问答的取向层，与交接记录进证据层

- 状态：Accepted（2026-11-02，用户就三个选项逐项拍定）
- 日期：2026-11-02
- 关联：[ADR-0014](0014-llm-wiki-knowledge-compiler.md)（分层上下文 L3→L0 与取证层）、[ADR-0031](0031-global-page-scope.md)（**L3 不做自动兜底**，本篇不改这条）、[ADR-0015](0015-w3-minimal-compile-loop.md)（编译闸门）、[ADR-0033](0033-ai-chat-endpoint.md)、[ADR-0007](0007-session-memory-protocol.md)（会话记忆协议）、[ADR-0025](0025-backup-recovery.md)

## 背景

起因是一句用户提问：**「为什么配置了外部的 AI，我们的 Agent 却完全读不到本地的项目呢」**。逐层追下去得到三个**互相独立**的断点——前两个在代码里，第三个在分发状态里。三者叠加，才凑出「配了 AI 却读不到本地项目」这个现象。

### 断点一：默认作用域下，上下文整段消失

「问」面板的作用域下拉默认是「全库知识与全部项目」，对应后端 `projectID = 0`。而 `internal/service/wiki_evidence.go` 的 `evidenceMessages` 把整段上下文装配包在 `if projectID > 0` 里面：

```go
if projectID > 0 {
    lay := s.BuildLayeredMemory(projectID, "", LayerBudget{}, false)
    ...
} else if ev != "" {
    messages = append(messages, chatMessage{Role: "system", Content: ev})
}
```

于是默认作用域下，模型的输入只有「检索到的页/笔记 + 问题」，**没有任何取向**。它不知道用户有哪些项目，不知道这个库的形状，只能对着检索到的几段文本硬答。

实测（v2.24.0 真库，31 项目）：`LayeredProjectContext(0, "")` → **chars=0、blocks=[]**；`LayeredProjectContext(124, "")` → 1000 字符（Project / Path / Repositories / Languages / Top contributors）。同一个接口，两个作用域差一个数量级。

而 **L3（`layerGlobalProfile`，`context_layers.go`）根本不依赖 projectID**——`BuildLayeredMemory` 里它排在 `if projectID > 0` **之外**。这一层被一个与它无关的条件挡掉了。

### 断点二：交接记录没有 AI 入口

交接记录是带 `handoff` 标签的笔记（`IsHandoffNote`，`context.go`）。唯一能让 agent 看到它的入口是 MCP 的 `repotory_context`（`cmd/mcp/tools_context.go` → `BuildProjectContext`，`context.go`）——那里把**最新一条交接记录全文**渲染出来，旧的收成行时间线。`context.go` 自己的注释说得很直白：「截断的退出记录比没有还糟，下一轮会话会基于半份摘要行动」。

AI「问」路径不走那条路。`GatherEvidence`（`wiki_evidence.go`）只检索**知识页 + 笔记**（FTS / 语义），没有「按时间 / 按类型」的通道。而交接记录的标题是 `Session Handoff 2026-10-26 18:34`（英文），用户用中文问「最近有哪些会话交接记录」——**FTS 匹配不上**。

实测：中文问句命中 4 条 ADR 笔记、**交接记录 0 条**；把查询词换成英文 `handoff` 才命中 3 条 Session Handoff。**这不是检索能力不足，是压根没有这条通道。**

### 断点三：IDE 侧的 agent 用的还是旧二进制

`~/.claude.json` 注册的是 `command = "repotory-mcp"`（走 PATH，不带绝对路径）。PATH 上的 `/usr/local/bin/repotory-mcp` 是 **v2.11.1**，实际只暴露 13 个工具（实测 tools/list）；`/usr/local/bin/repotory-server` 是 v2.18.0。当前源码是 v2.24.0 / 18 个工具——缺 `repotory_ai_audit`、`repotory_ai_review_rules`、`repotory_notes_organize`、`repotory_wiki_read`、`repotory_wiki_search` 五个。

**这一条不是「读不到本地项目」的原因**——MCP 路径是纯本地读库，与 AI 端点无关，ADR-0017 的「一个内核两个入口」在这里成立。但它让 IDE 里的 agent 少五个工具，而 IDE 恰恰是用户最容易以为「Agent 在读我的项目」的地方。

### 还有一件事必须先说清：L3 现在是空的

真库 45 张知识页里，**全局页（`project_id IS NULL`）0 张**。ADR-0031 给过人一个「把页提升为全局页」的动作（审核页「页面作用域」区），但没人用过。ADR-0015 明写「**Producing L3 automatically is exactly the model writes into the store with no gate**」，所以 L3 **不做自动兜底**——本篇也不做。

于是「补 L3」这一半在当前库上**不产生任何效果**。真正兑现「Agent 读得到本地项目」的是另一半：**全库项目目录**。这一点必须写在决策里，否则决策会被当成「已经让 L3 活了」。

## 决策

### 1. 无作用域时也给取向：L3 + 全库项目目录

**「每一层都有它的作用域」**——L2 回答的是「这个范围里有什么」：范围是某个项目时，那一层的正文是那个项目的场景；范围是全库时，同一层的正文换成全库的目录。

- `MemoryLayer` 新增 `LayerProjectCatalog = "L2-catalog"`。
- `BuildLayeredMemory`：`projectID > 0` → L2 项目场景（**一字不改**）；`projectID == 0` → L2 项目目录。两者**互斥**，不叠加。
- `evidenceMessages` 去掉整段挡法：改成「总是装配，取到什么给什么」。`projectID == 0` 且取向层为空时，仍按现状只给证据块——**不能因为去掉了条件就变成一条 system 消息都没有**。
- **目录内容刻意粗**：项目名 / 根路径 / 仓库数 / 笔记数。技术栈、README 摘要、贡献者这些**只在指定项目时才有**（`aiProjectBaseContext` 的既有口径），目录不做逐项目深度画像——它是取向，不是详情。
- **既无仓库也无笔记的项目不进目录**：那是一行空壳，说不了这个库的任何事，只占预算。空库因此仍然装配出**零块**（既有断言「空库不产生上下文」继续成立）。
- **排序**：笔记数降序（知识密度优先），同数按项目名。仓库多但一条笔记都没有的项目排在后面，因为它对「问知识」这件事贡献更小。
- **预算独立**：目录有自己的字符上限（`LayerBudget.CatalogChars`，默认 2400），不与 L2 项目场景共用——本仓库的既有原则是「预算逐层独立，因为一个共享上限会让话多的层挤掉给人指路的层」。超预算时按既有形状处理：**裁剪而不是消音整层**，并在块上如实报 `omitted`。

**目录不是 L3。** L3 是「人批准过的、无项目归属的知识页」，ADR-0031 明确不做自动兜底。目录是「库里有哪些项目」这一**事实**，从 projects / repositories / project_notes 三张表聚合而来，不是模型写进库的内容——两者的来源与闸门都不同，混为一谈会让 ADR-0031 的闸门形同虚设。

**L3 的现状要写进文档而不是靠自动兜底**：45 张页 0 张全局页，要真正用上 L3，得先在审核页把页「提升为全局页」。这条是用户动作，不是代码能替的。

### 2. 交接记录进证据层

- `GatherEvidence` 增加第三条候选源：**最近交接记录**。取数走既有的 `db.ListRecentHandoffNotes`（同一个 `handoff` 标签谓词 + `ORDER BY updated_at DESC`），**不新写一套识别规则**——两处各写一套 tag 匹配，迟早会分叉。
- **取几条**：最多 3 条——**最新一条给正文**（上限 1200 字符，新常量 `handoffEvidenceRunes`），更旧的 2 条压成**行时间线**（复用 `oneLineSummary`，上限与 `contextHandoffTimelineLen` 同口径）。形状与 `BuildProjectContext` 一致——**最新一条必须完整，旧的是历史，认出是哪次会话就够**。这个形状已经被 abeval 的「首位命中率」度量钉过。
- **上限为什么是 1200 而不是 `contextHandoffFullLen` 的 4000**：证据总预算只有 4000 字符，单条给 4000 就等于把其余十一条挤没了。交接记录是**整场会话的记录**，正文比普通笔记长得多，所以它有自己的天花板——但**深度仍由证据总预算仲裁**（L0 先按短摘录准入，L1 再在总预算内加深，与其余证据同一套两阶段机制）。
- **L0 深度也单独给**：普通笔记的 140 字符放在交接记录上只剩一个 Markdown 标题加半句话——那不是可读的摘要，所以交接记录按 400 字符准入。
- **作用域**：`projectID > 0` → 该项目的交接；`projectID == 0` → 全库最近。
- **它是一条证据，不是一段上下文**：走同一套预算竞争与引用编号（笔记用 `N` 前缀），前端「出处」清单列得出来，「存为页面」引用得动。**不新增证据类型**（`type` 仍是 `note`，`kind` 记 `handoff`）——那会把前端出处区拆成两套渲染，是另一件事（用户已在选项里明确否掉）。
- **位置**：交接记录排在**笔记候选之首**（与 `BuildProjectContext` 把交接记录放最前同一个理由：它是上一轮会话的当前状态），最多占 3 个笔记名额，其余名额仍归检索命中。它们**不额外占位**，只是排在前面让预算按序取舍。
- **去重**：同一条笔记可能既被 FTS 命中、又被交接通道取到。按笔记 ID 去重，只出现一次——重复的证据项会让预算的覆盖声明虚高。
- **交接记录是用户数据**（可能含仓库内容片段），渲染时按数据对待，不当指令——与 `Evidence.Render()` 的既有口径一致。

### 3. IDE 侧二进制升级

- `/usr/local/bin/repotory-mcp` 与 `/usr/local/bin/repotory-server` 升级到 **v2.24.0**。
- **先备份**现二进制到 `~/Documents/repotory-rescue/bin-backup-<日期>/`，再覆盖。覆盖的是用户机器上的系统级文件，属于**仓库外**的改动——不进版本库、不进 CI。
- 验证：升级后 MCP 的 tools/list 工具数从 13 变成 18。

### 4. 不动的东西

- **不改 schema**（无新表、无新列）→ `ExpectedSchemaVersion` 仍是 28。
- **不新增 MCP 工具、不新增 `internal/app` 绑定**。
- **不改 `BuildProjectContext` 的形状**——MCP 路径（`repotory_context`）逐字不动。
- **不改页 / 笔记两条既有检索语义**——FTS / 语义召回照旧，只**加**一条候选源。
- **不改 ADR-0031「L3 不做自动兜底」**。
- **不改 ADR-0038 / ADR-0039 的任何决策**。
- **前端不动**：「问」面板的 UI 与作用域下拉不动；出处清单会多出交接记录条目，但类型仍是 note。

## 后果

- **默认作用域下的问答第一次能回答「我有哪些项目」「这个库长什么样」**——但要如实说清：目录是**粗粒度**的（项目名 / 路径 / 两个计数），技术栈与 README 摘要仍只在指定项目时才有。用户问「用的是什么技术栈」而没选项目，模型仍答不全——这是取舍，不是遗漏。
- **L3 仍是空的**。真正要用上跨项目画像，用户得先在审核页把页提升为全局页。这条进文档（`docs/features/knowledge.md` 中英双份），不靠自动兜底。
- **答案可能引用一次会话的结论**。交接记录是上一轮会话的产物，**可能已经过时**——模型被告知「这些是数据」，但不能被告知「这些一定是真的」。这是把「上一轮的判断」放进「证据」的固有代价。缓解：最新一条给正文（它最可能仍然有效），旧的压成一行。
- **证据预算多了一个竞争者**：交接记录排在笔记候选之首，最多占 3 个笔记名额，其余名额仍归检索命中。它们**不额外占位**——排在前面只是让预算按序取舍，不是额外预留。
- **IDE 二进制的版本漂移会再次发生**。本篇只记事实，不加自动同步机制——那是分发侧的事，且需要用户明确同意才能动系统级文件。

## 落地清单

1. `internal/service/context_layers.go`：新增 `LayerProjectCatalog`；`BuildLayeredMemory` 在 `projectID == 0` 时装配目录块；新增 `layerProjectCatalog`；`LayerBudget` 加 `CatalogChars`。
2. `internal/db/repositories.go`：新增每项目仓库数的聚合查询（一条 SQL，不做 N+1）。
3. `internal/service/wiki_evidence.go`：`evidenceMessages` 去掉整段挡法；`GatherEvidence` 增加交接候选源与去重。
4. 回归测试：目录层（有目录 / 无目录 / 超预算裁剪）、无作用域时上下文非空、交接进证据（作用域正确 / 去重 / 引用编号）。
5. `docs/features/knowledge.md` 中英双份：层表补 L2-catalog；写清「L3 当前为空」这条事实。
6. `docs/adr/index.md` 与 `docs/en/adr/index.md` 各加一行。
7. CHANGELOG 记一笔。

## 修订记录

- 2026-11-02：初稿即 Accepted（用户就三个选项逐项拍定）。同日在落码前把决策 1 / 2 的细节补齐到与实现逐字一致（交接取 3 条、上限 1200、L0 深度 400、空项目不进目录），并据此改写索引摘要——**落码前补齐草稿，不算改写已生效的决策**。

## 落地记录（2026-11-02）

**真库实测**（v2.24.0，31 项目 / 136 笔记 / 4 交接记录 / 45 知识页 / 140 仓库）：

- **默认作用域的上下文**：修复前 `LayeredProjectContext(0, "")` → **chars=0、blocks=[]**（一个字都没有）；修复后 → **chars=2341**，一个 L2-catalog 块，**items=29 / omitted=2**（31 个项目里 2 个被 2400 字符的预算裁掉，其余全列出）。指定项目的路径未变：`LayeredProjectContext(124, "")` → 1000 字符（Project / Path / Repositories / Languages / Top contributors）。
- **交接记录进证据**：中文问句「最近有哪些会话交接记录与重点事项？」修复前证据是 **4 条 ADR 笔记、交接记录 0 条**；修复后证据是 **7 条——N1/N2/N3 三条 `kind=handoff` 的交接记录排在最前，N4–N7 是 ADR 笔记**。模型**直接引用了 N1/N2/N3** 并给出各自会话的实际内容（v2.15.0 发布收尾、Sprint 0/1 审计推进、全量挑刺审计 20 条结果），不再答非所问。
- **全库目录进提示**：问「我有哪些项目？分别是什么技术栈？」，模型**逐条列出 29 个项目及路径**并引用了 `[L2-catalog]`；路径推不出技术栈的项目它**自己标注「未知」**——目录刻意粗、技术栈仍在指定项目时才有这条边界，在真实回答里如实显形。

**回归**（后端）：`context_layers_test.go` 新增三例——「无作用域时装配出目录块且含项目名/路径/两个计数」「选定项目时目录与项目场景不同时出现」「超预算时裁剪而不是消音，并如实报 omitted」；`wiki_evidence_test.go` 新增四例——「无作用域的问答消息带上面向（不是空 system 消息）」「交接记录进证据集、可引用、带正文」「作用域正确（别的项目的交接不泄漏）」「同一条记录不重复出现」。后端全量 `go test ./...` 全绿。

**文档同步**：`docs/features/knowledge.md`（层表补 L2-catalog、两个 L2 互斥、目录不是 L3、L3 今天多半仍是空的）、`docs/features/ai-integration.md`（交接记录走自己的通道、全库也是一种作用域、答案可能引用一次已经过时的会话结论）——中英各改。ADR 索引中英各加一行。
