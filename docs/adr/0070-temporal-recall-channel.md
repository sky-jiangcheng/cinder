# ADR-0070：时间轴检索通道——日期表达式是结构信号，第三条召回臂

- **状态**：Accepted（2026-11-23）
- **日期**：2026-11-23
- **关联**：[ADR-0012](0012-semantic-search.md)（语义检索与 RRF 融合架构，本通道复用其融合形状）、[ADR-0018](0018-knowledge-graph-foundation.md)（任何新检索形态先过 A/B 门的纪律）、[ADR-0065](0065-hindsight-absorb-not-replace.md)（吸收路线 P2 第一项）、[ADR-0068](0068-builtin-credential-pii-presets.md)（同构论证：格式/结构本身是信号）

## 背景

评测集 v2（70 条标注 query）的 hybrid 逐案复测（2026-11-23，远程端点）把剩余缺口重新定位：hybrid 总体 recall@10 0.945，memory 类从 0.333 升到 1.000，legacy 51 案从 0.745 升到 0.990——**但 temporal 类是向量救不动的唯一一类**：

- `temporal-natural`（「11月17号那天 MiddlePlatform 项目在解决什么问题？」）：词法 0.000，hybrid 仍 **0.000**。
- `temporal-relative`（「上周五的会话交接记录了什么？」）：两臂都停在 0.500。
- `temporal-literal` 一案（「2026-11-17 那天有哪些会话交接？」）hybrid 下 **0.83→0.67 退步**：RRF 把词法命中的两条交接挤出 top-10。

已核实目标笔记都在库里、`created_at` 干净（213 = 2026-11-17、222 = 2026-11-20 周五）。失败的机理是双重的：中文日期「11月17号」与标题里的 ISO 日期「2026-11-17」**共享零个 trigram**；embedding 空间里「那天在解决什么问题」也不携带日历语义。日期是**结构信号**——与 ADR-0068 对结构化凭证的论证同构：它不依赖语义改写，可以用确定性规则精确识别，而两条现有召回臂在原理上就表示不了它。

## 决策

1. **独立 `internal/search/temporal` 包，纯字符串模式，零依赖。** 支持形态：ISO / 斜杠日期（`2026-11-17`、`2026/11/17`）、中文全式（`2026年11月17日`）、月日（`11月17号/日`，**必须带日/号**，裸「11月」不解析）、相对日（今天/今日/昨天/昨日/前天）、周表达式（上/本/这 + 周/星期 + 可选星期几；裸「周X」= anchor 之前最近一次出现）。**MVP 刻意不解析**：时刻（晚上/上午）、裸月份、日期区间、「最近N天」、英文日期——它们落回现有两臂，行为不变。固定 anchor 下解析逐字节确定（测试钉住），这是评测可复现的前提。
2. **窗口语义：本地日历日 → UTC 瞬间区间。** `created_at` 是 SQLite `CURRENT_TIMESTAMP`（UTC），所以本地一天的边界不是 UTC 一天的边界：UTC+8 的「11月17号」= `[2026-11-16 16:00Z, 2026-11-17 16:00Z)`。周一起始周；半开区间；溢出日期（2月30号）拒绝而非归一化进三月。**误报守卫**：「版本周期」「资本周转」包含「本周」但不是日期——「本/这」前一个 rune 是汉字时跳过该候选（「上」豁免：「查上周五」合法）。
3. **第三条 RRF 臂，默认开，无开关。** 与 ADR-0012 默认关的口径不冲突：那条口径管的是**数据出机器**（embedding 端点）与**未审内容**，本通道是纯本地计算，无端点、无新数据暴露，没有需要用户授权的东西。无日期表达式的 query 逐字节走原路径。融合复用从 `fuseSemantic` 提取的共享助手 `fuseRankList`（RRF k=60，窗口召回 K=20），两条臂以同一形状扩展结果。
4. **窗口内两级召回（`db.NoteIDsInWindow`）：先 FTS bm25（严格 AND → 宽松 OR），再窗口内全部笔记按更新时间倒序。** 第二级是本臂存在的理由：日期问句与目标笔记可以共享零个 trigram，纯内容排序会返回空——窗口本身必须供给召回。
5. **作用面：`SearchNotes` + `SearchAll`（检索面）；`SearchNotesStrict`（引用路径）刻意不接。** 窗口命中是「创建时间巧合」不是「内容匹配」，正是 strict 路径拒绝 OR 宽松化的同一条理由（ADR-0012 落地时的「昨日代码提交量」误引用案例）——检索面给人看排序列表，引用面把每条命中当证据呈现，两者容忍度不同。
6. **anchor 走 context 注入（`temporal.WithAnchor`），不进配置。** 生产路径 `time.Now()`；abeval / probe 加 `-anchor YYYY-MM-DD` 钉住标注日——「上周五」这类相对表达式只有在标注日下才保持标注含义，评测门因此可复现。不做配置键：那是给生产开测试后门。
7. **同批落地的健壮性修复（全部来自本次实测缺陷）**：
   - `RebuildEmbeddings` / `RebuildPageEmbeddings` 批失败 bail 时**返回 error**（原契约返回 `(部分计数, nil)`——UI 把端点挂掉报成「重建 N 条」的成功；活库切换端点时实测踩中）。持久队列照旧保留未完成行，失败可见与数据不丢两条都守住。
   - `RemoteEmbedder` 读 body 的错误不再被吞（原 `raw, _ := io.ReadAll` 让网络截断伪装成 JSON 解析错误）；8 MiB 响应上限改为**显式检测**（读 cap+1 字节，超限报错而不是静默截断）。
   - 默认超时 30s→**120s**、`embedBatch` 64→**16**：实测远程端点每请求固定开销 ~15s，64 条长笔记一批在 60s 内读不完 body；16 条批 + 120s 在 provider 429 限流下重试一次即完成 156 条全量重建。

