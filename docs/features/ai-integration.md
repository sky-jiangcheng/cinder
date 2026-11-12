---
title: AI 集成（MCP/llms.txt）
order: 7
---

# AI 集成

Cinder 面向 AI 代理提供读取通道与自检工具，全部复用同一 `internal/service` 实现（与桌面端行为一致）。

## 价值定位：Cinder 是 AI 的数据源，而非 AI 平台

**Cinder 不托管模型、不代持密钥、不自带 agent 编排。** 更准确地说：它是**数据底座**——把 git 原始信息建模成结构化、可索引、可物化的本地知识库（详见[存储结构优化与 AI 价值](../storage-optimization.md)）；AI 工具（Claude Code / Cursor 等）通过 MCP 的 22 个工具来**消费**这层数据，按需取数、精确检索。

这一层「为什么比让 AI 直接读 git 更优」的论证，见[存储结构优化与 AI 价值](../storage-optimization.md)。

需要明说的边界：早期版本这里写的是「Cinder 不调用任何大语言模型」。**这句话已经被产品自己推翻**——AI 问答（v1.15.0）会打 OpenAI 兼容的 `/chat/completions`，可选的语义检索会打 `/v1/embeddings`。真实的定位是上面那句：模型与端点**由用户自备**，相应能力**默认关**，密钥只存不外传。之所以要较真这个区别，是因为它决定了隐私责任的落点——「不内置 AI」意味着没有任何一行笔记会在你没显式开启、没填自己端点的情况下离开这台机器；而「内置了 AI」就不是这个承诺了。

### 配置项

`internal/service/config.go` 的白名单分两组。**核心配置与模型 / API 完全无关**：

| 键 | 类型 | 默认 | 用途 |
|----|------|------|------|
| `auto_import` | 0 / 1 | **`0`（未发布的破坏性变更：旧库会被一次性归零）** | 启动时是否自动导入知识源（当前只有 `claude` 是自动源，其余 4 个需手动触发）。**只有值为 `1` 才开**：行缺失、空串、乱值一律视为关——旧判据是「不等于 `0` 就算开」，而配置读取把「无行」返回成空串，等于**从未设置过的人被默认放行**，这是本次修掉的真 bug |
| `daily_code_standard` | 整数 | `500` | 每日代码行数目标，用于仪表盘达标展示。名字带 “code standard” 但非 AI 规范，易误读 |
| `scan_depth` | 整数 | `2` | 扫描目录深度 |
| `git_author` | 字符串 | 系统 git 用户 | 影响「我的」统计 / 热力图归属 |

**AI 侧配置全部可选、全部默认关**，且都要你自己填端点——Cinder 不带任何服务：

| 键 | 默认 | 用途 | 开了会把什么发出去 |
|----|------|------|-------------------|
| `claude_session_capture` | 关 | 允许按需读取 Claude 会话转录做交接（[ADR-0010](../adr/0010-session-auto-capture.md)） | 无外发，但会读盘上转录 |
| `openclaw_project` / `hermes_project` | 空 | 两个 agent 全局记忆源的归属项目；不设则该源整批 skip | 无 |
| `semantic_search` + `embedding_base_url` / `embedding_model` / `embedding_api_key` / `embedding_dim` | 关 / 空 | 向量召回补 FTS5 盲区（[ADR-0012](../adr/0012-semantic-search.md)） | **笔记文本** → 你的 embedding 端点 |
| `vector_store` + `vector_store_url` / `vector_store_api_key` / `vector_store_collection` | `local` | 向量存哪：本地 sqlite-vec 或远程 Qdrant / Weaviate（[ADR-0013](../adr/0013-vector-database-selection.md)） | 向量（非原文）→ 你的远程库 |
| `ai_chat_base_url` / `ai_chat_model` / `ai_chat_api_key` | 空 | 项目详情页的 AI 问答 | 提问 + 打包的项目上下文 → 你的 chat 端点 |
| `wiki_lint_llm` | 关 | lint 五查中**只有矛盾与过时声明**两项走模型，其余三查纯 SQL、无需授权 | 若干页面摘要 → 你的 chat 端点（只读分析，不回写） |
| `wiki_compile` | 关 | 摄入时编译：**唯一让模型往知识库写东西**的开关 | 笔记正文 → 你的 chat 端点；回写内容持久化在本机，且一律以待审核状态落库 |
| `wiki_merge_suggest` | 关 | 让聊天模型判两张页是否同一概念（[ADR-0019](../adr/0019-entity-resolution-merge.md)）；**需 `wiki_lint_llm` 同时开**，产物只是一条待办，合并本身由人点 | 两张页的标题 + 各自有界正文摘录（约 700 字节） → 你的 chat 端点 |
| `wiki_community_summary` | 关 | 让聊天模型起草 `synthesis` 页（第五种页，[ADR-0020](../adr/0020-community-synthesis.md)）；**绝不上定时器**，也无 MCP 工具 | 每个成员页的标题 + 有界摘录（≤900 字节） → 你的 chat 端点 |
| `community_min_size` | `3` | 社区综述的最小页数门槛；低于它的碎片被丢弃并计入 `dropped_small` | 无——纯本地阈值，不外发 |
| `community_max_size` | `30` | 社区综述的最大页数门槛；更大的块只报告不综述（计入 `dropped_large`） | 无——纯本地阈值，不外发 |
| `wiki_graph_search` | 关 | 图感知取证（[ADR-0018](../adr/0018-knowledge-graph-foundation.md) lane 2）：AskAI 的页面证据以检索命中为种子，沿 `ref/part-of/documents` 边扩 1 跳召回邻居并用 PageRank 重排；关时（默认）`evidencePages` 走与开启前**逐条一致**的 FTS 顺序 | 无外发——纯本地 SQL 图遍历；若同时开了 `semantic_search`，页面向量召回沿用它对端点的授权 |

