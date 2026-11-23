# ADR-0022: 自动发现与定时同步 agent 记忆——来源类型用户可见

- 状态：Accepted（**2026-10-23 用户评审晋升**——2026-10-22 逐阶段核验已收口全部实施项，晋升时 `go test ./...` 全量再次全绿）。以下为 2026-10-22 逐阶段核验记录（当时发现五阶段实际落码边界与原状态行所述不符，按实测回写）：**P1 接线** ✅ 已落码（`internal/service/plugin.go:115-116` 注册 codebuddy 双源，共八源；`internal/importers/codebuddy` 5 项测试）；**P2 发现探活** ✅ 已落码（`plugin.Prober` 接口 + `plugin.ProbeResult`，`internal/core/plugin/plugin.go:29-41`；`runtime.safeProbe` 带 panic 恢复，`internal/core/plugin/runtime/runtime.go:151`；`SourceStatus` 扩为 `{name, plugin, detected, enabled, detail, type, last_run, last_error}`；七源全部实现 `Probe()`，memsrc 为内置测试源不适用）；**P3 定时同步** ✅ 已落码（`internal/service/knowledge_sync.go` ticker，首跳推迟一个周期，`knowledge_sync_interval_minutes` 默认 240 分钟，逐源 `sync_source_<name>` opt-in，claude 默认开其余默认关；**per-source `lastRun`/`lastError` 已补**，落在 runtime 层 `SourceStatus`，手动导入与定时轮次共用同一份状态）；**P4 来源类型与 UI** ✅ 已落码（派生映射 `SourceTypeFor`（`runtime.go:48`，已导出为跨层单一真值源）+ `SourceType` 四值常量 + `source_type_test.go` 三项钉住 + 设置页类型 chip / 探活状态点 / detail / 逐源同步开关 / per-source 同步时间与错误 + `settings.sourceTypes.*` 双份 i18n；**笔记卡片已按类型显示 label + 类型色调**——新增共享组件 `web/src/components/SourceTypeBadge.tsx`（后端下发 `source_type`，前端不二次派生））；**P5 验证与收口** ✅ **已落码**（`go test ./internal/...` 全绿；**golden-file 回归已建立**——ADR-0011 决策 5 要求的「各源 golden-file 样本」已在 `internal/importers/` 下七源全覆盖（claude/codex/codebuddy/opencode/openclaw/hermes/cursor，共 28 个样本文件；两个 SQLite 源用 `.sql` dump 形态，共享辅助 `internal/importers/imptest`）；**GUI 真机冒烟已做**——隔离环境下走通 hermes 源「探活 → 导入 → 幂等复跑 → 定时同步 → 设置页展示」全链路，见 `outputs/screenshots/2026-10-22-adr0022-closure/`）。**实施侧无遗留项**：P4 与 P3 的三处偏差已全部收口，P5 的 golden-file 目录形态亦已收口；方案本身于 2026-10-23 经用户评审晋升 Accepted）
- 日期：2026-10-14
- 关联：[ADR-0011](0011-multi-agent-memory-importers.md)（多 agent 记忆源导入）、[ADR-0014](0014-llm-wiki-knowledge-compiler.md)（LLM Wiki 知识编译层）、[ADR-0015](0015-w3-minimal-compile-loop.md)（W3 最小编译闭环）、[ADR-0007](0007-session-memory-protocol.md)（会话记忆协议）、[ADR-0009](0009-ide-presence.md)（IDE 存在感）、TODO 会话记忆路线 M2/M8

## 背景

用户诉求（目标环境为 **macOS 真机**，宿主为 Wails 桌面应用；agent 记忆目录随 OS 不同，发现路径按 OS 分表、本 ADR 以 macOS 为准）：本机安装了多种自带对话记忆能力的 agent 工具——CodeBuddy 这类 **IDEA/VS Code/JetBrains 插件**（会话转录与 curated 记忆分别落在 `~/Library/Application Support/CodeBuddyExtension/Data/...` 与 `~/.codebuddy/memery/*.md`），以及 Claude Code / Codex / OpenCode / OpenClaw / Hermes / Cursor 等**命令行 agent**。希望应用能做到四点：

1. **自动发现**本机这些 agent 工具的对话记忆；
2. **定时同步**这些记忆；
3. 把同步内容**组织进应用自有知识库**；
4. 每条知识的**来源类型对用户可见**（如外部导入 / 项目提取 / 用户对话等）。

