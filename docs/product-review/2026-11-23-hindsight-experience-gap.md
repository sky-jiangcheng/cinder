# 对标 Hindsight：极致体验还缺什么（2026-11-23）

> 仅中文研究稿，不进手册镜像（`product-review/` 整棵子树在 ZH_ONLY 白名单）。
> 决策结论见 [ADR-0065](../adr/0065-hindsight-absorb-not-replace.md)：不替换底层，定向吸收；对标归因与布局结论见 [ADR-0075](../adr/0075-vscode-hindsight-experience-positioning.md)。本篇是缺口清单全文，**所有条目均未排期**，逐项另行拍板。

## 0. 体验的度量先换掉

Hindsight 引用的 AMB sdebench（61 个真实仓库任务 ×3 轮）给出一个反直觉结论：**记忆不提高解题率**（有无记忆都是 60–61/61），它降低的是**每任务人工纠正次数**（Claude Code 0.85→0.36、Codex 1.34→0.47）与 token 成本。机制：一次纠正 = agent 猜错一个项目特有决策 → 你打断 → 解释 → 重做，一个来回的 token + 一个来回的注意力。把几个月前 commit message 或会话里定下的决策（舍入规则、重试白名单、tie-break 策略）**在开工前递到 agent 面前**，猜测和来回就一起消失。

「体验做到极致」的北极星因此不是检索指标，是**纠正次数**。recall@10 是手段，不是目的。

## 1. 事实基线（当日核实）

Hindsight：vectorize-io/hindsight，MIT / Python / ~62.7k stars / v1.2.3（2026-11-19）。Postgres+pgvector（嵌入式 pg0），retain/recall/reflect 三操作，四层记忆（facts→experiences→observations→mental models/pages），四路并行检索+RRF+cross-encoder 重排，三个开源小模型（决策 0.8B / 重排 114M / 嵌入 112M），Memory Defense 45 模式，22 个 coding agent 一键接线。每次 retain 必过 LLM。

Cinder 现状修正两处调研初判：**RRF 已有**（`internal/search/hybrid/hybrid.go` FuseRRF k=60，已接生产）；**评测基建已有**（`cmd/abeval` / `cmd/searchbench` / `cmd/queryset` / `queries.jsonl`）。ADR-0018 双 lane 已 Accepted。向量/图门默认关是隐私授权决策（ADR-0012/0018），不是欠账。

## 2. 缺口清单（按体验维度）

### 2.1 会话开局注入 —— 最大体验缺口【P0】

- **对方**：会话启动即注入 curated pages（架构/约定/在途事项），agent 开工前就拿到项目决策；摄入全自动（git 历史 + 会话后台流入，无 setup 命令），另有一次性 codebase survey。
- **我方**：拉模式——AGENTS.md 受管区块（ADR-0043）指示 agent「开局调 `repotory_context`」，但**调不调取决于 agent 自觉**；hook 只有 Claude Code SessionEnd（ADR-0010，默认关），没有 SessionStart。指令缺席时生产者路径全断的事故已经发生过一次（ADR-0043 起因：6 场会话 0 条 handoff）。
- **吸收形态**：SessionStart hook 把 `repotory_context` 简报 + 已批准页摘要**推进**会话上下文（Claude Code / Codex 等支持 hook 的宿主）；不支持 hook 的宿主维持受管区块。零模型路径不变——注入的是已落库文本，不调 LLM。
- **不破坏**：注入内容只含已批准页与 handoff（闸门后的东西），pending 永不进注入。

### 2.2 一键接线与卸载【P0】

- **对方**：`install all` 一条命令接线全部检测到的 agent（22 家，各自原生形态：hook/plugin/MCP），`uninstall all` 精确移除所加内容，`stats` 看各 agent 使用频率，autoUpdate 后台自更新，安装时问一次「记忆放哪」（cloud/self-hosted/daemon）。
- **我方**：桌面「接入 AI 客户端」一键四家（claude/cursor/vscode/windsurf，`internal/agentinit`）；`cinder-capture` 支持 6 家但靠指令块兜底；无 CLI 形态的 install all，无使用统计。
- **吸收形态**：把 agentinit 的四家契约扩成 CLI 子命令（`cinder init --all`），复用既有 Go/mjs 双实现契约钉死的纪律；`stats` 可后做（依赖 2.1 的注入留痕）。
- **代价**：每多接一家宿主是一份长期维护契约（对方 22 家是全职团队的覆盖面，单人维护应按本机实际在用的 agent 排序，不追数量）。

