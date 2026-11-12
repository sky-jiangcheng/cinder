# ADR-0060: 按域拆分 `internal/service`——从单结构体到可独立演进的子包

状态：**Accepted（2026-11-12 工程评估提出，同日用户选定 A 绞杀者按域抽包；第一步 `review` → `internal/service/reviewrun`、第二步 wiki 读侧 → `internal/service/wikipage`、第三步 wiki 写侧的模型三件套（compile / merge / community）→ `internal/service/wikigen`、第四步 lint → `internal/service/wikilint`（均 2026-11-12 落码）；第五步 evidence 经讨论**决定不搬**（判定为跨域编排层），系列收口，跟踪 TODO 13.3）**
日期：2026-11-12
关联：[ADR-0005](0005-service-layer.md)（service / app / domain 分层）、[ADR-0055](0055-agent-surface-contract-and-observability.md)（明确「不重写 service 层」的非目标）、[ADR-0057](0057-write-idempotency-and-version-history.md)（写路径契约）
起始 schema：v31 → **v31（无 schema 变更）**；`CoreVersion` 不变

## 背景

`internal/service` 是 2026-11-12 实测的 **33,886 行 / 117 个文件 / 158 个 `*Service` 导出方法**，`Service` 一个结构体持有 DB、配置、LLM、向量、扫描器、日志等全部依赖（`internal/service/service.go:31-140`）。同一域内还混着笔记、wiki 编译、审查、导入、备份、健康检查、AI 工具调用。存储侧 `internal/db` 另有 211 个包级函数，没有仓储接口，service 直接按函数名调用。

ADR-0005 定下的是 `service / app / domain` 三层，不是「所有业务进一个包」；ADR-0055 则明确写了「**不重写 service 层**：本 ADR 是出口、预算与观测的收口，不是分层重构」。两条叠加的实际效果是：**分层被承认，但拆分被无限期推迟**，而每次跨域改动都落在同一个包、同一个结构体上。

可量化的代价（2026-11-11 评估实测）：`go test ./internal/service` 单包 13.9s；改动一处检索排序需要同时读 wiki、导入、handoff 三条路径；任何人要判断「这个改动影响谁」都得翻整个包。功能面还在长（review / importers / chat / observability 都是近两个月加进来的），成本只会上升。

## 决策（2026-11-12 用户选定）

**选 A 绞杀者按域抽包**：按域把代码从 `internal/service` 搬进 `internal/<domain>`，`Service` 保留门面与跨域编排，每一步都能单独合入、单独回滚。

抽包顺序（每一步都要求 `go test ./...` 全绿、MCP 22 工具 fixture 与 `CoreVersion` 不动）：

1. **`review`**（已有 `internal/review` 的规则层在，service 侧只剩编排）与 **`importers`**（解析与落库的边界清楚，测试自成一体）——这两块副作用最窄、依赖最少，用来验证「搬出去之后包间不成环」这件事本身。
2. **`wiki`**（编译、审批、版本）——依赖最重的一块，等前一步证明模式可行再动。
3. **`ai`**（三条 HTTP 原语 + 八个业务入口）——最后的收口。

三条硬约束，用来防止「抽包」变成「换个地方写单结构体」：

- **子包不吃 `*Service`**。依赖以参数传入（`*sql.DB`、配置值、纯数据结构），否则只是把循环依赖从包内挪到包间。
- **`Service` 保留门面**：`internal/app` 的绑定与 `cmd/mcp` 的 22 个工具继续调 `Service` 的方法，调用点零改动。
- **防环靠门不靠纪律**：`go list -deps` 断言 `internal/<domain>` 不反向依赖 `internal/service`。

代价照记：过渡期多一层转发（薄但真实）；搬运期间 `git blame` 的可读性下降一次；「先抽哪块」是判断而非推导，选错只是顺序成本，不是不可逆成本——这就是选它而不选 C 的理由。

## 待定问题（已被上面的决策回答，原文保留）

1. 拆不拆：是接受单包，还是现在开始付拆分成本？
2. 拆的边界按什么：按 MCP 工具域、按数据所有权，还是按「纯逻辑 / 有副作用」？
3. 是否引入仓储接口，还是保持「子包直接吃 `*sql.DB`」的现状？
4. 拆分是否必须保持单二进制、单 MCP 工具面（答：必须，但需写进门）。

## 选项与取舍

