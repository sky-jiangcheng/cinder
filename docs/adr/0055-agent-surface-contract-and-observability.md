# ADR-0055: Agent 交互面统一契约与可观测性——四维审计加固

状态：**Accepted（2026-11-11 拍定方向；2026-11-12 P0（D1/D2）、P1（D3/D4）、文档契约门（12.4）与 D5 的免 schema 部分均已落地；D5 剩下的幂等键 / 乐观锁 / 版本历史需要 schema v31，已单独立 [ADR-0057](0057-write-idempotency-and-version-history.md)，跟踪 TODO 12.14。详见文末「落地记录」）**
日期：2026-11-11
关联：[ADR-0048](0048-core-contract-version.md)（契约版本语义与漂移门）、[ADR-0051](0051-conversation-as-interface.md)（工具面是统一操作层）、[ADR-0054](0054-rolling-checkpoint-handoff.md)（交接的读写闭环）、[ADR-0056](0056-desktop-dialogue-persistence.md)（同日修的桌面对话历史）
起始 schema：v30 → **v30（无 schema 变更）**；`CoreVersion` 1.5.0 → **2.0.0（2026-11-12 已生效）**——D1 改变客户端判定失败的方式，属 `core.go` 定义的 major，不是 additive

## 背景

2026-11-11 对 Cinder 做了一轮「契约 / 记忆 / 观测 / 容错」四维只读审查（22 个 MCP 工具、三端入口、DB 与后台任务全量走查）。四个维度不是四个独立问题，**是一条链上的四个断点**：出口契约不统一 → 错误被当成功 → 没有打点 → 静默。

### 问题一：契约明确，但不一致

- 工具面是单点的：`cmd/mcp/main_test.go:138-161` 钉死 22 个工具 + `len()` 断言；`internal/version/core.go:19` 的 `CoreVersion = "1.5.0"` 有双份拷贝与 CI 门。
- 但 `cmd/mcp/results.go:34-44` 的 `makeJSONResult(name, data)` 里 `name` **是从未使用的死参数**，返回裸 JSON，无 `ok` / 错误码 / request id；`tools_metrics.go:27` 与 `tools_chat.go` 用 `mcp.NewToolResultError`（`IsError=true`），其余 20 个工具用 `makeTextResult("error: " + ...)`（`IsError=false`，如 `tools_ask.go:83`）。同一个客户端要写两套解析。
- 版本门只比两份字面量相等（`scripts/check-version-drift.mjs:404-416`），`core.go:13-15` 写的 major/minor 语义**无任何代码校验**；ADR-0051 声称的「两张面对齐断言」在脚本里不存在。
- 文档漂移未被门覆盖：`README.md:143` / `README.zh-CN.md:146` 仍写 20 个工具；根 `SKILL.md:101-102` 列了不存在的 `offset?` / `starred_only?`；`skill/repotory/references/mcp-tools.md:301,307,312-314` 四组 MCP↔绑定映射写错；`docs/api/reference.md:248` 自承手工 openapi.json 已移除、无机器可读契约。
- 参数边界三种写法：`tools_scope.go:25-27` 超长 `path` 静默 `return 0`，`tools_context.go:64-66` 显式拒绝，`main.go:157` 的 `notes_search.path` 不校验；`Required` 仅 7/22。

### 问题二：记忆不会物理丢失，但会逻辑丢失

- 全部记忆落 SQLite、每日 `VACUUM INTO` 快照 + `VerifySnapshot`（`internal/db/backup.go:50-105`），保底成立。
- `internal/service/handoff.go:29-33` 允许写 `summary 4000B + 4 节 × 20 条 × 1000B`（≈84KB），而 `internal/service/context.go:20` 的 `contextHandoffFullLen = 4000` 截断；模板顺序是 Changes → Decisions → **Gotchas → Next Steps**（`handoff.go:320-323`），恰好先切掉避坑与待办。`context.go:17-19` 的注释却写「must survive complete」。
- `context.go:270-288` 只全文渲染 `updated_at` 最大的一条交接，其余压成 140 字时间线，**无条数上限、不按时间排序**；`context_test.go:129-177` 把「旧 gotcha 消失」钉成了断言。
- SessionEnd hook 是 `repotory-capture >/dev/null 2>&1 || true`（`internal/agentinit/agentinit.go:387`），`cmd/repotory-capture/main.go:53-59` 出错只 stderr + exit 0，`claude_session_capture` 默认关闭；开启后自动捕获的低质量交接还会顶替人工交接成为「最新全文」。
- 只有分段硬编码常量（notes 10 / 交接 4000 / 时间线 140），**无全局预算、无配置项、无 "N omitted"**；导入器 4h sync 无条件 `UpdateNoteFull` 刷新 `updated_at`，`kind=log` 笔记会挤掉 curated 知识。
- 回滚不完整：`internal/db/migrate.go:169-173` 的版本触发器存 `new.content` 且是 `AFTER UPDATE`（创建态永不入历史）；全仓无 `RestoreBackup`；`internal/integrity` 只读不修；写路径无乐观锁。