2026-10-14 现状盘点显示，第 3、4 点已有地基，第 1、2 点是纯缺口：

- **导入管线已就绪（ADR-0011）**：`plugin.KnowledgeImporter` 产出 `[]plugin.ImportDoc{ProjectID,Title,Content,Tags,Kind,Source}`，经 `Runtime.RegisterSource(/Manual)`（`internal/core/plugin/runtime/runtime.go`）注册，`TriggerImport → upsertDoc` 以 `(project_id, source, title)` 幂等 upsert 进 `project_notes`（`source TEXT DEFAULT 'manual'`）。已注册六源：`claude`（AUTO，唯一启动自动导入）、`codex/opencode/openclaw/hermes/cursor`（MANUAL，默认关，仅显式触发）。`internal/service/plugin.go:89 registerBuiltinImporters` 为唯一注册点。
- **CodeBuddy 导入器代码已写完、未接线、未提交**：`internal/importers/codebuddy/codebuddy.go`（621 行，git 未跟踪）实现双源——`codebuddy`（会话转录）+ `codebuddy-memory`（curated 记忆，`codebuddy_project` 目标项目配置），但 `plugin.go` 没有 import 与注册，注释仍写「六个内置源」。这是当前仓库里唯一「半完成」的操作。
- **没有自动发现**：源的可用性靠用户自己知道路径；`SourceStatus.Enabled` 目前只是 `lastErr == nil` 的派生值，不是「本机是否装了该 agent、记忆目录是否可读」的检测结果。用户装好 CodeBuddy 后，设置页不会自己冒出该源。
- **没有定时同步**：只有启动 auto-import（仅 claude）与设置页手动点按；服务层已有 `time.NewTicker` 惯例（`startEmbedDrainer` 5s、`startWikiLintTicker` 6h、page drain、compile poll，均在 `internal/service/`），但知识导入没有周期通道。
- **来源标注只有原始字符串，无分类、几乎不可见**：`project_notes.source` 存 importer 名（claude/codex/…/manual），前端仅 `NoteSection.tsx:274` 给 `source==='claude'` 画了一个 badge，其余来源、`manual`、`handoff`（会话交接）一律不显示；不存在「外部导入 / 项目提取 / 用户对话」的分类概念，也没有按来源过滤的入口。项目扫描（scan 管线）只产出 `repo_meta`（README excerpt + stats，进 llm 上下文），不产知识笔记，因此「项目提取」目前是空类别，需要预留。

## 决策

1. **发现 = 每个源实现 `Probe()`，注册即探活**。在 `plugin.KnowledgeImporter` 上新增 `Probe() ProbeResult`（`Detected bool` + `Detail`：根路径、匹配文件数、错误摘要）；`Runtime` 在 `RegisterSource` 时立即对每个源跑一次只读探活，`SourceStatus` 扩展为 `{name, plugin, detected, enabled, detail}`（`enabled` 保留原语义：上次导入是否成功）。探活沿用 openclaw/hermes 的**硬 scoping 纪律**：只检查各源声明根目录（如 `~/.codebuddy/...`、`~/.claude/...`）内是否存在至少一个记忆文件，绝不读 parent/sibling/子目录之外的内容。设置页来源列表从「列注册表」变成「本机实际发现了什么」。

2. **CodeBuddy 接线即首个新增源（先行项）**。提交 `codebuddy.go` 入库，在 `registerBuiltinImporters` 增加 `RegisterSourceManual(codebuddy.SourceName, codebuddy.New(s.db))` 与 `RegisterSourceManual(codebuddy.MemorySourceName, codebuddy.NewMemory(s.db))`（保持 MANUAL，会话转录隐私敏感，遵守 ADR-0011 决策 4 默认关）；`codebuddy_project` 与 `openclaw_project`/`hermes_project` 同列设置页「全局记忆源目标项目」输入组；同步更新 `plugin.go`/`runtime.go` 注释「六个」为「八个」。Cost：约一个提交 + 十行注册，最快解除「未完成操作」。