| 选项 | 做法 | 优点 | 代价 / 风险 |
|---|---|---|---|
| A 绞杀者按域抽包 | 先 `review`、`importers`，再 `wiki`、`ai`；`Service` 保留门面与跨域编排 | 增量、可回滚；测试随代码一起搬；每步都能全绿 | 过渡期多一层转发；包间依赖要防环；拆分节奏依赖人工纪律 |
| B 只抽纯函数域 | 仅把 `review` 规则、`importers` 解析这类无副作用的搬出去 | 风险最低，收益立现（可独立测、可并行） | 核心 monolith 不动，跨域改动成本仍在 |
| C 仓储接口 + 全量重写 | 引入 repository / CQRS，按聚合重划边界 | 边界最干净，长期最优 | 成本最高，直接违背 ADR-0055 的非目标；当前没有用户可见收益，回归面最大 |
| D 不拆，只加约定 | 文件命名分段 + 每域测试入口 + CODEOWNERS 式注释 | 零改动 | 不解决 review 面积、编译时间与依赖纠缠 |

## 明确不决

- ~~不在本篇选定 A/B/C/D~~ —— **已选定 A（2026-11-12）**；不承诺时间表，但顺序已定（review/importers → wiki → ai）。
- 不改 MCP 工具面、不改绑定、不改 schema、不拆二进制。
- 不做「大爆炸」重写，也不引入仓储接口（C）：`internal/db` 的 211 个包级函数照旧直接调，抽包不改变数据访问形态。

## 验收判据

- 第一步（`review` + `importers`）：`internal/service` 的行数与导出方法数下降**可度量**，`go test ./internal/service` 时间下降，既有测试**除 import 路径外零修改**通过。
- `go list -deps` 证明无循环依赖（子包不反向依赖 `internal/service`）；`internal/app` 的绑定面注册表测试不变。
- 行为零变化：MCP 22 个工具的 fixture 与 `CoreVersion` 不变；`internal/app` 与 `cmd/mcp` 的调用点零改动。

## 实施记录（2026-11-12，step one）

落地形态：`internal/service/reviewrun`（实现 5 个文件 + 测试），`internal/service` 只留门面、refiner 与 ticker。

**实际只有 `review`，没有 `importers`。** 抽之前先查了代码：service 里不存在 importer 的实现域 —— 导入早已下沉到 `internal/core/plugin/runtime`（12.x 那批），`internal/service` 剩下的 `auto_import_test.go` 测的是一个配置谓词（`auto_import` 必须显式 opt-in），不是解析与落库。所以「第一步两块」在评审时的判断只对了一块，这里按实际落。

三处与判据不符，如实记：

| 判据 | 结果 | 说明 |
|---|---|---|
| `internal/service` 行数下降 | **达标**：33,886 → 33,075（-811） | 搬走 1,013 行实现 + 888 行测试，门面与注释补回约 1,090 行 |
| 导出方法数下降 | **未达标**：158 → 158 | 门面刻意保留，且 12 个 `Review*` 方法**全部**有外部调用者（`internal/app` 8 处、`cmd/mcp` 4 处），删任何一个都是改绑定面 |
| `go test ./internal/service` 时间下降 | **未达标**：13.9s → 13.8s | 那 13.9s 主要花在 scan / wiki 的 git 与 embedding fixture 上，与 review 无关；本步把 0.37s 的 review 测试移出，读数自然不动 |

结论：**方法数与包内测试时间不是这一步的有效指标**，把它们写进判据是当初的想当然。真正可度量的是「review 现在能脱离 `*Service` 单独测」，以及依赖方向被一次 `go list -deps` 钉死。后续步骤（wiki / ai）应改用「子包可独立构造」与「跨域改动落在几个包」作为验收，而不是拿总行数当进度条。

唯一跨包依赖是模型调用。`reviewrun.Refiner` 只有 `Verdicts(ctx, path, content, chunk)` 一个方法，service 侧 `reviewRefiner` 实现它（endpoint、key、ADR-0030 审计行都留在 service）。`reviewrun.ErrUnavailable` 区分「没配 endpoint」与「调了但失败」：前者记一条跳过日志、run 保持 rule-only，后者计入 `_failed_` 并把数量写进 `review_runs.error` —— 这条语义是从原实现逐字搬过来的，不是新决定。