两个密钥键（`embedding_api_key` / `vector_store_api_key`，以及 `ai_chat_api_key`）在读取配置时以掩码返回、不回传界面——后端持真值，界面只显示 `********`。

## 语义检索与向量存储（可选）

词法检索答不了「同一件事的另一种说法」，向量召回补的是这个盲区。整条链路默认关，且**任何失败都退回纯词法结果**——开启它不会让搜索结果变少（[ADR-0012](../adr/0012-semantic-search.md)）。

- **引导式配置**：`go run ./cmd/vector-init`。它会自检 sqlite-vec 是否装载（建 vec0 → 写测试向量 → KNN 往返 → 清理），让你选 embedding provider（本地 Ollama 为默认推荐，或任意 OpenAI 兼容远端），写入配置，最后指向应用内设置复核。加 `-store qdrant`（或 `weaviate`）可写远程向量库并当场探测；**不可达或未配则自动退回本地**，不会让检索变少。
- **首次要跑一次全量重建**（O(全部笔记) 次文本送端点）。之后不需要再重建：笔记的新建、标题/正文修改、删除由数据库触发器排队，后台每 5 秒批量排空；已删除的笔记会从向量索引里移除。维度未知或索引尚未建立时排空器直接跳过且不消费队列（猜维度意味着可能 drop 整个索引），端点不可达时任务原地保留、下一轮重试。细节见[知识库与笔记](knowledge.md) 的「语义检索」。
- **效果先量化再改默认值**：`go run ./cmd/abeval` 对活库跑「词法 vs 词法+向量」的 Recall@k / NDCG@k 与差值门判定。它读的是你的库，但**不会改动它**：工具在 `VACUUM INTO` 出的一次性副本上运行，所以评测触发的索引重建与 schema 迁移都落在副本里。设置 → AI 已提供知情开启与重建索引入口；真实标注 query 集与门槛通过之前，该功能保持默认关闭——宁缺毋滥，也不默认放大隐私面。

### 检索真实标注工作流（Phase 0 门禁）

语义检索默认上线与 ADR-0018 lane 2 图感知取证两道门共用同一份**真实标注 query 集**。流程五步，工具只负责机械半边，**相关性判断只能由人完成**：

1. **充实语料再开始**：标注质量取决于库里有什么可标。先批准审核页的 pending 页面，再在 设置 → 插件 对各知识源点「立即导入」（六源清单见[知识源导入](../plugins/overview.md)；`openclaw_project` / `hermes_project` 未设的源会按设计跳过）。`repotory_integrity`（MCP）过一遍确认 FTS 索引无漂移——**索引漂移会让检索静默漏结果，标注前必须排除**。验收：笔记 **≥10 条**（目标 ≥15；低于 10 条时单条波动就能主导结论，门禁输出只是噪音）。
2. **导出工作表**：`go run ./cmd/queryset -seed > notes.tsv`。它走一次性副本、只读，**不碰真库**；输出的笔记行号是标注的唯一单位。
3. **人工标注**：每条问题写你真会问的（已有的问题比编造的更真实），`relevant=` 填 1–3 个能回答它的行号，**不凑数**。这是唯一纯手工的一步：门禁拿标注当真值量检索，真值若由工具读笔记猜出来，测的就是猜测器不是检索，**没核对的草稿不能用**。
4. **生成标注集**：`go run ./cmd/queryset -emit notes.tsv -out queries.jsonl`。行号到真实 id 由工具解析（行号与工作表文件绑定——**不要重新 `-seed` 后混入已标注的行**，每次 `-seed` 的编号都会重排）。**`queries.jsonl` 是长期资产，随库保留**，两道门复用同一份。
5. **跑门禁**：`go run ./cmd/abeval -cases queries.jsonl -k 10 -min-recall 0.05`，打印 Recall@k / NDCG@k 与 `GATE PASS/FAIL`。同样走一次性副本，不动真库。

门禁阈值：M3 语义检索默认上线要求 hybrid−lexical recall@10 增益 ≥ 0.05；M7 Lane 2 图感知取证要求 evidence+graph 相对 evidence 的 delta 为正，并附带 `page#N` vs `note#N` 串号专项回归。

### 跨会话记忆门禁（handoff 门）

「产品有没有 PMF」只有一个硬指标：**跨会话能不能记住上周的 gotcha**。这条口径此前只有一句定性的「首条命中」，现在有一条可复跑的命令：

```bash
go run ./cmd/abeval -mode handoff -k 10
```

它不需要标注集：探针由库里的历史 handoff **机械派生**——按 gotcha → decision → change → next step → summary 的顺序取第一条有内容的行，截成一句查询那么长。所以「当时记的是什么」是生成的，不靠人编。产出两件事：

- **可达性（硬门）**：每条记过的 handoff 都必须能被**生产检索路径**（`svc.SearchAll`——前端五个搜索入口、MCP `repotory_notes_search` / `repotory_ask`、HTTP `/api/search?all=1` 走的那一条）在 top-k 内找回。找不回来 = `GATE FAIL`。
- **首位命中率（`repotory_context`）**：逐个项目渲染 agent 真正拿到的那份上下文，检查「## Knowledge Notes」下的第一条是不是最新的那条 handoff，输出 `context lead rate: N/M project(s)`。这是**接线检查**，不是质量分。

