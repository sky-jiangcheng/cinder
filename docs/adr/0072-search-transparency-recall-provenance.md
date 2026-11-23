# ADR-0072：搜索透明度——召回来源徽章与来源类型筛选

- **状态**：Accepted（2026-11-23，用户体验优化路线 c 拍板落地）
- **日期**：2026-11-23
- **关联**：ADR-0012（语义检索）、ADR-0022（来源类型，待决②本次关闭）、ADR-0038（知识页一体两面）、ADR-0070（时间轴臂）、ADR-0071（融合调参）

## 背景

检索质量批次（ADR-0066～0071）把 recall@10 拉到 0.986、ndcg@10 拉到 0.873，但**用户看不见这些增益如何发生**：

1. 首页问答卡片有召回来源徽章（fts/graph/semantic/fact），知识页搜索与 Cmd+K 面板却什么都不显示——同一条笔记在两个表面呈现不同的透明度，「为什么搜到这条」没有可见答案。
2. 语义检索开关埋在设置 → AI 深处，搜索现场无法知道当前是纯词法还是混合召回。
3. ADR-0022 给笔记加了来源类型 badge（agent-import/user-conversation/project-extract/manual），但待决②「搜索/列表按来源筛选」一直等需求信号——本次体验分析确认搜索是每天 5 个入口的高频面，信号成立。

## 决策

1. **SearchHit 增加两个可选字段**：`recall []string`（lexical | semantic | temporal，按融合阶段序、可多值——一条笔记被多臂召回就带多个）与 `source_type`（note 命中的派生来源桶，todo 为空）。纯增量 → **CoreVersion 2.3.0 → 2.4.0（minor）**，`cmd/mcp/main_test.go` 的 `wantCoreVersion` 同步，check-version-drift 守门。
2. **来源标注在 service 层完成，不改排序**：db 出口统一打 lexical 底标（`markLexicalRecall`）；`fuseRankList` 增加 channel 参数，臂召回的每个 id 追加对应通道（去重、保阶段序）；标注是纯元数据，融合顺序逐字节不变。语义关闭/嵌入失败时命中诚实地只带 lexical——徽章反映**实际发生的召回**，不是配置宣称。
3. **来源类型复用单一派生映射**（`pluginruntime.SourceTypeFor`，ADR-0022 决策）：service 出口一次批量查询（新增 `db.NoteSourcesByIDs`）填充 note 命中的 source_type；查询失败只留空字段、记日志，**绝不让搜索失败**。不物化列（ADR-0022 待决③继续搁置，无 schema 变更）。
4. **前端三处对齐**：知识页搜索与 Cmd+K 面板显示召回徽章（复用 home.sourceFts/sourceSemantic 文案 + 新增 sourceTemporal）；搜索框旁语义状态 pill（读 config，未知则隐藏不猜）；结果含 ≥2 个来源桶时出现筛选 chips（客户端过滤），失效筛选在渲染期降级为「全部」而非 effect 重置。
5. **ADR-0022 待决②关闭**：筛选以客户端形式落地；待决③（物化列）的触发条件不变——按类型过滤成为高频服务端路径时再考虑。

## 代价

- 每次搜索多一条本地批量 SQL（note 命中 id → source），人类规模结果集下可忽略。
- 徽章增加视觉密度：用三级色小徽章（与 hitType 同表面）压制噪声，不引入新语义色。
- recall 是「臂能召回什么」不是「为什么排这个位置」——RRF 分数仍不外露（SearchHit.Rank 是 bm25 语义，混用会误导）。

## 翻转触发

- 真实使用中徽章被证明是噪声 → 收进设置开关，默认隐藏。
- 来源筛选成为高频路径且客户端过滤不够（结果窗口截断导致筛后过少）→ 物化 source_type 列 + 服务端过滤（ADR-0022 待决③）。

## 落地记录（2026-11-23）

- 后端：`internal/domain/types.go`（两字段）、`internal/db/vecindex.go`（NoteSourcesByIDs）、`internal/service/search.go`（markLexicalRecall/appendRecallChannel/annotateSourceTypes + 三搜索函数接线）、`search_semantic.go`/`search_temporal.go`（channel 参数）、`internal/version/core.go` + `cmd/mcp/main_test.go`（2.4.0）。
- 前端：`web/src/api/types.ts`、`pages/Knowledge.tsx`（状态 pill + 徽章 + 筛选）、`components/CommandPalette.tsx`（徽章）、两份 module.css、locales zh/en 同批（6 新键）。
- 测试：后端新增 `search_recall_test.go` 4 例（纯词法/语义通道/时间轴通道/todo 保持 lexical），52 包全绿；前端新增 Knowledge 1 例（徽章 + 状态 + 筛选交互），vitest/build/lint 门。
- 真机验证（2026-11-23，headless dev server + 浏览器）：活库 API 命中带 recall=['lexical','semantic'] 与派生 source_type；知识页渲染 Vector on 状态 pill、来源 chips、Full text/Vector 徽章；点 Manual 筛选 35→1，且该命中仅带 Vector 徽章（词法零匹配、纯向量召回）——徽章诚实反映实际召回。