测试改动：`reviewrun` 新增 7 例（默认值、配置校验、`ParseClock`、以及从 service 搬来的 `repos` / `ruleSet` 三个白盒用例）；service 侧 `review_test.go` / `review_notify_test.go` **零改动**，`review_ai_test.go` 改 6 处引用（`reviewrun.Refine` / `ParseVerdicts` / `SystemPrompt` / `KeyAI`）。

无环已验：`go list -deps ./internal/service/reviewrun` 不含 `repotory/internal/service`；`go test` 42 包全 ok（拆分前 41 + 新包）；MCP 工具 fixture 与 `CoreVersion` 未动。

## 实施记录（2026-11-12，step two：wiki 的读侧）

落地形态：`internal/service/wikipage`（`page.go` / `versions.go` / `suggest.go` / `scope.go` / `export.go` / `feed.go` + 8 例测试）。

**wiki 没有作为一个整体搬走，因为这批代码的耦合面不是均质的。** 先按「对 `Service` 的引用」把 12 个 wiki 实现文件排了一遍，结果分成三层：

| 层 | 文件 | 除 `db` 外的依赖 |
|---|---|---|
| 读侧 | browse / page_versions / suggest / scope / export | 几乎没有 |
| 写侧 | compile / merge / lint / community | 全部要模型（`aiChatConfig` + `chatWithAudit`） |
| 调度与问答 | compile_job / evidence | 模型 + 检索 + `bgGo` / `finishJob` / `toolRegistry` |

所以这一步只搬第一层。它不是「先搬好搬的」这种偷懒：读侧**不需要任何接口**就能走，这本身就是它值得先走的理由——如果连它都要造 seam，说明边界判断错了。

`wikiReviewFeed` 与 `WikiPendingPage` 一起搬了过去（`feed.go`），因为它虽然住在 `wiki_compile.go` 里，内容是一段纯读投影，且被三个入口共用（待审队列、scope 页、编译后的「刚产出了什么」）。把它留在写侧，等于让读侧为了一个返回结构去依赖编译流程。

**两个共享的文本工具选择导出而不是复制**：`WikiLinkRe`（`[[link]]` 正则，compile / lint / suggest / export 四处用）与 `FirstLine`（`context_layers.go` 做摘要用）。这类符号一旦有两份副本，漂移只是时间问题，而它们出现在同一个报告里就会自相矛盾。`clipRunes`（4 行 rune 截断）暂时保留两份，注释里写明了「第三个调用者出现时就该提取」——为一个四行函数建 `textutil` 包是过度设计。

度量：`internal/service` 33,075 → 32,340（再 -735）；导出方法数仍是 158（门面照旧，理由同 step one）；`go test ./internal/service` 仍是 13.x 秒（wiki 读侧测试本来就只占 1.3s，且它们仍从门面跑）。子包 1,025 行含测试。

测试：service 侧 `wiki_browse_test.go` / `wiki_export_test.go` / `wiki_scope_test.go` / `wiki_suggest_test.go` **零改动**通过（走门面，端到端行为不变）；子包新增 8 例，全部只用 `:memory:` 库，覆盖「空列表必须是 `[]` 不是 null」「pending 页不可读」「promote 幂等」「CJK snippet 不切半字符」。

`go list -deps ./internal/service/wikipage` 不含 `repotory/internal/service`；`go test` 43 包全 ok。

**剩下的 wiki 三块各自需要什么 seam**（下一步的判断依据，先写下来免得临时拍脑袋）：

- compile / merge / community：只需要一个「一组消息 → 一段回复」的调用，形状与 `reviewrun.Refiner` 相同但语义不同（不是裁决，是生成）。可以复用同一个接口类型，也可以各带一个小接口——取决于是否要保留「零候选零调用」那类预算约束。
- lint：模型 + `bgGo`。job 生命周期属于 service，lint 规则可以搬。
- evidence：最重。它要检索、要流式回答、要 tool registry、要 `bgCtx`——这块搬与不搬值得单独讨论一次，不要默认「抽干净才算完成」。

下一步按顺序是 compile / merge / community 一组（同一类 seam）。

## 实施记录（2026-11-12，step three：wiki 写侧的模型三件套）

落地形态：`internal/service/wikigen`（`seam.go` / `config.go` / `findings.go` / `compile.go` / `merge.go` / `community.go` + 4 例测试，2,166 行含测试）。