3. **定时同步 = 服务层 sync ticker + 逐源持久化开关，默认关**。新增 `internal/service/knowledge_sync.go`：`startKnowledgeSyncTicker`（沿用 `time.NewTicker` 惯例，**首跳推迟一个周期**，避免启动即全量导入，同 `startWikiLintTicker`）。参数：
   - 周期配置 `knowledge_sync_interval_minutes`（默认 `240`，即 4 小时）；
   - 逐源开关 `sync_source_<name>`（`'1'`/`'0'`，默认 `'0'` 关；`claude` 延续 AUTO 语义默认 `'1'`）——**隐私默认关**是 ADR-0011 决策 4「每个源默认关、显式开」向周期通道的延伸；
   - 每轮同步 = 对所有 `sync_source_*='1'` 的源依次 `TriggerImport`（`importMu` 已串行，与设置页手动导入互斥不重复）；
   - 结果走既有 `emitImportEvent → import.completed` toast，并在 `SourceStatus` 带上 `lastRun`/`lastError` 供设置页展示上次同步时间与错误。

4. **来源类型 = 服务端派生分类**。新增枚举（不新增 DB 列，`project_notes.source` 已存在，分类是展示/过滤用的派生语义）：

   | 来源类型 | 含义（用户可见文案） | 映射的 source 值 |
   |---|---|---|
   | `agent-import` | 外部导入（agent 记忆导入） | `claude, codex, opencode, openclaw, hermes, cursor, codebuddy, codebuddy-memory` |
   | `user-conversation` | 用户对话（会话交接沉淀） | `handoff`（现有会话交接标签笔记）、未来结构化对话导入 |
   | `project-extract` | 项目提取（从项目/仓库提取） | 预留空类别；当前扫描只产 `repo_meta` 不产笔记，未来 README/代码摘要等经编译成笔记时归此类 |
   | `manual` | 手动（用户手写/编辑） | `manual` 及用户直建笔记 |

   实现为 `internal/service`（或 `internal/core`）单一 `SourceType(source string) (type, label)` 函数，未知 source 兜底显示其原始字符串、类型归 `manual`；前端通过现有 bindings 拿 `{source, source_type, label}`，i18n 文案走 react-i18next。

5. **来源用户可见 = 三层 UI**：
   - **笔记卡片**：`NoteSection.tsx` 现有 claude 专属 badge（`badgeSource`）泛化为「来源 badge」——所有 source 按类型显示 label（如「CodeBuddy 导入」「会话交接」「手动」）+ 类型色调，与现有 kind badge 并列；
   - **设置页来源列表**：`PluginsTab.tsx` 每源一行增强为：发现状态（已检测/未检测 + 根路径）→ 来源类型 → 定时同步开关 → 立即导入 → 上次同步时间；全局记忆源的目标项目输入随决策 2 增加 `codebuddy_project`；
   - **知识库浏览/检索**：笔记列表、搜索结果复用同一来源 badge 组件，用户在任何看到知识的界面都能回答「这条知识来自哪里」。

6. **组织进自有知识库 = 保持两层管线，不新建存储**。导入 → `project_notes`（现有 upsertDoc）→ M6 wiki 编译（ADR-0014/0015）产出 wiki page（`page.source` 继承笔记 source）；来源类型在 notes 与 pages 两层都可见。本 ADR 不新建表、不重建索引、不动 `embed-drainer`/向量检索（同步产生的新笔记自然经 `note_embed_dirty` 队列进入检索）。

## 理由

- **复用 > 新建**：发现（Probe）= 每个源一个只读函数；同步 = 一个 ticker + 一组配置；来源 = 一个派生函数 + badge 泛化。全部落在 ADR-0011 管线与服务层 ticker 惯例内，不碰 schema、不碰分层冻结面（ADR-0006）。
- **隐私默认关延续既有纪律**：定时同步是「周期读取 agent 私人记忆目录」的高敏感能力，逐源显式开启与 ADR-0011 决策 4 一致，避免「打开应用就自动把我所有 agent 的对话导进来」。
- **来源分类用派生映射而非 schema 迁移**：`source` 字符串已是版本化事实（v23 及更早），分类是展示语义；单一派生函数保证服务端与前端一致，未来确有过滤/统计需求时再物化为列（见待决）。
- **CodeBuddy 先行顺理成章**：代码已写完，只差接线；其双源形状（会话转录 + curated 记忆）正好覆盖「发现 + 定时同步」两种形态的试点，也直接解除仓库当前唯一未完成操作。

## 后果