同时打印 `hit@1 / recall@k / ndcg@k`（词法臂、混合臂各一行）。**这几个数只报不卡**：探针是记过内容的原文片段，命中率高是结构性的——在个人库这个量级上它衡量的是「可达」，不是「答得准」。要衡量答得准，仍然需要那份人工标注的 query 集。

工具同样走一次性副本，不动真库。

## AI 问答：现在怎么取证

项目详情页的悬浮球面板里有 AI 问答 tab（v1.15.1 起从详情页头部并入面板，与「记录」共享外壳与项目选择器）。

从 M6-W2 起，直问模式**走检索取证**，不再是按时间塞头部：

- 两路召回——**已批准的知识页面**（FTS5，见[知识库与笔记](knowledge.md) 的"待审核与已批准"）与**笔记**（既有检索路径，开启语义检索后含向量召回），按位置交织。
- 在「条数 / 字符 / 超时」三重预算下渲染成带 `[P#]` / `[N#]` 编号的证据块；面板会把本次依据列出来，挤掉多少条、以及"什么都没检索到"都显式说明。
- 答案下方可**存为页面**：链接只取答案正文里真正引用过的编号（模型编一个不存在的编号不会入库），把好答案留在库里供下次使用。
- 什么都没检索到时，**退回旧的静态打包**（项目底座 + 那 10 条笔记），保证不比以前拿到的上下文更少；旧路径那 10 条是 `created_at` 升序的**最早**十条，不是最近十条。
- **交接记录走自己的通道**（[ADR-0040](../adr/0040-unscoped-ask-orientation-and-handoff-evidence.md)）：最近几条带 `handoff` 标签的笔记**按时间**进入证据集——最新一条给全文，更旧的压成一行时间线，与 `repotory_context` 交给下一个会话的形状一致。它们进的是**证据**（带 `[N#]` 编号、可被引用、可回档），不是上下文。之所以要单开一条通道：关键词检索找不到它们——标题是英文的 `Session Handoff …`，正文是固定模板，中文问句命中不了。
- **「全库」也是一种作用域**：作用域选「全库知识与全部项目」时，模型会额外拿到一段**全库目录**（每个项目的名字、路径、仓库数、笔记数）。此前这一档**什么都不给模型**——上下文整段被跳过，模型只拿到检索到的几段文字，连"这个库有哪些项目"都不知道。目录**刻意粗**，技术栈与 README 摘要仍在选定项目时才有。

答案已改为**流式输出**（M6-W3 起桌面走 `StartAskStream` + Wails 事件、浏览器模式走 `/api/ai/ask-stream` 的 SSE，逐字渲染并支持中断/取消）；仍然成立的限制，别读成已完成：**无工具调用**；页面还没有自己的向量索引（融合只在笔记侧，见 ADR-0012/0013），所以页面的召回目前是词法 + 标题权重；取证质量只在构造语料上验证过（`Recall@8` 与 `NDCG@8` 显著优于静态打包），**在真实库上的收益仍未测**——那需要一个真实标注的 query 集。**答案可能引用一次会话的结论**：交接记录是上一轮会话的产物，**可能已经过时**，模型被告知"这是数据"，但不会被告知"这是真的"。

不想让任何内容离开这台机器的话，用**复制 AI 上下文**：它把打包好的 prompt 交给剪贴板，由你决定贴给谁，不经过任何端点。

模型往知识库里写东西（编译）需要另外两件事同时成立：`wiki_compile=1` 且已配置 AI 端点；产物一律是待审核页面，不批准就不进检索。详见[知识库与笔记](knowledge.md)。

## 无头 HTTP 服务与可执行清单

MCP 之外还有一条同源的通道：把 `internal/service` 暴露成 loopback HTTP，供浏览器模式、脚本和其它 agent 调用。

```
go build -o cinder-server ./cmd/server
./cinder-server --port 18765        # 或 env REPOTORY_HTTP_PORT；只监听 127.0.0.1
```

- `POST /api/rpc` 用反射覆盖桌面端绑定的全部方法（JSON body 指定 method + args），与 Wails 走的是**同一份实现**；`Startup` / `Shutdown` / `Service` 本身被屏蔽。写操作与 MCP 遵守同一套协议保护（例如交接笔记拒绝被 update 覆盖）。
- 浏览器开发模式用 `bash scripts/dev.sh`：它同时起无头 API（默认 18765）与 Vite。只跑 `npx vite` 会得到一个「页面能开、每个请求都 502」的假象——那是缺后端，不是应用坏了。
- **发布资产里只有**：桌面安装包（4 平台）、`cinder-mcp-<平台>`、VS Code 扩展 VSIX。`cinder-server`、`cmd/vector-init`、`cmd/abeval`、`cmd/repotory-capture`、`cmd/wiki-export`（wiki 只读导出）都需自行 `go build`（或 `go run`），未随版本分发。

## MCP Server（`cinder-mcp`）

MCP 是唯一的 AI 执行接口（`cinder` 无 CLI 子命令）。stdio 协议，进程内单次开库，22 个工具（含 6 个写操作：扫描 + 笔记创建/更新 + 会话交接 + 每日复盘 + 共享会话写入）：

会话记忆协议的两端在时序上是这样落位的：