**先把动手前那个待答的点答了——要不要复用 `reviewrun.Refiner` 的接口类型。答案是不能，字面上就不能**：`Refiner.Verdicts(ctx, path, content, chunk)` 的签名钉死在 `review.Finding` / `Verdict` 上，「复用同一个接口类型」实际意味着把它降级成「消息进、回复出」，那已经是一个新接口。真实选项是「每个 kernel 一个域形状小接口」与「这组共用一个窄传输 seam」，**选后者**——理由是生成与裁决的差别落在**解析失败的处置**上，而不是动词上：

- 裁决（review）解析失败是**静默**的：`ParseVerdicts` 返回空、规则裁决原样保留，runner 不需要向用户解释任何事，所以 parse 能待在 bridge 后面、接口可以做成域形状。
- 生成（本组）解析失败**本身就是产出的一部分**：三个 kernel 各欠用户一句不同的话（「本次不写入任何东西」/「本次不采信任何合并建议」/「建档但正文留空」），而把「被截断的 JSON」与「畸形的 JSON」分开需要原始的 `finish_reason`。域形状接口要么返回原文（一个交付 prose 的 `JudgePairs` 是撒谎），要么把解析失败的措辞拆到包边界两侧。

seam 只有两件事：`Message{Role, Content}`（**刻意不含 ToolCalls / ToolCallID**——生成 kernel 结构上无法表达 tool call，与 `compileOp` 词表里没有 update/delete 是同一个设计精神）与 `Reply{Text, FinishReason}`；哨兵 `ErrNotConfigured` 与 `Available(ctx)` 让 kernel 在零调用的前提下产出「AI 端点未配置」那条 note（compile 与 community 在统计任何东西之前就返回，merge 在读候选之前就跳过——这是搬包前逐字对照过的控制流顺序）。endpoint、key 与 ADR-0030 审计行留在 `internal/service`：唯一的 bridge `wikigenChat`（`wikigen_bridge.go`，两个方法）负责 config 解析、`[]Message → []chatMessage` 的收窄，以及把 `aiChatConfig` 的任何失败映射成 `ErrNotConfigured`。

**预算约束全部保留，且没有一条住在 seam 里**——它们本来就是 kernel 循环里的控制流：merge 的「零候选零调用」（`len(cand.Candidates)==0 → return`）、compile 的页数与 note 字节上限、community 的墙钟预算与「门后置在免费检测之后」。搬包时这段控制流随 kernel 逐字走，一行未改。子包新增 3 例用**计数 fake** 的测试，在 seam 上钉住「门关 / 无端点 / 零候选 ⇒ 零调用」——因为现在传输层就是那个被计数的东西，这个证明只有在拆出 seam 之后才写得出。

跨包依赖方向：`wikigen` 不 import `internal/service`；`service` 单向 import `wikigen`（bridge、门面、别名）。

**两处「第三个调用者出现」的旧账在同一批还掉**：

- `clipRunes`：step two 记的「四行暂留两份、第三个调用者出现时再提取」——`wikigen` 就是第三个。定义搬到 `wikipage.ClipRunes`（wikipage 已是 wiki 文本工具的家，`FirstLine` / `WikiLinkRe` 都在那），`internal/service` 留一层 wrapper、`wikigen` 内同名调用——三份收敛成一份。
- `DefaultBatchChatTimeout`：它存在的理由正是本组的调用（本地 27B 一次 compile 实测 173s），所以定义随调用者搬进 `wikigen.BatchTimeout`，`internal/service` 留 `const DefaultBatchChatTimeout = wikigen.BatchTimeout` 别名——`cmd/server` 那条「写超时必须高于批量上限」的断言与测试一字未改。

**共享词表的归属**：`WikiFinding` / `LintTodoTitle` / `TruncatedHint` 随 kernel 搬进 `wikigen`（merge 产出 finding；compile 与 community 经 `LintTodoTitle` 渲染修订待办；三个 kernel 都要 `TruncatedHint` 区分截断与畸形）。`internal/service` 侧一律留下**别名或 wrapper**：`type WikiFinding = wikigen.WikiFinding`，`lintTodoTitle` / `truncatedHint` / `wikiLintLLMEnabled` / `communitySizeThresholds` 均为转发，`CommunitySynthesisSlug` / `CommunitySynthesisTitle` 为 wrapper。const 别名（配置键、source tag、预算常量共 10 个）由编译器钉住同值，**不可能漂移**。`truncateBytes` / `firstNonEmpty` 因两侧都在用，各留第二份并注明「第三个包需要时才提取」——与本 ADR 一贯的容忍度一致，不为几个字面量重建一个 textutil 包。