- **正面**：设置页从「我知道有哪些源」变成「应用告诉我本机装了什么」，新装 agent 后无需手写路径；定时同步让 agent 记忆增量持续进入知识库并随现有 embed-drainer 进入语义检索；来源类型在笔记、页面、设置三处用户可见，满足「这条知识哪来的」的一等追问。
- **负面**：注册时探活增加一次只读 IO（每个源一次 stat + 少量文件列举，可接受）；定时同步周期性占用 IO/CPU，与开启源数量成正比，靠间隔配置与逐源开关控制；来源分类是人工枚举，新源上线需同步登记映射，否则落入 `manual`/原始字符串兜底。
- **待决**：① 是否提供「全局一键开启所有源同步」——倾向否，逐源开关更符合隐私默认关；② 是否在搜索/列表加「来源类型」筛选与统计——等真实需求信号；③ `source_type` 是否物化为列（schema v24）——当按类型过滤成为高频路径时再考虑；④ 同步失败是否加 backoff 重试——先沿用 `TriggerImport` 单次语义 + `lastErr` 展示，观察真机表现。

## 落地安排（分阶段，供评审）

- **P1 接线（先行）**：`git add internal/importers/codebuddy/codebuddy.go`；`plugin.go` 注册双源 + 注释更新；`runtime.go` 注释更新；`PluginsTab` 目标项目组加 `codebuddy_project`。验证：`go build ./...`、`go test ./internal/importers/codebuddy/ ./internal/core/plugin/runtime/`；真机跑一次 CodeBuddy 双源导入（需本机存在 CodeBuddy 数据）。
- **P2 发现（Probe）**：runtime 增加 `Probe` 契约与 `SourceStatus.detected/detail`；七源（claude/codex/opencode/openclaw/hermes/cursor/codebuddy×2）各实现只读探活（多数是检查根目录 + 至少一个样本文件，复用各源既有 golden-file 样本）；设置页展示发现状态。
- **P3 定时同步**：`knowledge_sync.go` ticker + `knowledge_sync_interval_minutes` + 逐源 `sync_source_<name>` + 设置页开关与 lastRun 展示。
- **P4 来源类型与 UI**：`SourceType` 派生函数 + 测试（钉住上表映射 + 未知兜底）；NoteSection 来源 badge 泛化；设置页每源类型/开关信息增强；i18n 文案。
- **P5 验证与收口**：golden-file 回归保持（ADR-0011 决策 5）；`go test ./internal/...`；GUI 真机冒烟（源列表发现 → 开启某源定时同步 → 笔记 badge → 设置页 lastRun）；本 ADR 的晋升与实施修正记录按项目惯例在落地后回填。

## 实施修正记录（2026-10-22 逐阶段核验）

上一版状态行写「P2/P4/P5 尚未开工」，逐阶段对代码核验后发现**两条已过时、一条部分过时**。核验方法：不读索引行，逐阶段对实现与测试（ADR 状态行会滞后于代码，索引行则滞后于正文）。

### P2 发现探活 —— 原写「未开工」，实为完全落码

| 决策要素 | 落点 | 状态 |
|---|---|---|
| `Probe() ProbeResult` 契约 | `internal/core/plugin/plugin.go:29-41`（`ProbeResult{Detected, Detail}` + 可选能力接口 `Prober`） | ✅ |
| 注册即探活 | `internal/core/plugin/runtime/runtime.go:151` `safeProbe`（带 panic 恢复，单源探活失败不拖垮整个设置页） | ✅ |
| `SourceStatus` 扩展 | `runtime.go:63-70`，`{name, plugin, detected, enabled, detail, type}` | ✅ |
| 七源各自探活 | claude / codex / opencode / openclaw / hermes / cursor / codebuddy 均实现 `Probe()`；memsrc 是内置测试源，不适用 | ✅ 7/8 |
| 硬 scoping 纪律 | 各源只 stat 自身声明根目录（hermes 只碰 `memories/*.md` 与 `state.db`，不读 `.env` / `mcp-tokens/`） | ✅ |

### P4 来源类型与 UI —— 主体落码，与决策文本有偏差

已落码：派生映射 `SourceTypeFor`（`runtime.go:48`）+ `SourceType` 四值常量（`agent-import` / `user-conversation` / `project-extract` / `manual`）+ `source_type_test.go` 三项测试钉住八源映射与未知兜底 + 设置页类型 chip、探活状态点、detail、逐源同步开关、预览/立即导入按钮 + `settings.sourceTypes.*` 双份 i18n。

**偏差两处，均已收口（2026-10-22）**：