```mermaid
sequenceDiagram
    participant A as AI Agent
    participant R as cinder-mcp
    participant K as 知识库 SQLite
    A->>R: repotory_context（会话开始）
    R->>K: 项目解析 + 上下文组装<br>（交接置顶）
    K-->>A: 全项目上下文 Markdown
    Note over A,K: 干活：notes_search / ask /<br>read / create / update
    A->>R: repotory_handoff（会话结束，summary 必填）
    R->>K: 渲染统一模板落库，handoff 标签，<br>拒绝后续覆盖
    K-->>A: 返回 note_id
    Note over K,A: 下一个会话（任何 agent）经 context 置顶读到
```

读图：整个协议只有**两个入口调用**——开会话 `repotory_context`、收会话 `repotory_handoff`，中间的工作期走的是普通读写工具。两条关键保证：交接笔记**写入一次即带 `handoff` 标签**，且 `repotory_notes_update` 拒绝覆盖它（所以下一个会话读到的始终是同一条记录，置顶渲染）；落库走的是统一模板，不是自由文本。

### 谁执行这次写：跨进程单写者契约（ADR-0058）

桌面端、`cinder-mcp` 与 SessionEnd 钩子可以同时打开同一个 `repotory.db`。**同一时刻只有一个 Cinder 进程写库**：谁持有数据目录里的 `repotory.writer.lock`（OS 文件锁，进程被杀即释放）谁是写者，其余进程的写操作经本机回环 HTTP 转给写者执行。对 agent 的可见差别只有三条，都不改工具签名：

- **长写不再炸出 `database is locked`**：备份、批量编译这类长事务占锁时，你的写排在写者的队列里等它，而不是 5 秒后失败。
- **「无需桌面」仍然是真的**：机器上没有别的 Cinder 在跑时，`cinder-mcp` 自己当选写者；桌面一启动就收回写权（有界请求，agent 正忙则桌面暂时继续转发）。
- **新增一个错误码 `writer_unavailable`（retryable）**：写者消失且无法接管时返回，message 会说明补救动作（启动 Cinder Desktop，或直接重试——另一个存活的 Cinder 会接管）。它可能出现在任何写工具上；`conflict` 等既有码语义不变、跨进程不降级。

**写失败会不会已经生效？** 契约区分两种情形：请求从未送达（写者端口已死）时客户端会透明地重试一次并可能就地接管；请求发出后连接中断则**绝不自动重放**——把歧义报回给你。带幂等键的 `repotory_handoff`（ADR-0057）重发安全；`repotory_notes_update` 重发前请先 `repotory_notes_read` 看一眼。

**完全离线的本地模型**（ollama / LM Studio）接的是同一套 MCP，接线步骤与真机验证记录见[本地 Agent 配方](local-agent-recipe.md)。

| 工具 | 说明 | 读写 |
|------|------|------|
| `repotory_scan` | 冷启动：播种默认扫描根目录并同步扫描，发现本地 Git 仓库（纯 MCP 安装可用，无需桌面应用） | 写 |
| `repotory_context` | 会话开始一次注入项目全上下文（技术栈 / README / 待办 / 高相关笔记，交接笔记置顶）。可选 `path`（绝对目录 / cwd）解析归属项目，见「工作区路径解析」 | 读 |
| `repotory_handoff` | 会话结束结构化交接（summary/changes/decisions/gotchas/next_steps），落库并供下次 `repotory_context` 置顶读取；落库笔记带 `handoff` 标签，`repotory_notes_update` 拒绝覆盖。`project_id` 可选，缺省时由 `path` 解析。**注意**：这里的 handoff 是**持久化的交接记录**，不是「在 harness / 执行环境之间移交一场正在进行的对话」（那是 VS Code "Hand off a session" 的含义，见 [ADR-0007](adr/0007-session-memory-protocol.md)） | 写 |
| `repotory_notes_list` | 全部笔记 | 读 |
| `repotory_notes_search` | FTS5 搜索（query）。可选 `path`：命中仓库内的笔记排到前面——只排序，不过滤 | 读 |
| `repotory_notes_read` | 按 ID 读笔记 | 读 |
| `repotory_notes_create` | 新建知识笔记 | 写 |
| `repotory_notes_update` | 更新笔记内容与元数据（部分更新：**省略的字段保持原值**；拒绝覆盖 `handoff` 协议笔记）。可选 `pinned`：传 `true` 置顶到项目顶部、传 `false` 取消置顶，省略则保持原状态 | 写 |
| `repotory_notes_organize` | 让已配置的 AI 把一篇笔记重排成干净 Markdown（同一批事实、更好的结构；长了给目录、是流程给 mermaid）。**只返回提案**（title/tags/content），不落库——审阅后自行用 `repotory_notes_update` 保存。需先配置 AI 端点 | 读（提案） |
| `repotory_projects_list` | 全部项目 | 读 |
| `repotory_projects_stats` | 项目统计（按 id） | 读 |
| `repotory_ask` | 问答式检索，Top-5 文本 | 读 |
| `repotory_wiki_search` | 检索**已批准**的知识页（概念/实体/综述/来源）。带 query 按相关度排序（FTS5，容中文），空 query 列出最新页；`project_id` / `path` 可限定项目。未批准的页面永不返回 | 读 |
| `repotory_wiki_read` | 完整读取一页已批准知识页：正文 + 出链页 + 入链页 + 编译来源笔记 | 读 |
| `repotory_agent_score` | 检查本地 AI 就绪度（DB/笔记/搜索/MCP/llms.txt/SKILL.md/i18n） | 读 |
| `repotory_integrity` | 审计数据可信度（FTS 索引漂移 / 孤儿行 / 缓存新鲜度 / 覆盖率） | 读 |
| `repotory_ai_audit` | 查看本机最近向 AI 发了什么：时间 / 端点 / 模型 / 发起面（mcp/http/desktop）/ 字符数 / 是否被本地规则拦截。**不含提示词与回复原文**（审计只存哈希与长度） | 读 |
| `repotory_ai_review_rules` | 查看本地 AI 审阅规则与输入/输出审阅开关。规则是用户自己的正则（每行一条，`#` 注释），命中即在发出前拦截/返回时标记；不内置黑名单 | 读 |
| `repotory_chat` | 跨端**共享会话**（Warden 与 IDE 插件之间同步主题与已完成的消息）：`operation` ∈ `list` / `create` / `read` / `append` / `rename`。id 由客户端生成 UUID；同 id 同载荷重放**幂等**，同 id 不同载荷直接报错、**绝不覆盖**；`reply_to` 只能指向本主题内的 user 消息。与知识笔记、检索、审批**完全隔离**，不共享一张表 | 写 |
| `repotory_metrics` | 读**已存库**的量化概览（仓库数 / 代码行 / 今日提交与增删 / 笔记数 / 待人工审批数）。不扫盘、不跑 git、不刷新缓存；`path` 空 = 全库，未知路径返回空计数而不是全库总量；`since` / `until`（`YYYY-MM-DD`，含端点，最长 366 天）给日期窗口的提交与增删——窗口超出最后一次扫描时计数为 `null` 且 `stale: true`，**不是 0**（没人扫过的日期在缓存里是缺席，不是没干活）；缓存语言 LOC 覆盖不完整时 `totalLines` 为 `null`（宁可少给，不给假总数） | 读 |