**测试改动（如实记，与「既有测试除 import 路径外零修改」的判据有出入的地方都在这里）**：

| 改动 | 说明 |
|---|---|
| `TestMergeSimilarityHelpers` 搬进 `wikigen/merge_test.go` | 纯 helper 白盒用例，无库、无 service（step one 搬 `repos` / `ruleSet` 白盒用例的同一做法） |
| `TestCompileNoteTargets_SharesSelectionWithSyncPath` 改 1 处引用 | 该用例断言的是「job 路径与 kernel 对『哪些笔记』一致」，属于 service 侧，故留在原文件，调用点改成 `wikigen.CompileNoteTargets` |
| 其余引用零改动 | 配置键 / tag / 常量的引用由 const 别名兜住；函数经由 wrapper |

**度量**：`wikigen` 2,166 行 / 8 文件（含测试）；`internal/service` 顶层现 118 文件 / 30,746 行（**快照**，该目录同时承着 step one/two 与其它未提交在制品，不是本步的独立增量）；`*Service` 导出方法数 158 → 158（门面照旧，理由同前两步）。`go list -deps ./internal/service/wikigen` 不含 `repotory/internal/service`；`go test ./...`：`wikigen`（0.3s）与 `internal/service`（13.4s）全绿，44 个可测包中 43 绿——`internal/db` 的 `TestCreateRollingBackup_WritesIntradaySnapshots` 红，源于工作区里未提交的滚动备份在制品（`internal/db/backup.go` +229 行），与本次拆分无关（`db` 不依赖 `service`，两个文件均未被本步触碰）。MCP 工具面与 `CoreVersion` 未动。

**剩下两步**：lint（其模型半与 merge 同形状，可直接接本文的 seam；job 生命周期留 service）、evidence（检索 + 流式 + tool registry，搬不搬值得单独讨论）。

## 实施记录（2026-11-12，step four：lint）

落地形态：`internal/service/wikilint`（`lint.go` / `model.go` + 2 例测试，726 行含测试）。

**分界严格按本文原话来落：「job 生命周期属于 service，lint 规则可以搬」。** 搬走的是规则与产出——四个结构检查（orphan / dangling-link / missing-ref / thin-page / no-source / no-synthesis / relation 三条）、模型半、报告组装、以及 `fileFindings` 的待办落库；留下的是调度——`startWikiLintTicker` 的 `bgGo`、`wiki_compile_job.go` 里的 lint job 队列与 `lintStopNote`。`Service` 保留 `RunWikiLint` 门面，ticker / job worker / Wails 绑定三处调用点的名字与签名不变；`WikiLintReport` 是别名。

**模型半直接接了 step three 的 `wikigen.Chat` seam，没有另造接口。** 它和 merge-suggest 同形（一批页面进，findings 加一句「为什么没跑」出来），而 merge-suggest 本就是 wikigen 的 kernel、由这个 pass 调用——两个模型半共用一个传输层是自然的，也让「门关 / 无端点 ⇒ 零调用」在 lint 侧同样能用**计数 fake** 断言（子包新增 2 例，gate 检查全部发生在序列化之前）。依赖方向：`wikilint` → `wikigen`（`Chat` / `WikiFinding` / `LintTodoTitle` / `LintLLMEnabled` / `SuggestMerges`）+ `wikipage`（`WikiLinkRe`），单向，无环。

**`WikiFinding` 留在了 `wikigen` 而不是跟着 lint 走，这是被依赖方向逼出来的**：merge-suggest 在 wikigen 且产出 findings，若把类型搬到 wikilint，`wikigen` 就要 import `wikilint`，而 `wikilint` 又调用 `wikigen.SuggestMerges`——成环。所以 finding 是「模型驱动写侧内核」的词汇，lint 消费它，不是反过来。

**`truncateBytes` 的第三个包出现了**（step three 在 `wikigen` 留的第二份注释写明「第三个包需要时才提取」）：wikilint 就是第三个。定义搬到 `wikipage.TruncateBytes`（与 `ClipRunes` 并列成「截断对」），`internal/service` 与 `wikigen` 各留一层 wrapper——一份定义、三处使用，副本漂移的风险归零。

