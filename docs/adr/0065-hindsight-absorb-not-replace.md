# 0065 对标 Hindsight——不替换底层，定向吸收；体验缺口清单另立研究稿

- 状态：**Accepted（2026-11-23，用户在 A/B/C 三条路线里选 A「不替换，定向吸收」，并要求「产品体验要做到极致」）**
- 相关：[ADR-0006](0006-scope-freeze.md)（范围冻结）、[ADR-0012](0012-semantic-search.md)（向量默认关 = 隐私授权）、[ADR-0015](0015-w3-minimal-compile-loop.md)（人审闸门）、[ADR-0018](0018-knowledge-graph-foundation.md)（检索底座，lane 1/2 均 Accepted）、[ADR-0047](0047-resident-agent-separate-host.md)（Warden 走 MCP 的分工）、[ADR-0049](0049-brand-restructure-manus-style.md)（标识层冻结）
- 缺口清单全文：[docs/product-review/2026-11-23-hindsight-experience-gap.md](../product-review/2026-11-23-hindsight-experience-gap.md)（仅中文研究稿，不进手册镜像）

## 背景

用户问「调研 Hindsight，差距多大，是否应该替换 Cinder 和 Warden 的底层」。Hindsight（vectorize-io/hindsight）是与 Cinder 定位正面重叠的 agent 记忆系统：MIT、Python、约 62.7k stars、v1.2.3（2026-11-19），商业公司全职维护，宣称 LongMemEval SOTA（Virginia Tech 与华盛顿邮报独立复现；具体数字本次未抓取核实，不引用）。其 `hindsight-coding-agents` 包按仓库自动建 bank、从 git 历史与会话自动摄入、会话开局注入 curated pages，支持 22 个 coding agent 一键接线——**这是竞争信号，不只是技术对比**。

## 事实（调研当日核实）

**Hindsight 侧**（来源：GitHub README 与 docs，经 gh API 取得）：

- 存储 PostgreSQL + pgvector（嵌入式 pg0 可免装库）；Docker / pip / Helm / 云托管四种部署；**每次 retain 必过 LLM 抽取**（实体/关系/时间），本地 Ollama 可跑但无零模型路径
- 记忆四层：world facts → experiences → **observations**（自动 consolidate，证据原文引用 + proof count，新证据修订信念而非覆盖，近重复合并默认开）→ **mental models / pages**（自维护活 wiki，增量 delta 刷新，`fs mount` 投影成磁盘 markdown）
- recall 四路并行（向量 / BM25 / 实体因果图 / 时间轴）→ RRF → cross-encoder 重排
- 三个自训开源小模型：决策 0.8B（Apache 2.0）/ 重排 114M（MIT）/ 嵌入 112M（MIT），可纯本地；决策分流实测省约 40% LLM 调用、页刷新省约 70% 输入 token
- Memory Defense：45 种内置 secrets/PII 模式，redact 或 block，per-bank opt-in
- 体验基准（AMB sdebench，61 个真实仓库任务 ×3 轮）：记忆不改变解题率（60–61/61 不变），改变的是**每任务人工纠正次数**（Claude Code 0.85→0.36）与 token 成本（$0.445→$0.338）

**Cinder 侧**（仓内代码核实，修正调研初判的两处高估）：

- **RRF 已有**（`internal/search/hybrid/hybrid.go` `FuseRRF` k=60，`fuseSemantic` 已接生产 `SearchAll`）；**评测基建已有**（`cmd/abeval` recall/ndcg、`cmd/searchbench` 分阶延迟、`cmd/queryset`）——「缺 RRF、缺评测」的初判不成立
- ADR-0018 lane 1/2 均 Accepted，`wiki_graph_search` 已晋升（分阶加性 + PPR）；向量默认关是 ADR-0012 的隐私授权决策，不是缺口
- 真缺口：**无重排模型**；无时间轴检索通道；**无 observations 式证据加权信念修订**（grep 零命中）；wiki 编译/synthesis 门默认关且 2026-10-29 起停止扩产（供给不足，27 approved / 40% 拒绝率）；接线一键仅桌面四家（`internal/agentinit`），无 `install all` CLI；hook 仅 Claude Code SessionEnd 且默认关，**无 SessionStart 注入**；无内置 secrets/PII 规则库（aiaudit 只有用户自定义正则，四门默认关）；无已批准页磁盘投影；无记忆时间线视图
- Warden 不是记忆系统，是只走 MCP 的 copilot（ADR-0047）；「替换底层」对 Warden 意味着重写其全部记忆契约（`repotory_context` / `wiki_search` 三面板作废）

## 决策

1. **不替换底层**。Cinder 保持单 SQLite（FTS5 + sqlite-vec，零 CGO）、MCP 工具面、人审闸门（ADR-0015）、零模型捕获路径、冻结标识（ADR-0049）。Warden 的 MCP 契约不动。
2. **定向吸收**：以「减少人工纠正次数」为体验北极星（sdebench 证明这才是记忆系统的体验指标，不是解题率），按缺口清单逐项评估吸收形态。吸收一律不破坏三条承重边界：**人审闸门**（LLM 产物仍只进 pending）、**隐私授权**（新模型端点默认关，ADR-0012 口径）、**零 CGO 单文件分发**。
3. **首步动作是评测不是写码**：用既有 abeval/queryset 基建在真实笔记库上建评测集，量出检索缺口后再决定吸收顺序。「差距多大」目前只有对方基准，没有自己数据上的数字。
4. **候选吸收项**（清单与优先级建议见研究稿，**均未排期**，逐项另行拍板）：会话开局注入（SessionStart hook / 受管区块升级）、`install all` 一键接线 CLI、内置 secrets/PII 规则库、已批准页只读磁盘投影、重排模型（`hs-rerank-1` MIT 114M 是候选，走可选本地端点）、时间轴检索、observations 式证据修订（pending + 人审形态）、小模型分流（治 40% 拒绝率与编译成本）。
5. **明确不吸收**：disposition traits（对话人设，与 coding 记忆无关）、云 / 多租户 / Oracle / Prometheus 企业运维面、Postgres 存储形态。