### 就绪度 vs 数据可信度

两个自检工具问的是**不同的问题**，不要混用：

| 工具 | 回答的问题 | 不能回答的问题 |
|------|-----------|----------------|
| `repotory_agent_score` | 这个安装配置好了吗？（有没有笔记、MCP 通不通、i18n 齐不齐） | 数据本身对不对 |
| `repotory_integrity` | 数据还能信吗？（索引有没有漂移、有没有孤儿行、缓存新不新鲜） | 配置齐不齐 |

**为什么需要第二个**：FTS5 索引一旦与 `project_notes` 失配，搜索会**静默少返回结果**，而 `agent_score` 依然会报「Search operational」——它测的是通路，不是内容。`repotory_integrity` 用 FTS5 的 `_docsize` 影子表（external-content 表的 `SELECT rowid` 读的是内容表，比不出来）对比索引真实文档数，并检查同步触发器是否齐全。详见 [`internal/integrity` 的检查清单](../../internal/integrity/integrity.go)。

当 AI 发现「搜出来的东西好像不全」时，应该先跑 `repotory_integrity`，而不是直接下结论说知识库内容少。

### 工作区路径解析：这个文件夹属于哪个项目

「我站的这个目录是哪个项目？」是最常见却一度无法回答的问题。旧的 `ResolveProject` 用 `strings.Contains(project.RootPath, query)` 匹配——存进来的根路径必须*包含*查询串——所以一个多仓库项目的子目录（例如 `/…/CBiPay/pay-common`）永远匹配不上；而 `repositories` 表明明记录了那个目录，却根本没被查询过。VS Code 扩展更是从不把工作区路径发到任何地方，交接命令甚至要求先手填一个项目 id 数字才肯运行。

现在由 `Service.ResolveProjectByPath` 做**四层路径解析**，从最具体到最粗：

1. 恰好存于该目录的 repository → 它的 project（scope 即那个 repository）
2. 作为该目录*祖先*的最深 repository 路径 → 它的 project
3. `root_path` 等于该目录的 project → 该 project
4. 作为该目录*祖先*的最深 project `root_path` → 该 project

repository 层（1、2）压过 project 层（3、4），因为命中 repository 报的是**模块**，而不只是容器。

**容器限制（只作用于第 4 层）。** `is_auto_grouped = 1` 的项目是扫描期的兜底容器，**不得凭祖先关系取胜**——这个库里确实有一个根在用户 home 目录下的容器，若放开祖先匹配，home 下每个文件夹都会被它认领、注入错误项目的知识且毫无征兆，这比"未解析、请挑一个"更糟。自动归组项目仍可被第 3 层的**精确匹配**命中（主动打开那个目录本身就是一个真实答案）。调用方可显式解除这一限制。

**匹配方式是上报的，不是假设的。** 每条结果都带 `MatchedOn`：`repository-exact` / `repository-ancestor` / `project-exact` / `project-ancestor` / `repository-…-orphan` / `unresolved`（大小写回退命中会附加 `-case-folded`，仓库层与项目层同时命中拼成 `repository-X+project-Y`）。什么都解析不出来时，调用方拿到项目目录加 `unresolved`，绝不静默猜一个。一个 project 行已删的 repository 报为孤儿而非"解析成功"，因为"这个文件夹是被扫过的仓库、项目已删"和"从没见过这个文件夹"是两个不同的、可行动的诊断。

**路径语义。** 祖先判定按路径段判断，绝不用 `strings.HasPrefix`（`/a/b` 是 `/a/bc` 的字符串前缀，却不是它的父目录）。同层命中取最长/最深路径。大小写折叠只是**回退项**——大小写敏感的精确匹配永远优先，因为在区分大小写的文件系统上仅大小写不同的两个路径就是两个不同目录。