1. **决策 4 的 `label` 未单独派生 → 保留为前端 i18n 职责**。决策文本写 `SourceType(source string) (type, label)` 单函数同时返回类型与文案；实际实现把文案留在前端——设置页按 `type` 走 i18n 取 `settings.sourceTypes.<type>`。这是**更合理的分工**（文案不随服务端二进制走），与决策文本不符，故记录在此并按此实现。
2. **决策 5 的「笔记卡片按类型显示 label + 类型色调」→ 已落码**。原先 `NoteSection.tsx` 的 `sourceLabel` 按 source 字符串首字母大写生成 badge（如 `Codex`、`Opencode`），与设置页的「Agent 记忆 / 会话记录」是两套词汇，用户从卡片看不出类别。收口方式：
   - 后端在 `domain.Note` 上加**派生字段** `source_type`（不加 DB 列——决策 4 已定「分类是展示语义」），由 service 层在笔记返回路径上填充，映射函数从包内私有改为导出的 `SourceTypeFor`，成为跨层**单一真值源**（前端不二次派生，避免两处漂移）；
   - 前端新增共享组件 `web/src/components/SourceTypeBadge.tsx`：按类型给 label（走 i18n）与色调（agent-import=info / user-conversation=accent / project-extract=success），原始来源名降为 tooltip（用户仍能看出是 claude 还是 codex），`manual` 不出 badge（手写笔记无来源可标）；
   - 组件自带 5 项测试，钉住「manual 不出 badge / 未知类型不出 badge（不猜）/ 类型决定色调 / 原名只在 tooltip」四条契约。

### P3 定时同步 —— 主体落码，lastRun/lastError 粒度已补齐

已落码：`internal/service/knowledge_sync.go` 全部（ticker 首跳推迟一个周期、`knowledge_sync_interval_minutes` 默认 240 分钟、逐源 `sync_source_<name>` opt-in、claude 默认开其余默认关、`importMu` 与手动导入串行、失败不中断其余源）+ 5 项测试。

**原偏差（全局粒度）已收口（2026-10-22）**：决策 3 要求「在 `SourceStatus` 带上 `lastRun`/`lastError` 供设置页展示上次同步时间与错误」，原先只有全局 ticker 级的 `KnowledgeSyncStatus`（描述整轮），回答不了「哪个源上次出错」。收口方式：per-source 状态落在 **runtime 层**（`sourceEntry.lastRun` + `SourceStatus.last_run/last_error`），而非 service 层 —— 因为手动导入、定时轮次、启动时 auto-import 三条路径**都经由 `TriggerImport`**，记在 runtime 是唯一能看到全部三条路径的地方；在 service 层另存一份就是第二份真相。设置页每个源行内展示本来源上次同步时间与错误，与全局轮次行并存。

### P5 验证与收口 —— 三项全部达成

| 要求 | 实际 | 判定 |
|---|---|---|
| golden-file 回归（ADR-0011 决策 5） | `internal/importers/` 下**七源全部建了 `testdata/` 目录**（28 个样本文件）：claude / codex / codebuddy / opencode / openclaw / hermes / cursor 各源的内联 fixture 全部迁成样本文件 | ✅ 已建立 |
| `go test ./internal/...` | 全绿 | ✅ |
| GUI 真机冒烟（源列表发现 → 定时同步 → 笔记 badge → 设置页 lastRun） | 隔离环境（隔离 HOME + 只读挂真实 `~/.hermes/state.db`）下走通 hermes 源全链路，见下表 | ✅ |

**GUI 真机冒烟实测结果（2026-10-22）**：

| 步骤 | 结果 |
|---|---|
| Probe 探活 | `detected=true`，detail「~/.hermes/state.db：发现会话库」 |
| 首次导入 | `created=1, updated=0, skipped=0` |
| 复跑幂等 | `updated=1`，笔记数不变 |
| 导入内容 | 首条提问 + 末条回复完整保留 |
| 目标项目 | 未设 `hermes_project` → 落「未分类」（既定语义）；设 `hermes_project=lushan` → 落 project_id=41 |
| 定时同步 | `sync_source_hermes=1` + 间隔 1 分钟 → `last_run` 已填、`last_error` 空、笔记数不变（幂等） |
| 设置页 UI | 「插件」标签页 hermes 行：「已发现数据」+「Agent 记忆」chip + 同步开关「同步中」+ detail「~/.hermes/state.db：发现会话库」 |

