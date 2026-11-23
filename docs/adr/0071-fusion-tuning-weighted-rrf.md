# 0071 检索融合调参——加权 RRF + 锐化 k + 向量扩窗；kind 噪声降权否决

- 状态：**Accepted（2026-11-23，用户批准 ADR-0065 选项 a 前半段「两个零依赖实验」，实验过门后落地）**
- 相关：[ADR-0012](0012-semantic-search.md)（语义默认关 = 隐私授权）、[ADR-0018](0018-knowledge-graph-foundation.md)（检索底座 / A/B 门纪律）、[ADR-0065](0065-hindsight-absorb-not-replace.md)（定向吸收，选项 b 重排的前置实验）、[ADR-0070](0070-temporal-recall-channel.md)（时间轴第三臂）

## 背景

ADR-0070 落地后，70 案生产路径 hybrid+时间轴 **recall@10=0.986 / ndcg@10=0.808**。recall 近饱和（仅 2 案部分失），剩余缺口全在**排位深度**：10 案 rank1≥4，逐类 ndcg knowledge 0.753 / memory 0.687 / untagged 0.792。ADR-0065 选项 b（RRF 调参 → 重排模型）的前置实验：先做零依赖的融合参数实验，再决定是否上重排。

两个实验设计为**捕获一次、本地回放**：`.annotation-prep/fusesweep/capture` 对每案捕获词法 / 向量 / 时间轴三臂的原始有序 id 列表（向量臂只需一次远程查询嵌入，70 案约 4 分钟），之后参数扫全本地、零远程成本。离线回放复刻生产的两段 RRF（先语义融合再时间轴融合），基线配置（k=60 / embedK=20 / tempK=20 / 等权）复现出 **recall=0.9857 / ndcg=0.8097**，与活库门数字一致——回放忠实。

## 决策

1. **加权 RRF**：`hybrid.FuseRRFWeighted(k, lists...)`，每列表带权重，分数 = Σ weight_i/(k+rank_i)。`FuseRRF` 重构为等权（weight=1）特例，签名与行为不变——它的唯一生产调用方是笔记搜索的 `fuseRankList`（`db/pagevec.go` / `wiki_evidence.go` 只在注释提及，实际走 round-robin，不受影响）。
2. **向量臂权重 1.5，词法 / 时间轴臂 1.0**：向量召回在融合里说话更响。时间轴臂权重不动（temporal 类已饱和，调权无增益）。
3. **笔记搜索融合常数 k=10**（`noteRRFK`），锐于通用 `DefaultRRFK=60`。**只作用于笔记搜索**，不改全局常量。k=10 是消除「单独提向量权重会丢 1 案 recall」的关键。
4. **embedRecallK 20→30**：向量召回扩窗。这是 wv=1.5 下保持零 recall 回归的**必要条件**——embedK=20+wv=1.5 会丢 1 案，embedK=30 补回。
5. **kind 噪声降权：否决**。BookStore 案（top-10 被 6 条 `<environment_context>` 日志挤占）看似噪声问题，但全库统计：**86 个相关标注里 52 个是 log 类，47/70 案含 log 类相关笔记，37 个相关笔记本身就是 environment_context 日志**。「噪声」与「答案」同类，降权 log 必然连答案一起降。实测降权 log（任意 0.1–0.7 系数）：**recall 0.986→0.736，19 案回归，ndcg −0.365**。没有干净的 kind 杠杆，此路关闭。

## 证据（70 案，离线扫 + 分半验证）

| 配置 | recall@10 | ndcg@10 | Δndcg | recall 回归 |
|---|---|---|---|---|
| 基线 k=60 / embedK=20 / wv=1.0 | 0.9857 | 0.8097 | — | — |
| 仅 k=10 | 0.9857 | 0.8135 | +0.004 | 0 |
| 仅 wv=1.5（k=60 / embedK=20） | 0.9786 | 0.8515 | +0.042 | **1** |
| **k=10 / embedK=30 / wv=1.5** | **0.9857** | **0.8710** | **+0.061** | **0** |
| kind=log 降权 0.5 | 0.7357 | 0.4443 | −0.365 | **19** |

选中配置 **k=10 / embedK=30 / wv=1.5 / wt=1.0**：