### 问题三：观测几乎为零

- 全仓 223 处 `log.Printf`、**0 处 slog**；唯一的 `log.SetOutput` 在 `main.go:233`（仅 Wails 桌面端，`O_APPEND` 无轮转无限额）。`cmd/mcp` 与 `cmd/server` 只写 stderr，IDE 不捕获即全丢；stdout 协议隔离靠上游库默认行为，仓内无一行注释或测试钉住。
- 无 request / trace / correlation id；`internal/httpapi/server.go:45-59` 的 `New()` 只包 `loopbackGuard`，无访问日志、无耗时、无 recover；`cmd/mcp/` 下除 main 外无任何 `log.` 或 `time.Since`，22 个工具零打点。
- `/health` 仅 `db.Ping()`（`internal/service/service.go:317-331`），无 `/metrics`；`internal/stats` 是 git 提交业务指标，`repotory_integrity` 是按需只读体检，`ai_call_log` 默认关闭且无耗时/成败字段。
- 备份、LLM 编译、导入失败一律只 `log.Printf`，无告警、无失败计数；`docs/troubleshooting.md:71-79` 只给桌面日志路径，`:83` 与 headless server / S3 推送矛盾。

### 问题四：数据层容错扎实，Agent 面不优雅

- 已落地：WAL + `busy_timeout=5000` + 15s 退避（`internal/db/db.go:20-38,173-192`）、`cmd/server/main.go:186-206` 六次开库重试、AI 超时与 localhost 回退、向量库失败回退 FTS5 且「never reducing results」（`internal/service/search_semantic.go:63-120`）、迁移单事务可重入、LLM Wiki 一律 pending + 墓碑、HTTP 状态码分类。
- 断在边界：`internal/service/search.go:49-53` 把 DB 错误 `log.Printf` 后 `return nil` → `cmd/mcp/main.go:160` 序列化成 `"null"` 且 `IsError=false` → HTTP `200 {"hits":null}`；`tools_ask.go:94-100` 把 `nil` 显示成 `No results found`。**故障被当成零命中**。
- 部分成功报全成功（`internal/service/wiki_compile_job.go:223-245` 无条件 `CompileJobDone`；`internal/core/plugin/runtime/runtime.go:303-376` 累积 `firstErr` 仍 `return run, nil`）。
- 待审队列有旁路：`internal/service/wiki_evidence.go:588` 的 `FileAnswerAsPage` 走 `db.CreateWikiPage` → `internal/db/wiki.go:723-726` 直接 `WikiStatusApproved, "manual"`，模型文本未经审核即可检索。
- 无统一错误码，分类靠 `strings.Contains`（`cmd/server/main.go:210`、`web/src/utils/apiError.ts:15-23`）；handoff 无幂等键；MCP 开库无锁重试（与 server 不对称）；embedding drainer 每 5s 无限重试无退避。

## 决策

### D1 统一 MCP 结果契约，失败必须机器可判

> **落地修正（2026-11-12）**：**成功面冻结**——22 个工具的成功返回仍是裸 JSON / 原文文本，因为 Warden 与两个 IDE 插件已在解析它，同批改三个解析器不属于这次缺陷修复。机器可读的 `{code, message, retryable, request_id}` 放进 MCP 标准的 `StructuredContent`，`Content[0]` 保留原来的 `"error: …"` 文本（现有文本断言与客户端显示零改动）；失败一律 `IsError=true`；`makeJSONResult(name, data)` 的死参数 `name` 直接删除。因此 envelope 只落在失败面，代价见「代价」段。

`cmd/mcp/results.go` 收成**单一失败出口**：`makeErrorResult` / `makeInvalidResult` 产出 `IsError=true` 的结果，`StructuredContent` 带 `{code, message, retryable, request_id}`，`Content[0]` 保留原来的 `"error: …"` 文本。删掉 `makeJSONResult` 的 `name` 死参数与全部 in-band 错误分支。错误码至少区分 `invalid_argument` / `not_found` / `retryable` / `internal`，让 Agent 能据此决定改参数还是重试。`request_id` 同时进返回与结构化日志（D4）。**成功面冻结**，理由见本节开头的落地修正。

