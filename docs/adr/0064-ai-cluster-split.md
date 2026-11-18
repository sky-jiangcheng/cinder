# 0064 `ai` 集群拆分——传输层下沉 `internal/aiclient`，工具循环与编排留在 `Service`

- 状态：**Accepted（2026-11-18 起草、复核并同日落码）**——第一刀已实现并验证，实测数据见「落地记录」
- 相关：[ADR-0060](0060-service-domain-split.md)（按域绞杀者抽包；收口判据「依赖面小且稳定才搬」，并把 `ai` 列为下一个压缩目标）、[ADR-0055](0055-agent-surface-contract-and-observability.md)（工具面契约与观测，本 ADR 不动）

## 背景

ADR-0060 的收口记录把 `evidence` 判为编排层、留在 `Service`，并指向下一步：压缩 `ai` 集群。本轮实测：

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/service/ai.go` | 1,042 | AI 配置、HTTP 原语、SSE、provider 探测、Ask 编排 |
| `internal/service/aitools.go` | 1,073 | tool registry、工具实现、两条 tool-calling 循环 |

这 2,100 行不同质，先按「对 `Service` 的引用」排一遍再决定切哪。

## 事实一：传输半是 s-free 的，但不止「7 个类型」

按函数体是否引用 `s.` 统计，**20 个函数 / 611 行（含注释）**完全不碰接收者：

```
aiCandidateURLs / nextAICandidate / aiLocalhostFallback /
aiProxyFunc / aiProxyFuncWith / isLoopbackHost     ← URL 与代理解析
aiGet / aiPostJSON / aiPostJSONStream / parseSSEStream /
aiNetworkHint                                       ← HTTP 原语与 SSE 解码
parseAIModels / ListAIModels / TestAIChat /
TestAIChatWith / TestAIChatWithTimeout /
testAIChatWithTimeout / streamChatWithTimeout       ← provider 探测与无审计直连
aiFinishReason / chatResponse.providerError         ← 响应与错误信封
```

**逐符号复核后，随迁清单比初版估算大**：

| 类别 | 符号 | 出处 | 为什么必须一起走 |
|---|---|---|---|
| 类型 ×10 | `chatMessage`、`chatRequest`、`chatResponse`、`chatErrorEnvelope`、`chatStreamRequest`、`streamDelta`、`finishReasons`、`toolCall`、`AIModelListResult`、`AITestResult` | `ai.go:113/124/134/150/183/199/484/231/237`、`aitools.go:33` | `chatMessage.ToolCalls` 是 `[]toolCall`，`toolCall` 在 `aitools.go`——不走就成环；两个 Result 类型是 `ListAIModels` / `TestAIChat*` 的返回值 |
| 包级符号 ×3 | `aiChatTimeout`（90s）、`aiProbeTimeout`（15s）、`aiHTTPClient` | `ai.go:38/72/324` | 传输函数直接引用；`aiHTTPClient` 的 5 处引用全在传输段内 |
| 辅助函数 ×1 | `truncateBytes` | `handoff_capture.go:235`（`wikigen/seam.go:162` 另有一份） | `providerError` 与 4xx 报错都截断消息体；`aiclient` 自带一份 6 行实现，不回依赖 `service` |

**不在切片内**：`truncatedHint` 只是 `wikigen.TruncatedHint` 的 wrapper，搬走会让传输包反向依赖 wiki 词汇，与 ADR-0060 的依赖方向记录冲突。

## 事实二：工具循环的耦合不在循环里，在工具实现里（2026-11-18 复核修正）

初版把工具循环写成「消费六个未搬走的域」。逐行复核后这个说法不准确：

| 循环 | 行数 | `s.` 引用 |
|---|---|---|
| `askWithTools` | 116 | **1 处**（`s.toolRegistry.call`） |
| `streamAskWithTools` | 141 | **1 处**（同上） |

审计（`recordAICall` / `aiReviewInputText` / `resolveAuditConfig`）在 `chatWithAudit` 里，在循环**之前**；记忆（`BuildLayeredMemory`）同理；检索只在 `toolSearchNotes` 这个**处理函数**里（1 处 `SearchAll`）。也就是说六个依赖里四个属于 handler，不属于 loop mechanics。

循环真正缺的是一个能自洽的边界，而它就在 `aitools.go` 内部——**第三份传输与第三份 wire 类型**：`aiPostJSONToolStream` + `parseSSEToolStream`（64 行，s-free）重复了 `aiclient.PostJSONStream` / `ParseSSEStream` 的职责，`toolChatRequest` / `toolChatResponse` / `streamToolDelta` 又重复了 OpenAI 的请求/响应形状（只为多一个 `tools` 字段与 `tool_calls` 增量）。

于是顺序也修正：**先合并传输与 wire（无行为变化），再搬 registry + 循环**，而不是等 `search` / `context` / 审计先成域。三者作为「域」的实测成本是：

| 候选域 | 行数 | `Service` 字段依赖 |
|---|---|---|
| `search.go` | 109 | `db`、`fuseSemantic`（几乎现成） |
| `context_layers.go` | 458 | 9 处：`db`、`SearchNotes`、六个 `layer*` helper、`aiProjectBaseContext` |
| `ai_audit.go` | 309 | 7 处：`db`、三个开关、`auditForwarder`、`auditSurface` |

三者各自是一个独立刀口的量级；把它们设成 257 行循环的前置条件，收益/成本倒挂。

## 事实三：传输层在别处已有一份复制

`internal/httpapi/guard.go:42` 自带一个 `isLoopbackHost`，与 `ai.go:375` 语义相同（前者把裸 `::1` 当整体解析，后者先 `SplitHostPort`，结果等价）。传输下沉到 `internal/` 顶层后，`httpapi` 可以直接复用，这一份复制应删除——**这是本刀最实的收益之一，也是包位置的判据**。

## 决策

**位置：`internal/aiclient`（internal 顶层），不是 `internal/service/aichat`。**

`reviewrun` / `wikipage` / `wikigen` 是业务域，`aiclient` 不是——它是 HTTP/SSE 客户端。放 `internal/service/` 下虽然编译无环，但会迫使 `httpapi` 去 import「service 的子包」才能复用 loopback 判定，读作跨层；放顶层则 `service` 与 `httpapi` 都是它的平级消费者。

1. **搬入 20 个函数 + 10 个类型 + 3 个包级符号 + `truncateBytes` 副本**，导出名为 `CandidateURLs` / `NextCandidate` / `Get` / `PostJSON` / `PostJSONStream` / `ParseSSEStream` / `FinishReason` / `Models` / `TestChat*` / `StreamChatWithTimeout`，类型为 `Message` / `ToolCall` / `Request` / `Response` / `ErrorEnvelope` / `StreamRequest` / `StreamDelta` / `FinishReasons` / `ModelListResult` / `TestResult`。
2. **`providerError` 改为导出方法 `Response.ProviderError()`**。Go 不允许给外部包的别名类型定义方法，未导出方法也跨包不可见——service 侧只能留自由函数 `providerError(cr chatResponse)` 转发。**这是唯一需要动的既有测试**（`ai_provider_error_test.go:56`，一行）。
3. **`internal/service` 只留一次性脚手架，同日已拆**：搬迁当轮用 `type chatMessage = aiclient.Message` 等 10 个别名 + 19 个同名转发函数让调用点与 `ai_*_test.go` 零改；随后把 `ai_url_test.go` / `ai_stream_test.go` / `ai_provider_error_test.go`（纯传输部分）迁进 `aiclient`，脚手架整体删除，service 直调 `aiclient.X`。`aiChatTimeout` / `aiProbeTimeout` 的两处引用同样改为 `aiclient.ChatTimeout` / `aiclient.ProbeTimeout`。
4. **配置、审计、Ask 编排、工具循环、`truncatedHint` 留在 `Service`**：`aiChatConfig` 读 `s.db` 的 `ai_*` 键，`chatWithAudit` / `streamChatWithAudit` 写 ADR-0030 审计行，二者都是 `Service` 的形状，传输入口只收 `(baseURL, apiKey, model, messages, timeout)`。
5. **删除 `httpapi/guard.go` 的 `isLoopbackHost` 复制**，改调 `aiclient.IsLoopbackHost`，全库只剩一处定义。

## 行数账（修正后的口径）

- `ai.go` 删 611 行；service 侧回加约 70 行 wrapper + 12 行别名，**净减约 530**（初版写的 -600 是漏了 wrappers）。
- `httpapi/guard.go` 再减 13 行复制；`internal/aiclient` 新增约 620 行。
- 这是**文件体积与依赖方向**收益，不是 API 面收益：service 的导出方法数不变（ADR-0060 已记过同一代价）。真实增量收益是「传输层可被 service 以外的包复用」——loopback 复制即时消掉，SSE 解析后续可复用。

## 验收判据

1. `gofmt -l` 无输出；`go build ./...` / `go vet ./...` / `go test ./...` 全绿。
2. `go list -deps ./internal/aiclient` 不含 `repotory/internal/service`、`repotory/internal/db`。
3. **请求字节等价**：搬码前用临时测试从现实现抓取 `method / path / Authorization / Content-Type / body` 存成 golden，搬码后用 `aiclient` 跑同一组用例逐字段比对，必须一致（含 `localhost → 127.0.0.1` 重试那条路径）。
4. 既有 `ai_*` 测试除 `ai_provider_error_test.go` 一行外零改即通过。
5. `isLoopbackHost` 全库只剩一处定义；MCP 22 工具 fixture、`CoreVersion`、绑定面、schema 全部不动。

## 明确不做

- 不搬工具循环、不搬 Ask 编排、不引入 port 层。
- 不改工具名 / 参数 / 返回结构，不改 `CoreVersion`，不动 ADR-0055 的观测面。
- 不把 `truncatedHint` 或 ADR-0030 审计行搬进 `aiclient`。

## 落地记录（2026-11-18）

按上表落码，与提案的差异只有一处：`testAIChatWithTimeout` 在 `aiclient` 里导出为 `ProbeChatWithTimeout`（service 侧名不变），其余符号一一对应。

| 指标 | 实测 |
|---|---|
| `internal/service/ai.go` | 1,042 → **358** 行 |
| `internal/service/aitools.go` | 1,073 → **1,064** 行（`toolCall` 迁出） |
| `internal/service/ai_transport_facade.go` | 新增 **95** 行（10 别名 + 19 wrapper + providerError 自由函数 + 2 const） |
| `internal/aiclient/aiclient.go` | 新增 **715** 行（含注释） |
| `internal/httpapi/guard.go` | 44 行，`isLoopbackHost` 只剩一处定义（委托 `aiclient.IsLoopbackHost`） |
| `go list -deps ./internal/aiclient` | 无任何 `repotory/internal` 依赖 |

请求字节等价用的是搬码前抓的 golden：`internal/aiclient/testdata/aiclient_golden.json`（3 个请求的 method / path / Authorization / Content-Type / body，含流式那条），`TestWireShapeMatchesGolden` 在搬后逐字段比对通过。`ai_provider_error_test.go` 按预期只改了 `cr.providerError()` → `providerError(cr)` 一行；`ai_stream_test.go` / `ai_url_test.go` / `aitools_test.go` 零改即绿。

### 第二步：脚手架拆除（同日完成）

- 三个白盒测试迁进 `internal/aiclient`（`url_test.go` / `stream_test.go` / `provider_error_test.go`），符号按导出名改写，13 个用例全绿。
- `provider_error_test.go` 里两个端到端用例（`TestAskAISurfacesProviderError`、`TestAskAIKeepsNoChoicesWordingWhenThereIsNoErrorEnvelope`）**拆回 `internal/service`**（`ai_provider_error_e2e_test.go`）：它们断言的是 `Service.AskAI` 的错误措辞，放进 `aiclient` 会把 service 反向引入，直接成环。
- `internal/service/ai_transport_facade.go` 整个删除（95 行）：10 个别名、19 个 wrapper、`providerError` 自由函数、2 个 const 全部消失；`ai.go` / `aitools.go` / `ai_audit.go` / 各测试直调 `aiclient.CandidateURLs` / `aiclient.PostJSONStream` / `aiclient.Message` 等。
- `internal/app` 的 `ListAIModels` / `TestAIChat` 绑定改调 `aiclient.ListModels` / `aiclient.TestChat`（App 方法名与绑定面不变）。

拆除后行数：

| 文件 | 行数 |
|---|---|
| `internal/service/ai.go` | **359** |
| `internal/service/aitools.go` | **1,065** |
| `internal/aiclient/aiclient.go` | 715 |
| `internal/aiclient/*_test.go` | 410 |

`ai` 两个文件合计 2,115 → 1,424（净减 691），且 service 内不再存在任何与 `aiclient` 同名的转发函数。

### 复核中顺手修掉的缺陷（2026-11-18）

两条循环原本都写成 `ctx, cancel := context.WithTimeout(context.Background(), timeout)`：**遮蔽了调用方 ctx**，且 `cancel()` 之后又把同一个 ctx 交给 `s.toolRegistry.call` —— 于是所有 ctx 感知的工具（含检索）都收到一个已取消的 ctx。上一轮 ctx 贯穿落地后，这个潜伏问题才变成真错误（db 层开始用 `QueryContext`）。已改为从调用方 ctx 派生 `callCtx` 只给 HTTP 用，handler 拿原始 ctx；新增 `tools_ctx_test.go` 两例（handler 拿到活 ctx / 调用方取消不再发出请求）作为回归门。

### 第二刀（2026-11-18）：工具循环合流

按复核结论，先消除 `aitools.go` 内的重复，再搬循环：

1. **传输合并**。`aiclient` 增加 `ToolDefinition` / `ToolCallDelta`，`Request.Tools`、`Response` choice 的 `ToolCalls`、`StreamDelta.Delta.ToolCalls`；`ParseSSEStream` 拆成 `parseSSE(r, onChunk)` 核心 + 内容包装 + `ParseSSEToolStream`；新增 `PostJSONToolStream`（返回 `int, FinishReasons, error`，与 `PostJSONStream` 同形）。service 里的 `aiPostJSONToolStream` / `parseSSEToolStream` / `toolChatRequest` / `toolChatResponse` / `streamToolDelta` / `streamToolChoice` 全部删除。
2. **循环另立包**。新建 `internal/aitools`：`Registry`（`Register` / `Definitions` / `Call`）、`Provider{BaseURL,Model,APIKey}`、`MaxRounds = 5`、`Ask` / `AskStream`、内联工具调用解析（`InlineToolCalls` / `ContentWithoutInlineCalls`）。seam 只有一个 `*Registry` 值，没有为 handler 造接口层。
3. `internal/service/aitools.go` 只剩工具实现与注册函数（`registerDashboardTools` / `registerDashboardToolsExtra`）以及 `QueryProjectStatsBatch`，由 1,065 行降到 520 行；`ai.go` 359 → 366（新增 `aiChatConfig.provider()`，把配置收敛成循环要的三个字段）。

等价性用请求体 golden 保证：`internal/aitools/testdata/tool_loop_golden.json` 保存搬动前抓到的两次请求原文，`internal/service/tools_golden_test.go` 在搬后重放同一轮工具调用并逐字节比对 —— 字段顺序 `model → messages → tools` 与 `tools` 数组内容完全一致。

验证：`gofmt -l` 无输出，`go build ./...`、`go vet ./...` 通过，`go test ./...` 50 包全绿（含 `TestToolLoopRequestBodyGolden`、`TestAskWithToolsHandlersSeeLiveContext`、`TestAskWithToolsRespectsCallerCancellation`）。

遗留（有意保留）：`PostJSONToolStream` 仍是「任何错误都回退 localhost」，`PostJSONStream` 仍是「仅传输错误回退」。这是搬动前的既定行为，统一与否是另一个决定，已在函数注释里写明。

### 复核：`search` 刀口的真实成本（2026-11-18，暂缓）

上一版把 `search.go` 标成「109 行、只依赖 `db` + `fuseSemantic`、几乎现成」。按 `s.` 计数它确实只有 1 处引用，但**引用的是 `fuseSemantic`，而这个词会把整条向量检索链拖进来**：

| 归属 | 行数 | 被谁依赖 |
|---|---|---|
| `search.go`（三个 Search 方法 + 格式化） | 109 | app / httpapi / cmd/mcp / cmd/abeval / cmd/abevaldiag / cmd/searchbench，共 12 处调用 |
| `search_semantic.go`（`semanticEnabled` / `noteEmbedder` / `vectorStore` + 缓存 / `invalidate` / `RebuildEmbeddings` / `fuseSemantic`） | 240 | `embed_drain.go`（后台增量索引）、`config.go`（改配置失效缓存）、`wiki_graph_evidence.go` 的 `pageVectorRecall`、4 处 `RebuildEmbeddings` 调用 |
| `vectorStoreCache` 字段 + 相关测试夹具 | — | `vector_store_cache_test.go` / `embed_drain_test.go` / `page_embed_drain_test.go` 直接读写 `svc.vecStore` |

也就是说，它横跨「检索 / 后台索引 / 页面召回 / 配置」四个域，动它要碰 **14 个测试文件**。这不是 109 行的位移，而是一次跨域重构；目前收益（service 少 349 行）与风险不匹配，**暂缓**，等出现第二个必须共用向量存储的域时再按「embedding runtime 先成包」的顺序做。

### 第三刀（2026-11-18）：audit 拆出 `internal/aiaudit`

与 `search` 相反，审计的依赖是干净的两件进程级状态加一个存储句柄，实测后直接落地：

- `internal/aiaudit`：`Recorder{db, surface, forwarder}` + `Policy`（`Enabled/ReviewInput/ReviewOutput/Rules`）+ 纯函数 `PromptText/PromptHash/MatchRule/Entry` + `BlockedError` + `Verdict*` / `Surface*` 常量。`Resolve()` 一次读四个 config 行，`RecordCall()` 保留「审计失败只记日志、绝不打断模型调用」的原规则。
- `Service` 不再持有 `auditSurface string` 与 `auditForwarder func` 两个散字段，改为一个 `Audit *aiaudit.Recorder`（`NewWithDeps` 里构造），外部调用点从 `svc.SetAuditSurface(...)` 改为 `svc.Audit.SetSurface(...)`。
- 涉及 8 个文件、55 处引用：`ai.go`（两个 audited 入口）、`ai_audit_query.go`（读侧）、`writerapi`（写目录 + forwarder）、`main.go` / `cmd/server` / `cmd/mcp` / `cmd/repotory-capture`（启动时声明 surface）、4 个测试文件。

行数：`internal/service/ai_audit.go` 309 行删除，`internal/aiaudit/aiaudit.go` 333 行（含包注释与 `Recorder`/`New`/`Policy` 头）；`ai.go` 366 → 367。验证：`go build`、`go vet`、`gofmt -l` 干净，50 包测试全绿。

## 后续

工具循环已按上述顺序落地；`search` / `context` / 审计三个域不再是它的前提，而是各自独立的可选刀口。三者中 `audit` 已落地（第三刀），`search` 实测为跨四域的向量链、暂缓（见上）；`context` 待同样实测后再排。传输这一刀的脚手架已在同日拆除，没有留下「两个包两套同名函数」的长期税。