### 2.3 内置 secrets/PII 防线【P1】

- **对方**：Memory Defense，45 种内置模式扫每条 retain，redact（`[REDACTED:github_token]`）或 block，per-bank opt-in。
- **我方**：aiaudit（ADR-0030）出口侧审查只有**用户自定义正则**，规则示例是 placeholder，四门全默认关。入口侧（导入器/handoff 写入）无扫描。
- **吸收形态**：内置规则库（token/key/密码/证件号等常见模式）作为 aiaudit 的出厂预设 + 入口侧写入前 redact 选项；默认关的口径不变（ADR-0030 四条硬约束），但「开」的时候不再需要用户自己写正则。
- **为什么 P1**：本地优先产品的信任卖点；且 handoff 自动捕获把会话转录写进库，泄密面真实存在。

### 2.4 已批准页磁盘投影【P1】

- **对方**：`hindsight fs mount` 把 pages 投影成真实目录树 + markdown（YAML frontmatter），后台保鲜；agent 用 `ls/cat/grep/rg`/编辑器/文件工具直接消费，零 SDK 零新词汇。另有 observation tree：免模型调用浏览全部信念。
- **我方**：已批准页只在 `/knowledge?view=pages` 与 MCP `wiki_search/wiki_read` 里（ADR-0032）；AGENTS.md 受管区块是「指令」的投影，不是「知识」的投影。
- **吸收形态**：approved-only 只读镜像目录（如 `<data>/pages/<project>/<slug>.md`），批准/修订时同步，删除即回收；frontmatter 带 slug/批准时间/版本。agent 的文件工具天然可达，**这是把「人批过的知识」递到 agent 手边最短的路**，与 2.1 互补（hook 推简报，文件放全文）。
- **不破坏**：只投影 approved（闸门语义不变）；镜像是派生物不是第二真值源，禁手改（同 docs html 纪律）。

### 2.5 重排与时间轴检索【P2，先评测】

- **对方**：四路并行（向量/BM25/图/时间轴）→ RRF → cross-encoder 重排；时间范围查询（「去年春天做了什么」）是一等公民。
- **我方**：FTS trigram + CJK 松弛 + RRF 语义融合 + 分阶加性图检索（PPR）已在生产；**无重排**；**无时间轴通道**（handoff 有时间线文本但没有按时间过滤的检索）；图是 wiki 页受控关系词表，不是实体因果图。
- **吸收形态**：重排候选 `hs-rerank-1`（MIT、114M、八语言），走可选本地端点（Ollama/ONNX 服务），默认关；时间轴检索可先在 FTS 层加时间过滤 + handoff 时序加权，不必上图。
- **纪律**：ADR-0018 的教训在前——两种融合形态实测被否（等权 -0.182 / RRF 加权 k<7 为负）。**任何新检索形态先过 abeval 真实标注 A/B 门，负结果照记**，不凭对方基准直接上。

### 2.6 observations 式证据修订 + 小模型分流【P3，治供给】