**契约版本走 major**：`IsError` 从 `false` 变 `true` 改变的是客户端判定失败的方式，按 `core.go:15` 属「字段含义变」，落地时 `CoreVersion` `1.5.0 → 2.0.0`，同步 `cmd/mcp/main_test.go` 的 `wantCoreVersion` 与 `TestToolsRegistered` 断言，不改任何工具名、不删工具。

### D2 读错误冒泡，禁止把故障伪装成空结果

`Service.SearchAll` / `SearchNotes` 改为返回错误（或提供带 error 的变体），MCP / HTTP 出口在 error 时用 `IsError=true` / 5xx；`internal/service/project_overview.go:73-74`、`handoff.go:225-226` 同类吞错一并处理。**零命中必须是显式空数组，不能是 `nil`**。

### D3 上下文预算与交接保真

- `BuildProjectContext` 加一个总预算常量 + 配置项，按「最新交接 > Gotchas / Next Steps > 当前笔记 > 历史时间线 > mined meta」优先级装配，超预算打印 `N notes / M handoffs omitted`。
- 修最新交接截断：按小节优先保留 Gotchas / Next Steps 再截，或把 `contextHandoffFullLen` 提到与最大合法交接同量级；区分 `auto-captured` / `milestone` 与人工 session summary，不让自动捕获无条件顶替。
- 历史时间线设条数上限并按 `updated_at DESC` 排序；`content[:1200]` 改 `truncateBytes`。
- SessionEnd hook 去掉 `>/dev/null`，失败写 durable pending 并在下次 `repotory_context` 提示；项目解析从 `LastPathSegment` 换成 `ResolveProjectByPath`，多命中宁可不写。
- 检索对 `handoff` tag 加权，snippet 至少回整条 bullet；导入 upsert 加内容哈希，内容未变不刷新 `updated_at`。

### D4 三进程统一日志 + 关联 id + 打点

- 抽共用 `setupLogging`：Wails / `cmd/mcp` / `cmd/server` 各写独立文件，`slog` + JSON handler + 按大小轮转；MCP 显式固定 stdout 只给协议并加测试。
- HTTP 加中间件生成 `X-Request-ID`（回写 header + 注入 ctx + 访问日志 + recover）；MCP 复用 JSON-RPC id 作为 tool call id；`AddTool` 包一层 wrapper 统一打 tool 名 / 耗时 / 成败 / 错误摘要；LLM 与向量库调用把 id 带进日志。
- `/health` 扩出 `db_size_bytes` / `last_backup_ok,at` / `compile_jobs_failed_24h` / `llm_calls_failed_24h` / `search_p95_ms` / `import_failed_by_source`，从现有表汇聚，不新建管线。备份 / 编译 / 导入失败除日志外写一条可见事件。

### D5 状态与回滚补齐

`makeJSONResult` 编码失败也走 D1 的错误出口；批量任务带 `partial` / 失败数；`repotory_notes_create` / `repotory_handoff` 加可选 `client_request_id` 与 DB 唯一键；写路径加 `WHERE version=?` 乐观锁；`note_versions_snap` 改存 `old.content` 并补创建态；提供 `RestoreBackup`；`FileAnswerAsPage` 改 pending + `source="ai"`；drainer 加退避与死信；MCP 复用 server 的 `openDatabase` 重试。

### 这个 ADR 明确不做的事

1. **不新增 MCP 工具、不改工具名、不动标识层** `repotory_*`（ADR-0049 冻结）。
2. **不改 schema**（D5 的唯一键与版本号若最终需要列，另开 ADR 并走迁移）。
3. **不引入分布式追踪 / 外部 APM / 新框架**：本地优先、零账号是产品前提；结构化日志 + 一个关联 id 已能覆盖单机三进程。
4. **不把 `chat_threads` 当桌面 UI 状态**（见 ADR-0056）。
5. **不重写 service 层**：本 ADR 是出口、预算与观测的收口，不是分层重构（延续「不写新架构」的 P0 原则）。

## 代价

- **major bump 是真实破坏**：靠 `IsError=false` + 文本前缀判定失败的客户端都要改（成功面与错误文本本身一字未动，Warden / 两个 IDE 插件的解析零改动）。不能宣称无感。
- **失败面多了结构化元数据**：只有失败结果多一个 `structuredContent`，成功结果不变；工具 description 要说清 `request_id` 是元数据、不是答案的一部分。
- **日志落盘带来磁盘与隐私面**：MCP / server 从此有持久日志，必须轮转、限额，且不得把笔记正文 / API key 写进去（`getConfig` 的掩码规则同样适用于日志）。
- **历史交接保真会增加首屏体积**：总预算与 `omitted` 标注是硬要求，否则「修好了丢失、换来了挤爆」。
- **乐观锁与唯一键改变写语义**：并发编辑从「静默后写覆盖」变成「后写收到冲突」，UI 要能提示，属于行为变化。