**repository 范围：按排名，不按过滤。** `note_repositories` 从 wiki 层首个迁移起就在，却**全代码库没有任何写入方**，所以任何严格的"把笔记过滤到这个 repository"对每个既有项目都会返回空。两个后果：
- 交接路径现在负责**写入归属**：`HandoffInput.WorkspacePath` 解析到一个 repository 时把笔记挂到它上面。归属拒绝跨项目（一个属于另一项目的 repository 不会被挂上），且归属失败绝不导致交接失败——它记在 `HandoffResult.RepositoryNote` 里，笔记本身保留。
- 消费端**重排而非删除**：在 `repotory_context` 输出里，记录在被匹配 repository 内的笔记排在前面，其余项目笔记仍然出现，前面有一行显式的 `_Scope: repository "x" (matched by …)_`——尚未有任何归属时这行会诚实地说"下面 N 条项目笔记未经过滤"。`repotory_notes_search` 同样按此排序，且不动待办，因为归属表描述的是笔记。

**挖掘不再拿任意兄弟仓库作答。** `GetProjectOverview` 过去按 `repos[0].ID` 键入缓存并在项目根目录挖掘，于是一个兄弟仓库容器展示的技术栈 / README / 提交只是排序最前的那个 repository。`GetRepositoryOverview(projectID, repositoryID)` 把挖掘、缓存与提交都收窄到该 repository（这也让挖掘有了真正的 git 根——v13 迁移过去只能做到失效）。一个不属于该项目的 repository id 会被**拒绝**而非替换。

这条解析是**服务端**的，MCP 工具与 VS Code 扩展共用同一实现：`repotory_context` / `repotory_handoff` / `repotory_notes_search` 新增可选 `path`，扩展通过只读绑定 `ResolveWorkspacePath` 拿到归属显示，因此不会在 TypeScript 里重新实现一遍匹配器。注意 `repotory_context` 里 `path` 能解析到项目时**优先于** `project_id`/`project_name`，而 `repotory_handoff` 里 `project_id` 优先、`path` 仅在其缺失时兜底。（`repotory_context` 的 MCP 工具描述仍把 `project_id` 写成「preferred」，但实际是能解析的 `path` 取胜——描述与行为存在已知出入。）

### 桌面端一键接入（不用开终端）

装了桌面 App 的人不必去终端跑脚本：**设置 → 操作 → 接入 AI 客户端**（见[设置](settings.md)）里选一个项目、勾选要接的客户端、点一次「接入」，桌面端就会写好 `.mcp.json` / `.cursor/mcp.json` / `.vscode/mcp.json`，可选装上 Claude Code 的 SessionEnd hook，**可选把「开局读 / 收尾写」那两句写进项目的 `AGENTS.md` 与 `CLAUDE.md`**（[ADR-0043](../adr/0043-handoff-instructions-in-agent-instruction-files.md)），并顺手触发一次扫描。装完直接开 Claude Code，`repotory_context` 就有内容可返回，不用再手动 rescan。

（Windsurf 是个例外：它的 MCP 配置是**用户级**的，落在 `~/.codeium/windsurf/mcp_config.json`，与项目无关。其余三个客户端的配置写在**项目目录**里，跟着仓库走。）

注册进去的 `cinder-mcp` 二进制按这个顺序找：显式路径 → `REPOTORY_MCP_BIN` 环境变量 → `PATH` → **与当前可执行文件同目录**（桌面安装包里它就躺在 `repotory` 旁边，所以正常安装一次就能找到）→ 包管理器常见目录。找不到时按钮会明确说「二进制不在」并给出安装指引，而不是写进一个指向空路径的配置。

它与下面那条终端路径是**同一套协议的两份实现**：

| 实现 | 形态 | 谁在用 |
|------|------|--------|
| `internal/agentinit` | Go，桌面二进制自带 | 桌面端那个按钮 |
| `scripts/repotory-init/index.mjs` | node 脚本，零依赖 | 终端与 VS Code 扩展 |

两份实现写出的文件被**同一份 fixture**（`internal/agentinit/testdata/contract.json`）钉住：`go test ./internal/agentinit` 验 Go 那一侧，`node scripts/check-agent-init-contract.mjs` 验脚本那一侧（并且**两份脚本副本都验**——事实源是 `scripts/repotory-init/index.mjs`，VSIX 里带的那份由 `ide/vscode/scripts/sync-init.mjs` 生成）。只改一边，另一边立刻红：这种漂移从两边看都是「已注册」，而 Claude Code 什么也没收到。

有几件事按钮**不会**替你做（都明确拒绝，而不是猜）：

- **不写默认项目与自动归组容器**：默认项目（`repotory://memory/default`）和扫描期的兜底容器不是能接 AI 客户端的目录，界面上它们根本不出现在项目下拉里；
- **不接受空选择**：一个客户端都不勾会被拒绝，而不是「都写一遍」；
- **不认未知客户端名**；
- **目录不存在就报目录缺失**，而不是凭空造一个目录。

按钮返回的是**机器可读原因**（`code`），文案由界面按你的语言渲染——后端不返回写死的人话句子，这是全仓一致的规矩（见 `CheckBackupCopyTarget`）。

### 终端一键注册（repotory-init）

[ADR-0009](../adr/0009-ide-presence.md) 的第一步：一条命令完成二进制探测与客户端注册，幂等可重复执行：