- **对方**：retain 后自动 consolidate 成 observations——去重信念 + 原文证据引用 + proof count，新证据**修订**而非覆盖，历史保留；近重复合并默认开（cosine 0.97）；freshness awareness（新记忆未合并时 reflect 视相关 observation 为 stale 并对照原始事实核验）；决策小模型分流：复述→挂证据、闲聊→跳过、改变/新增→才给 LLM，省 40% 调用。
- **我方**：grep 零命中，纯空白。最接近的是消歧合并（ADR-0019，人触发）与 synthesis（ADR-0020）。而 wiki 层真问题是**供给不足 + 40% 拒绝率**（2026-10-29 停止扩产）——LLM 草稿质量不够，人审拒绝率高，批准页长不出来。
- **吸收形态**：① 证据修订做成 pending 形态——consolidation 产物带证据引用与 proof count 进审核队列，人批后才生效（闸门不破）；② 小模型分流先于大模型编译：复述/闲聊在本地小模型层挡掉，只有「改变/新增」进 LLM 起草——**同时治成本与拒绝率**（草稿少了、准了，人审负担降）。③ proof count/证据引用在 Review 页可见，批准决策有据可查。
- **为什么 P3**：依赖 LLM 端点与评测先行，且是供给链改造不是单点功能；但它是「知识自己长出来」的长期解，2.4 的投影面越有用，供给越要紧。
- **落地记录（2026-11-24）**：② 的**分流目标**已由 [ADR-0074](../adr/0074-compile-triage-deterministic-routing.md) 落地，但形态改为**确定性结构规则**而非本地小模型——Cinder 单 AI 端点，加小模型破零 CGO 单文件，同端点分类反增调用；活库 58% 流水账带统一 `kind=log` + 标题形状，规则即可在模型调用前拦截（seam 断言 `chat.calls==0`）。① 证据修订（observations pending 形态 + proof count）仍未落地，是 ADR-0073 的翻转触发之一。

### 2.7 记忆可见性【P3】

- **对方**：observation tree 免模型浏览全部信念；证据引用 + proof count 全程可见；belief 演化有历史。
- **我方**：Review 五 tab、问答带 Sources、`page_versions` 时态快照已在库；**无记忆时间线视图**（交接时间线只在 `repotory_context` 文本里），信念演化无可视化。
- **吸收形态**：仪表盘「会话」tab 加时间线视图（handoff 已是带时间戳的笔记序列，纯前端）；证据计数展示并入 2.6。

### 2.8 明确不抄的

- **disposition traits**（skepticism/literalism/empathy）：对话人设，与 coding 记忆无关。
- **云/多租户/Oracle/Prometheus/Helm 企业面**：与本地单文件承诺正交，抄了就是 B 路线的代价。
- **reflect 深思考**：`repotory_ask` 证据问答（ADR-0033）已占住「带出处的答案」这个位；再加一层自由反思会稀释闸门语义。
- **22 家 harness 的覆盖面竞赛**：单人维护追不动，按本机在用排序（2.2）。

## 3. 优先级汇总

| 级 | 项 | 打的指标 | 依赖 |
|---|---|---|---|
| P0 | 2.1 会话开局注入 | 纠正次数（直接） | hook 宿主调研 |
| P0 | 2.2 install all CLI | 接入摩擦 | agentinit 契约扩展 |
| P1 | 2.3 内置 secrets/PII 规则库 | 信任 | aiaudit 既有钩子 |
| P1 | 2.4 已批准页磁盘投影 | 知识可达性 | 无（approved 页已有） |
| P2 | 2.5 重排 + 时间轴 | 检索质量 | **先自建评测集**（ADR-0065 决策 3） |
| P3 | 2.6 observations + 小模型分流 | 供给/拒绝率/成本 | LLM 端点 + 评测 |
| P3 | 2.7 记忆时间线视图 | 可见性 | 纯前端 |

首步动作不变：**先在真实笔记库上建评测集**（问题→应命中笔记），量出 2.5 的缺口，同时给 2.1/2.4 的效果留一把尺（纠正次数可用 sdebench 式前后对照自测）。

## 4. 来源

- GitHub README / docs（gh API 取得，2026-11-23）：`vectorize-io/hindsight` README、`developer/observations.mdx`、`developer/pages.mdx`、`developer/retrieval.md`、`docs-integrations/coding-agents.md`
- 基准：[benchmarks.hindsight.vectorize.io](https://benchmarks.hindsight.vectorize.io/)（LongMemEval 具体数字未抓取核实，不引用）；sdebench 数字转引自 coding-agents.md 的 AMB 表
- 仓内证据：`internal/search/hybrid/hybrid.go`、`cmd/abeval`、`internal/agentinit`、`internal/aiaudit`、`internal/service/scan_instructions.go`、TODO.md 现状速查、ADR-0010/0012/0015/0018/0019/0020/0030/0032/0043