## 被否路线

- **B 整体替换**（Cinder 底层换 Hindsight server，Warden 改指其 MCP）：审批闸门、零模型捕获、单 SQLite、git 统计工具面全部失去或重写；Warden 三面板契约作废；引入 Postgres + Python + 每写必 LLM；64 篇 ADR 的产品承诺推翻。**这等于放弃 Cinder 这个产品本身**——只要 Cinder 仍作为对外产品维护，此路不可选。
- **C 混合双库**（Hindsight 做旁路检索 lane，批准页同步 retain 过去）：两库一致性、同步漂移、双倍运维，是单人维护最重的形态；每次同步都付 LLM 抽取成本。单库补强够用时纯属多余复杂度。

## 后果与翻转条件

- 代价：单人工程时间逐项投入；重排/小模型接入引入**可选**外部端点依赖（默认关守住隐私授权）；评测集需要持续维护，否则吸收效果无法证明。
- **翻转向 B 的触发条件**：自建评测证明缺口不可补，**且**用户决定 Cinder 不再作为产品维护。
- **翻转向 C 的触发条件**：出现「必须拿 Hindsight 生态（60+ 集成）当卖点」的外部需求。
- 竞争警戒线：`hindsight-coding-agents` 的覆盖面（22 harness）与零配置摄入在快速扩张；Cinder 的差异化护城河是**人审闸门 + 本地单文件 + 零模型路径**，吸收动作若稀释这三条，等于走进对方的主场。

## 落地记录

- 2026-11-23：决策 A 由用户拍板；本 ADR 与缺口研究稿同批落库；候选项均未排期，首个执行动作（自建评测集）待启动。
- 2026-11-23（同日，用户指示「开始建评测集，然后按优先级处理」）：**评测集 v2 建成并跑出词法基线**——旧 51 条标注零失效原样保留，扩集 19 条按缺口类别打标（时间轴/消歧/ADR 知识/handoff 记忆/跨项目），合并 70 条；live 库副本上 lexical 生产路径 **recall@10=0.719 / ndcg@10=0.591**。逐案诊断四个实锤：① 自然语言日期查询零命中（时间轴通道缺失，P2 升为有实锤）；② 测试/导入噪声笔记挤占头部（`SearchHit.Kind` 杠杆检索面未用上，重排前先量噪声过滤）；③ handoff 内容检索 memory 类仅 0.333（疑似 expandForFTS 缺陷，属 bug 修复候选）；④ 库增长稀释旧标注排名。**hybrid 臂欠一次复跑**（LM Studio 端点未运行，delta=0 是端点缺席的必然结果不是结论）。全文与复跑命令：[outputs/process-logs/2026-11-23-评测集v2与检索基线.md](../../outputs/process-logs/2026-11-23-评测集v2与检索基线.md)。评测集/探针/副本均按 queries.jsonl 既有口径 gitignore（含用户笔记 id 映射）。
- 2026-11-23（同日下午，用户指示「本地模型不强，用符合规范的远程 API」）：**hybrid 复跑清账，门通过**——Ark 账号未开通模型（ModelNotOpen）且 coding-plan 过期，改用活库已有的 OpenRouter key + `qwen/qwen3-embedding-8b`（4096 维，MTEB 多语第一梯队，全量嵌入不到一美分）。配置只写评测快照（`embedding_dim` 须从继承的 768 显式改 4096），**活库语义检索配置未动**——日常发笔记内容往远程是 ADR-0012 的隐私授权决策，留用户拍板。70 案生产路径：**hybrid recall@10=0.945 / ndcg@10=0.766，delta +0.226 / +0.175，GATE PASS**。向量召回对中文自然语言问句收益实锤；剩余缺口仍指向时间轴通道、噪声过滤与 expandForFTS 排查。详见过程日志复跑章节。
- 2026-11-23（同日晚，用户批准「按建议处理」）：**逐案复测修正诊断 → 活库切换 → 时间轴通道落地**。① 70 案 hybrid 逐案复测：memory 类 0.333→**1.000**（expandForFTS 嫌疑被向量臂洗清，降级不修）；无标签 51 案 0.745→0.990；噪声过滤预实验取消（动机消解：噪声挤占只伤词法臂，hybrid 下已不在头部）；时间轴 6 案 hybrid 仍 0.000–0.500，是唯一向量救不了的类。② **活库语义检索切换获用户拍板**（ADR-0012 隐私授权）：OpenRouter `qwen/qwen3-embedding-8b`（4096 维），156/156 向量重建完成，`semantic_search=1`；重建路上实锤并修复三个健壮性缺陷（半途而废返回 nil 错误、响应体读取错误被吞 + 8MiB 静默截断、30s 超时/64 批量不适配远程端点 → 120s/16，见 CHANGELOG 修复节）。③ **时间轴通道落地（ADR-0070）**：日期表达解析 + created_at 窗口召回 + 第三 RRF 臂，常开无开关；词法+时间轴门下时间轴 6 案全部 1.000、70 案零回归（lexical 0.719→0.764）；hybrid+时间轴完整门 **recall@10=0.986 / ndcg@10=0.808（delta +0.221 / +0.189），GATE PASS**，对无时间轴 hybrid 基线 0.945/0.766 逐案零回归、temporal 6/6 全 1.000。剩余 backlog：RRF 调参/rerank（选项 b）与 P3 项（选项 d）明确缓议。