```bash
node scripts/repotory-init/index.mjs --with-hook
```

**在任意项目里都能跑**：VS Code 扩展的 `Cinder: Register MCP Server` 命令用的是随扩展打包的那份 `repotory-init`（事实源仍是 `scripts/repotory-init/index.mjs`，由 `compile` 拷入 VSIX，CI 用 `git diff --exit-code` 钉住两者一致），所以不必先克隆本仓库；执行时也不弹终端——扩展在宿主进程内静默跑完安装器（仅 `ENOENT`/`EACCES` 时才回退到 `node`）。上面的裸命令则需要你在本仓库目录内执行。

- 自动探测 `cinder-mcp`（`--bin` 可显式指定），向 Claude Code（`.mcp.json`）、Cursor（`.cursor/mcp.json`）、VS Code（`.vscode/mcp.json`）、Windsurf 写入注册（配置只落在对应客户端目录已存在的地方），JetBrains 打印手动指引
- `--with-hook` 同时安装下方的 SessionEnd hook（脚本 + `settings.json` 合并，改前备份）
- `--with-instructions` 同时把交接指令写进项目的 `AGENTS.md` 与 `CLAUDE.md`（ADR-0043；已有同名区块则原地替换，**区块之外的内容一律不动**，改前备份）
- `--dry-run` 预览全部写入动作；找不到二进制时给出安装引导，`--yes` 以裸命令名 `cinder-mcp` 写入配置（装好即生效）

### 手动接入 Claude Code（备选）

```bash
claude mcp add repotory -- /path/to/cinder-mcp
```

或写入 `.mcp.json`（项目级）/ `~/.claude.json`（用户级）：

```json
{
  "mcpServers": {
    "repotory": { "command": "/usr/local/bin/cinder-mcp", "args": [] }
  }
}
```

### 会话结束自动交接（SessionEnd hook）

「零成本沉淀」的真实含义不是「agent 记得自觉调用」——靠自觉等于没有承诺。Claude Code 的 **SessionEnd hook** 把交接变成会话生命周期的一部分：会话一结束，交接必然发生，不依赖 agent 的记性。这也是新用户装完第一天就能感受到「下次开局即带全上下文」的路径。

写入 `.claude/settings.json`（项目级，随仓库分享给协作者）：

```json
{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/repotory-handoff.sh"
          }
        ]
      }
    ]
  }
}
```

配套脚本 `.claude/hooks/repotory-handoff.sh`（记得 `chmod +x`）：

```sh
#!/bin/sh
# SessionEnd hook: make the handoff automatic instead of relying on the
# agent's goodwill. One bounded headless turn costs a fraction of the
# session it preserves; "the agent will remember to call handoff" costs
# the entire exit record whenever it forgets.
set -u
cd "${CLAUDE_PROJECT_DIR:-$(pwd)}" || exit 0
command -v claude >/dev/null 2>&1 || exit 0
claude -p --mcp-config .mcp.json \
  'This session ended. Call repotory_handoff for the current project: a concise summary plus next_steps. If the project cannot be resolved, call repotory_context once first. Do nothing else.' \
  >/dev/null 2>&1 || true
```

设计要点与代价（写清楚，别让叙事变成吹牛）：

- **触发时机**：会话结束时由 Claude Code 触发，触发原因（`clear` / `logout` / `prompt_input_exit` / `other`）以 JSON 形式写入 stdin；脚本可读 stdin 按需跳过（例如「只是清了上下文」不必交接）。
- **成本**：一次有界的 headless 回合（`claude -p`），远小于它保存的整场会话上下文；hook 有默认超时（60s），脚本以 `|| true` 静默收尾，不影响会话退出。
- **降级路径**：没有 `claude` CLI 时直接跳过；MCP 未注册时 headless 调用失败并被吞掉——协议要求交接必须由 `repotory_handoff` 写入，hook 只负责「必定触发」，从不绕过协议直写数据库。
- **协议保护**：交接笔记带 `handoff` 标签，`repotory_notes_update` 拒绝覆盖它们（防误覆盖），下一次会话由 `repotory_context` 置顶完整渲染。
- hook 的事件名与配置字段随 Claude Code 版本演进，接入前以 `claude --help` 和官方 hooks 文档为准。

### 交接指令进项目指令文件（受管区块，ADR-0043）

SessionEnd hook 只存在于 Claude Code。**换任何一个别的 agent，那条路就断了**——而本产品立的正是「跨 agent 的持久记忆」，所以补上另一条不挑客户端的路：把那两句指令写进项目自己就有的指令文件。

`repotory-init --with-instructions`（或桌面端设置 → 操作 → 接入 AI 客户端里那个勾选）会往 **`AGENTS.md` 与 `CLAUDE.md` 各写一段**被标记夹住的文本：

```markdown
<!-- repotory:begin -->
## Cinder memory layer
…开局调用 repotory_context…收尾调用 repotory_handoff…
<!-- repotory:end -->
```