## 证据（A/B 门，ADR-0018 纪律）

词法臂 + 时间轴（语义关，快照 149 笔记，anchor=2026-11-23）：

| 类 | n | 词法 | 词法+时间轴 |
|---|---|---|---|
| temporal-literal | 3 | 0.611 | **1.000** |
| temporal-natural | 1 | 0.000 | **1.000** |
| temporal-relative | 2 | 0.500 | **1.000** |
| 其余各类 | 64 | — | **逐案零退步** |

总体词法 recall@10 0.719 → 0.764。hybrid + 时间轴的完整门数字见落地记录。

## 代价（接受）

- **模式面窄**：不解析的形态静默落回现有臂（上面逐条列出）；月日不带年时取 anchor 年——12 月问「1月2号」解析成今年而非明年，记忆工具问过去为主，接受。
- **两级 RRF 不是三表一次 RRF**：先语义融合再时间轴融合，分数与三表一次融合不同。选它是为了 `fuseSemantic` 一行不改、两臂各自独立降级；实测零退步。若未来出现两臂互相挤压的案例，再考虑合并成单次三表融合。
- **窗口臂按创建时间召回，不按内容**：繁忙的一天最多灌 20 条进融合；内容命中笔记的最终排位仍由内容臂决定。
- **裸周几启发式**（anchor 之前最近一次）可能误读面向未来的问法（周一说「周五开会」解析成上周五）——记忆工具的 query 关于过去，接受。

## 翻转触发

- 实测出现守卫漏掉的误报（「版本周期」类）→ 收窄模式，不加白名单机制（与 ADR-0068 同一纪律）。
- 出现可靠的本地日期 NLP → 重估正则模式面是否降级为其兜底。
- 「最近N天」/区间/时刻形态出现真实评测案例 → 按同一 A/B 门扩模式面；没有案例就不扩（本 ADR 的窄面是刻意的）。

## 落地记录（2026-11-23）

- 代码：`internal/search/temporal/temporal.go`（Parse / Window / WithAnchor / Anchor）、`internal/db/search_temporal.go`（NoteIDsInWindow 两级召回）、`internal/service/search_temporal.go`（fuseTemporal）、`search_semantic.go`（fuseRankList 提取 + bail 报错 + embedBatch 16）、`search.go`（SearchNotes / SearchAll 接线，Strict 注释钉住不接）、`page_embed_drain.go`（bail 报错）、`embedder_remote.go`（读错误 / cap 显式化 / 120s）、`cmd/abeval/main.go`（-anchor）。
- 测试：temporal 包 27 个解析案 + 本地日边界 / anchor / 确定性 3 个；db 4 个（FTS 级 / 兜底级 / 本地日边界 / 空窗）；service 4 个（语义关下日期独召回、无日期逐字节不变、Strict 不接、SearchNotes 同臂）；embedder 截断响应 1 个；bail 契约测试更新为「必须报错且队列保留」。`go build` / `go vet` / `go test ./...` 52 包全绿。
- 门：词法+时间轴 temporal 6/6 全绿、70 案零退步（上表）。**hybrid+时间轴完整门（同日晚，-anchor 2026-11-23，快照 149 向量重建后）：recall@10=0.986 / ndcg@10=0.808，delta +0.221 / +0.189，GATE PASS**；对比无时间轴的 hybrid 基线 0.945 / 0.766，逐案 70 案零回归、3 案提升全部是 temporal 类（0.667→1.000、0.000→1.000 ×2），temporal 6/6 全 1.000 且 5 案 rank1=1（natural 案 rank1 从词法+时间轴的 10 升到 2——向量臂把窗口召回托上了头部）。SearchNotes 连续性臂与生产路径数字一致。
- **活库端点切换（用户拍板，ADR-0012 隐私授权）**：`embedding_base_url` 切 `https://openrouter.ai/api/v1`、`embedding_model=qwen/qwen3-embedding-8b`、`embedding_dim=4096`、key 从活库 `ai_chat_api_key` 运行时复制；156 条笔记全量重建完成（首试 provider 429，重试即过——新报错契约首次实战即生效）。桌面 app 旧二进制仍在跑旧批量/超时参数，索引本体已正确，drainer 按新维度自维护；参数修复随下个构建生效。