- **零 recall 回归**，ndcg +0.061（远超 0.02 阈值）；18 案 ndcg 提升，仅 2 案微降（Vercel 部署案 0.631→0.500、temporal-literal 一案 1.000→0.993）。
- **分半稳健**：四个独立切分（前 35 / 后 35 / 奇 35 / 偶 35）ndcg 增益分别 +0.071 / +0.051 / +0.073 / +0.050，全部零 recall 回归——增益广布且稳定，不是对单案过拟合。
- 逐类：knowledge 0.775→0.836、memory 0.687→0.877、untagged 0.792→0.858，temporal 不变（已饱和）。
- **生产路径验证（新二进制探针，完整 149 向量快照）**：recall@10=0.9857（与离线预测逐字一致、对未调参基线零回归）、ndcg@10=0.8728（基线 0.8078，+0.065；20 案升 2 案微降），无 429。**注意门指标口径**：本变更是 recall 中性 / ndcg 正向，abeval 的 recall-gain 门（-min-recall）不适用——recall 增益为 0 会误判 FAIL；正确证据是探针的 ndcg 测量 + 零 recall 回归 + 分半稳健。abeval 官方门本轮全量重建撞供应商 429 配额（新错误契约如实 surfaced），未作为本变更的判据。

## 代价（接受）

- **融合行为对所有查询改变**：不再是纯实验，是生产排序变更。
- **在 70 案评测集上调参、又用同一集验证，存在循环验证风险**。缓解：分半稳健 + 零 recall 回归硬约束 + 增益须广布（不追 legacy 单案）。真正的泛化证明需要新增标注案（held-out），列入翻转触发。
- **2 案 per-case ndcg 微降**接受（18 升 2 降，macro 显著正）。
- **权重依赖嵌入模型**：wv=1.5 是对当前 OpenRouter qwen3-embedding-8b 调的；换模型须重调。

## 翻转触发

- 新增 held-out 标注案上增益不泛化 → 回退 k=60 / wv=1.0 / embedK=20。
- 更换嵌入端点 / 模型 → 重跑参数扫（权重与 k 是模型相关的）。
- 出现「向量权重过高挤掉权威词法命中」的真实纠正案例 → 降 wv 或回到等权。
- 重排模型（ADR-0065 选项 b 后半段）：本实验证明融合调参仍有 +0.061 空间，重排是否值得引入端点依赖另行拍板。

## 落地记录（2026-11-23）

- 代码：`internal/search/hybrid/hybrid.go`（`WeightedRankList` + `FuseRRFWeighted`，`FuseRRF` 重构为等权特例）、`internal/service/search_semantic.go`（`noteRRFK=10` / `vectorFuseWeight=1.5` / `embedRecallK=30`，`fuseRankList` 加权重参数）、`internal/service/search_temporal.go`（时间轴臂传 weight 1.0）。
- 测试：hybrid 包 +3（权重缩放贡献、weight≤0 视为 1、FuseRRF 与等权加权等价）；`go build` / `go vet` / `go test ./...` 52 包全绿；gofmt 干净。
- 实验工具（gitignore）：`.annotation-prep/fusesweep/capture`（捕获三臂原始列表）、`sweep.py`（本地参数扫 + 分半 + kind 降权）。
- kind 噪声降权否决与 RRF 调参证据见过程日志同日章节。

## 补注：重排探针第一轮——OpenRouter 弱模型 LLM 重排，负结果（2026-11-23）

- **背景**：用户指示探针改为后端可配置，不再硬依赖 SiliconFlow key。`.annotation-prep/rerank/probe.py`（gitignore）支持 `-backend openrouter|siliconflow`、`-model`、`-reasoning`。第一轮用活库已有 `ai_chat_*` 配置（OpenRouter `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free`，零新增 key），对调参后融合回放的 top-40 候选做 LLM listwise 重排。agnes 核过：封闭平台 provider，无对外 base URL/key，不可借用。
- **仪器校准**：该 nano 模型 reasoning 开着时 40 候选 prompt 烧 16k token / 170s 不收敛（finish=length，content 空）；关掉后 ~3s 出有效 top-10，故默认 `-reasoning off`。
- **数字**（70 案；基线 = 同候选池的调参融合回放 recall@10=0.9857 / ndcg@10=0.8710，与 sweep 离线复现一致）：重排后 **recall@10=0.6857 / ndcg@10=0.569（−0.302）**；22 案 recall 回归、ndcg 9 升 30 降；10 案硬失败（empty ranking，诚实回落基线序计入）、2 案重试救回。逐类：untagged 51 案 −0.376、temporal-relative −0.500，仅 temporal-natural +0.369。
- **门判定：FAIL**（零 recall 回归硬约束破）。按 ADR-0018 记负结果：**该后端不得接生产**。
- **边界（重要）**：仪器弱——免费 nano LLM ≠ cross-encoder。本负结果只关闭「OpenRouter 弱模型 LLM 重排无增益」，**不否定重排天花板**（理论上限 +0.058 ndcg，7 个可修案全在 top-10）。SiliconFlow `BAAI/bge-reranker-v2-m3` 原生探针仍是可选仪器（`-backend siliconflow`，需 key）；重排线整体关闭与否待用户拍板。
- 逐案数据：`.annotation-prep/rerank/rerank-openrouter.jsonl`（gitignore，含 summary 行）。