- **两个文件都写，不按客户端选**。一份没人读的指令文件是**惰性的**——它既不产生行为，也不产生错误。写两份的代价只是多一个文件；按客户端挑的代价是**用户一换 agent 就悄悄失效**。`AGENTS.md` 是跨 agent 的事实标准（Codex / opencode / Cursor / Amp 都读），`CLAUDE.md` 是 Claude Code 自己那份。
- **标记之外一律不动**。已有区块 → 原地替换（所以点两次不会叠出两个区块）；文件已存在 → 追加到末尾；文件不存在 → 创建。**区块之前是你自己的内容，我们拥有那一段，不拥有那个文件**；改动前照旧留 `.repotory-init.bak` 备份，报告逐文件列出「新建 / 已改 / 未变」。
- **它进你的 git**。第一次点完，`git status` 会多出（或改动）这两个文件——这是「接入」这件事本来的形状，不是副作用被藏起来了。不想被提交的目录可以只装 hook。
- **区块里第三句是重点**：「记那些下一次会话会重新踩一遍的东西，**不要记 commit log 已经显示的东西**」。少了这句，指令只会让 agent 写活动流水——那正是本项目上游枯竭的形状（`idea` 类笔记 0 条）。

**诚实的边界**：指令文件让「收尾记一笔」从一个**必定发生**的事件（hook）变成一个**大概率发生**的事件（模型读到了，但服从不保证）。所以两条路**并存、不互斥**：hook 兜住「用户忘了收尾」，指令文件兜住「用户根本不用 Claude Code」，而两条路都走同一个 `repotory_handoff` 落库路径，**交接记录不会出现两种形状**。

卸载就是删掉那两行标记之间的内容——区块对用户是一段可读可改的纯文本，不是一个需要产品代管的隐藏状态，所以不提供 `--remove` 开关。

### 接入 Cursor / 其他 MCP 客户端

在对应客户端的 MCP 配置中添加同样的 `command` 指向 `cinder-mcp` 二进制（Cursor：`Settings → MCP → Add Server`）。

## llms.txt 与 Markdown 导出（应用内）

- **llms.txt**：`GenerateLLMsTxt` 生成知识库总览 Markdown（项目目录 + 技术栈 + 最近 20 条知识笔记），适合喂给 LLM 建立上下文。**llms.txt 是导出格式**（应用内生成，不随仓库分发），不作为独立产品方向扩张（见 ADR-0006）
- **笔记导出**：任意笔记导出为带 YAML frontmatter 的 `.md`（`ExportNoteAsMarkdown`）
- **Claude 记忆导入**：`~/.claude/projects/*/memory/*.md` 幂等导入（见[知识库](knowledge.md)）

## agent-score 自检

agent-score 已合并为 MCP 工具 `repotory_agent_score`，无需独立构建。通过任意 MCP 客户端调用即可获取 7 项 AI 就绪度评分。

## 为什么不直接让 AI 读 git 仓库

一个常见疑问：既然 Claude Code / Cursor 都能直接 `git log`、读文件，为什么还要经过 Cinder？核心答案是**成本与确定性**：

- **读得贵**：每次让 AI 直接 `git log` 或逐文件扫描都是一次性消费，重复读 = 重复 token；
- **读得乱 / 不全**：大仓必超上下文窗口、被截断，模型还可能幻觉或漏读二进制 / `.gitignore`；
- **读得慢**：每次重算统计，秒级任务退化成分钟级。

Cinder 在**扫描时一次性**把 git 原始数据解析并物化进本地 SQLite（`daily_stats` 预聚合统计、`repo_meta` 缓存挖掘结果、`project_notes_fts` 建全文索引），之后 AI 只通过 MCP 工具**按需取数、精确命中**。完整对比与存储结构细节见[存储结构优化与 AI 价值](../storage-optimization.md)。

## 面向 AI 代理的技能卡

仓库根目录的 [SKILL.md](https://github.com/sky-jiangcheng/cinder/blob/master/SKILL.md) 是给代理阅读的能力卡片（命令、工具表、路径），可直接投喂。

### WorkBuddy 技能包

根目录的 `SKILL.md` 是**平台无关**的能力卡片（Claude Code / Cursor 等按需读取）；WorkBuddy 则用**带 frontmatter 的技能包**机制，代理自动发现并按描述触发。

安装到 WorkBuddy（用户级，全局可用）：

```bash
# 技能包已随 release 提供，解压到用户级 skills 目录即可
unzip repotory.skill -d ~/.workbuddy/skills/
```

装好后 WorkBuddy 里还需要在连接器管理页点一次「信任」，MCP 工具才会出现在工具列表里：

```json
// ~/.workbuddy/mcp.json
{
  "mcpServers": {
    "repotory": { "command": "/usr/local/bin/cinder-mcp", "args": [] }
  }
}
```

技能包的内容：

| 文件 | 作用 |
|------|------|
| `SKILL.md` | 三级降级接入（MCP → HTTP RPC → SQLite 只读）、会话记忆协议、22 个工具的路由决策、agent 不可用的能力清单 |
| `references/mcp-tools.md` | 每个工具的精确参数签名、HTTP RPC 方法映射 |
| `references/data-model.md` | schema 与不变量（主题归属、迁移规则、handoff 保护、trigram 边界） |
| `scripts/repotory-query.py` | MCP 不可用时的只读兜底查询（检索 / 列主题 / 列项目 / 库概况） |

**为什么要有兜底通道**：MCP 未被信任、或环境里没有 `cinder-mcp` 时，代理仍要能查知识库。只读兜底走 `sqlite3` 直接打 `project_notes_fts`，只发 `SELECT` —— 但它绕不过 FTS 索引漂移的补偿逻辑，所以优先走 MCP。

**agent 侧的硬边界不变**：技能包里明确写了哪些绑定是 desktop-only（建/改/删主题、给笔记归类、项目合并、wiki 编译与审批）—— 这些是**归档与评审行为，归人**。技能包不会让代理越过这条线。