**测试改动（如实记）**：`TestParseLintReply`（纯解析器白盒）搬进 `wikilint/model_test.go`；其余 lint 测试零改动——它们走 `RunWikiLint` 门面、引用 `wikiLintLLMKey` / `lintTodoPrefix` 两个常量，而这两个别名在 step three 已就位。子包新增 2 例计数 fake 测试。

**度量**：`wikilint` 726 行 / 4 文件（含测试）；`internal/service` 30,746 → 30,163（-583，快照口径同前，该目录仍承着前几步与其它在制品）；`*Service` 导出方法数 158 → 158。`go list -deps ./internal/service/wikilint` 不含 `repotory/internal/service`；`go test ./...`：45 个可测包中 44 绿，唯一一红仍是 `internal/db` 的既有失败（滚动备份在制品）。MCP 工具面与 `CoreVersion` 未动。

**只剩 evidence**：检索 + 流式 + tool registry + `bgCtx`，按本文原话「搬不搬值得单独讨论」——它的依赖里没有一个是「消息进、回复出」的同形 seam，套不上本记录的模式，要单独评估。

## 实施记录（2026-11-12，step five：evidence——决定不搬，系列收口）

**结论：evidence 不搬。** 它不是「下一个域」，而是这套绞杀者模式撞上的边界。

**收口判据**（写给下一个想搬东西的人）：一个单元只有当它的依赖面**小且稳定**时才搬；把 N 个尚未搬走的域编在一起的是**编排层**，编排层留在 `Service`——这正是本文决策段自己的话（「`Service` 保留门面与跨域编排」）。

evidence（`wiki_evidence.go`，897 行，导出面为 `GatherEvidence` / `AskAIWithEvidence` / `AskAIWithEvidenceStream` / `StartAskStream` / `FileAnswerAsPage` 加几个类型）的 `s.` 依赖不是一个，是**六个未搬走的域**：

| # | 依赖 | 具体符号 | 现居 |
|---|---|---|---|
| 1 | 传输 | `aiChatConfig` / `askWithTools` / `streamAskWithTools` / `truncatedHint` / `aiAskTimeout` | `ai.go` / `aitools.go` |
| 2 | 检索 | `SearchNotes` / `semanticEnabled` / `pageVectorRecall` / `graphSearchEnabled` / `graphNeighborOrder` | `search.go` / `search_semantic.go` / `wiki_graph_evidence.go` |
| 3 | 工具循环 | `toolRegistry.definitions()` | `aitools.go` |
| 4 | 上下文 / 记忆 | `BuildLayeredMemory` / `aiProjectBaseContext` / `LayerBudget` | `context.go` / `context_layers.go` / `ai.go` |
| 5 | 时间源 | `s.git.GetTodayDate()` | scanner provider |
| 6 | 生命周期 | `bgGo` / `bgCtx` / `askStreams` | `service.go` |

对照前四步的依赖面——step one 是 `Refiner` 一个方法、step two 几乎为零、step three 与 step four 各一个 seam（step four 另加 wikipage）——evidence 的六项里五项是**尚未搬走的域**。硬搬需要定义五到六个 port，而每一个 port 都只是把 `Service` 的既有方法换个名字注入回来：**隔离是假的**（search / tools / context 一变，evidence 照样隔着 port 跟着变），review 面积不减，只多一层转发。

决策段其实已经预告了这件事：抽包顺序第 3 项是「**`ai`（三条 HTTP 原语 + 八个业务入口）——最后的收口**」。evidence 属于那个 bucket，而那个 bucket 的形状与 wiki 系四步不同——它不是「一个域一条 seam」，而是传输 + 工具循环 + 上下文 + 问答编排互相咬合的一大块，需要自己的 ADR 与设计，不是本系列的延续。

**如果将来要让它可搬**，前提是先把它的六个依赖变成域：`search`（`SearchNotes` + 语义 + 图检索）、`ai` 传输与工具循环（`ai.go` + `aitools.go`，合计约 2,100 行，是 `internal/service` 里剩余最大的单块）、`context` / 记忆。在那之前搬 evidence，只会把一个编排器换成六个 port。

**系列到此收口**：step one~four 是本 ADR 承诺的可增量、可回滚的抽取；evidence 按判据停在 `Service`，是本 ADR 边界勘定的一部分，不是未完成项。`internal/service` 的后续压缩目标应当是上表里的**依赖域**（尤其 `ai` cluster），而不是这个编排器本身。