## 落地清单（分批，每批结束跑守门）

**P0（契约 + 容错，先做）**：D1 的 `results.go` / 全工具错误出口 / `main_test.go` 断言 / `CoreVersion` 2.0.0 / `check-version-drift` 语义门；D2 的读错误冒泡。
**P1（观测 + 记忆）**：D4 日志底座与 wrapper、D3 预算与交接保真、hook 去静默、`/health` 扩字段、`docs/troubleshooting.md` 三进程路径。
**P2（状态与回滚）**：D5 幂等 / 乐观锁 / `RestoreBackup` / 待审旁路 / drainer；文档契约纳入 CI 硬门（README、SKILL、mcp-tools、api reference 的机器生成比对）。

## 验收（全部可测）

1. 任一工具在 service 返回 error 时，`IsError=true` 且 `error.code` 属于约定集合；参数错误与存储故障返回不同 code。
2. 让 `db.SearchAll` 返回错误，MCP 与 HTTP 均不得出现 `null` / `200 {"hits":null}`。
3. 长交接（含 Gotchas / Next Steps）经 `BuildProjectContext` 后两节仍可读；总输出不超预算且带 `omitted` 计数。
4. SessionEnd hook 失败时留下 pending 记录，下次 `repotory_context` 能看到提示。
5. 一次 MCP 工具调用与外部 LLM 调用日志中的 `request_id` 相同；`cmd/mcp` / `cmd/server` 落盘文件存在且 stdout 无协议外输出。
6. `go test ./...`、`npm test`、`npm run build`、`check-docs-mirror`、`check-i18n-keys`、`check-version-drift`、`release-notes --check` 全绿。

## 落地记录（2026-11-12）

**P0（2026-11-12）**：`cmd/mcp/results.go` 收成单一失败出口（`makeErrorResult` / `makeInvalidResult`，`StructuredContent` 带 `code / message / retryable / request_id`），20 处 `makeJSONResult(name, …)` 去掉死参数、22 个工具的失败分支全部 `IsError=true`；`CoreVersion` 1.5.0 → **2.0.0**，`cmd/mcp/main_test.go` 同步并新增 `TestToolErrorIsMachineReadable`；`scripts/check-version-drift.mjs` 新增第⑨门，用 `CoreVersionPrevious` / `CoreVersionChange` 校验「声明的变更类型」与「版本号实际走了多远」一致（工具清单 diff 的自动判定仍属 12.4）。`Service.SearchAll/SearchNotes` 改为 `([]SearchHit, error)`，MCP / HTTP 出口分别用 `IsError=true` / 500，零命中返回显式 `[]`；Wails 绑定保留单返回值但改为 log + 空数组（**残留**：桌面 UI 仍拿不到错误通道，登记 12.13）。

**P1（2026-11-12）**：`internal/platform/logging.go` 共用 slog JSON 底座 + 按大小轮转，三进程各写独立文件，MCP 的 stdout 约束有测试钉住；`internal/httpapi/middleware.go` 提供 `X-Request-ID` / 访问日志 / recover；`cmd/mcp` 用 mcp-go 的 `WithToolHandlerMiddleware` 零侵入打点 22 个工具；`Health()` 扩 `db_size_bytes` / `compile_jobs_failed_24h` / `last_backup_at` 三个现读指标。`internal/service/context.go` 加 32000 字总预算与 `omitted` 标注、Gotchas / Next Steps 优先保真、时间线上限 10 且按时间排序、CJK 安全截断、自动捕获不再顶替人工交接；`internal/platform/capture_pending.go` + `cmd/repotory-capture` 写 durable pending 并在下次 context 提示；`internal/agentinit/agentinit.go` 的 hook 去掉 `>/dev/null`，两份 `repotory-init/index.mjs` 与 `contract.json` 同批同步、fixture 恢复严格逐字比较；项目解析换成 `ResolveProjectByPath`。

**同批**：TODO 12.2 / 12.3 / 12.6 / 12.7 / 12.8 勾选，12.11 由「待拍板」改为「已拍板 B」；CHANGELOG `[Unreleased]` 补条目。

**明确未落地**：D5（幂等 / 乐观锁 / `RestoreBackup` / 待审旁路 / drainer 退避）与 12.4（文档契约门）、12.5（参数边界统一）、12.9、12.10、12.12 仍留在 TODO。不写「已落地」。