截图见 `outputs/screenshots/2026-10-22-adr0022-closure/`。

**golden-file 这条曾一度记为「形态未达标」**：ADR-0011 决策 5 的原意是「各源用固定样本文件锁住解析结果」，目的是源格式漂移时立即报警。原先 66 项测试用内联字符串样本 —— 同样能锁住解析结果，同样会在格式漂移时报警，**能力上等价**，差别只在样本与代码是否同处一文件、漂移时 diff 是否更醒目。所以当时记为收口项而非缺陷。

**已收口（2026-10-22）**：七源全部把内联 fixture 迁成 `testdata/` 下的样本文件，共 28 个文件：

| 源 | 样本形态 |
|---|---|
| claude | `testdata/memory/{project.md,notes.txt}` + `testdata/oversize/project.md`（超长文件仍程序生成，避免仓库里躺 1MB 的 'x'） |
| codex | `testdata/rollout.jsonl`（含非 JSON 行）+ `testdata/rollout-envelope.jsonl`（真实 2026-07 形态） |
| codebuddy | `testdata/session/{index.json,messages/*.json}` + `testdata/fork/{index.json,messages/*.json}` |
| opencode | `testdata/{session.json,object-summary.json,notjson.json,empty.json}` |
| openclaw | `testdata/workspace/{SOUL.md,USER.md,notes.txt,sub/nested.md}` + `testdata/vault-roots.md` |
| hermes | `testdata/memories/{MEMORY.md,USER.md,notmd.txt}` + `testdata/sibling/{leak.md,.env}` + `testdata/state.sql` |
| cursor | `testdata/state.sql` + `testdata/workspaceStorage/<32-hex>/workspace.json` ×2 |

两个 SQLite 源（hermes `state.db` / cursor `state.vscdb`）用 **`.sql` dump** 而非二进制 `.db` 文件：二进制 blob 在格式漂移时 diff 里什么都看不出来，而 dump 逐行可读，schema 与行数据本身就是「真实格式」的定义。为此在 `internal/importers/imptest` 加了两个共享辅助 —— `CopySample`（文件或目录树）与 `ExecScript`（一次 Exec 灌多语句）；跨包共享是为了避免同一段复制逻辑在七个包里各自漂移。

样本同时**保留了负例**：每个源的样本目录里都躺着「必须被忽略」的文件（openclaw 的 `sub/nested.md`、hermes 的 `sibling/leak.md` 与 `sibling/.env`、cursor 的未知 workspace、codex 的非 JSON 行），让 allowlist 与容错行为也被样本锁住，而不只是锁住「能解析出内容」这一半。

### 收口清单（按优先级）

1. ~~**P4 决策 5 笔记侧**：笔记卡片改按 `SourceType` 分类显示（label 走 i18n、按类型给色调），与设置页口径一致~~ —— **已收口（2026-10-22）**：后端下发 `source_type` + 前端共享 `SourceTypeBadge` 组件 + 5 项测试；
2. ~~**P3 lastRun/lastError 粒度**：`SourceStatus` 补 per-source 的 `last_run` / `last_error`~~ —— **已收口（2026-10-22）**：落在 runtime 层，设置页逐源展示；
3. ~~**P5 golden-file**：八源各建 `testdata/` 目录，把内联 fixture 迁成样本文件~~ —— **已收口（2026-10-22）**：七源全覆盖，28 个样本文件，两个 SQLite 源用 `.sql` dump；
4. ~~**P5 GUI 真机冒烟**：源列表探活 → 开启某源定时同步 → 笔记 badge → 设置页 lastRun 四步走查~~ —— **已收口（2026-10-22）**：隔离环境下 hermes 源全链路走通，见上表与截图。

四项收口项全部关闭，「来源类型对用户可见」闭环达成。ADR 状态仍维持 Proposed —— 方案本身仍待评审，但**实施侧已无遗留项**：P1–P5 的落码边界与本 ADR 所述一致，P3/P4 的三处偏差已按实测修正并记录。（**2026-10-23 注**：此为晋升前结语；方案已于同日经用户评审晋升 Accepted，以状态行为准。）

## 补注（2026-11-23）

待决②（搜索/列表按来源类型筛选）由 [ADR-0072](0072-search-transparency-recall-provenance.md) 关闭：SearchHit 增加可选 `source_type`，知识搜索面以客户端筛选 chips 落地；待决③（物化列）继续搁置，触发条件不变。
