# ADR-0075：对标 VS Code / Hindsight 的体验结论——同层竞争、全栈差异与布局减法

- 状态：**Accepted（2026-11-24，用户要求补 ADR；本篇只固化分析结论，不改代码、不改产品承诺）**
- 日期：2026-11-24
- 关联：[ADR-0065](0065-hindsight-absorb-not-replace.md)（总路线：不替换、定向吸收）、[ADR-0007](0007-session-memory-protocol.md)（handoff 协议）、[ADR-0015](0015-w3-minimal-compile-loop.md)（人审闸门）、[ADR-0034](0034-desktop-three-column-shell.md) / [ADR-0038](0038-desktop-information-architecture.md) / [ADR-0039](0039-in-page-tabs-over-stacked-sections.md) / [ADR-0042](0042-page-shell-pinned-header-and-scrolling-body.md) / [ADR-0044](0044-ide-panel-windsurf-shape.md) / [ADR-0050](0050-family-design-standard.md)（布局减法既有决策）、`docs/product-review/2026-11-02-vscode-session-memory.md`、`docs/product-review/2026-11-23-hindsight-experience-gap.md`

## 背景

2026-11-02 的 VS Code 调研与 2026-11-23 的 Hindsight 对标，分别落到两份产品评审稿；ADR-0065 只记录了总方向（不替换底层、定向吸收），没有记录三组更细的结论：① VS Code 级体验下 Cinder 还有哪些布局可合理化；② Hindsight 是否只是存储层；③ Hindsight 全栈里最重的几段与 Cinder 出入为何这么大，是否因为对方要做商业产品、要云平台化。

本篇把这三组结论从研究稿/对话里抬到决策层，避免后续把对标重新做成功能军备竞赛。**本篇不新增功能、不排期、不改 schema / MCP 工具面 / CoreVersion。**

## 事实

### 1. VS Code 侧有三层，不能当一件事看

- **session 存储层**：本地 SQLite、默认开，但属 IDE 私产；`microsoft/vscode#318855` 仍是 proposal，扩展没有受支持 API 读别人的 session。
- **session 同步 / Copilot Memory**：云端、GitHub 账号绑定、opt-in sync，Repository/User 两种 scope，28 天未用自动删。
- **local memory files / Chronicle**：User memory 前 200 行开局注入；`/chronicle:*` 在本地 session 索引上提供检索与 standup。

VS Code 的 “Hand off a session” 是**运行时换 harness / 换执行环境 / 换角色**，携带完整对话历史；Cinder 的 `repotory_handoff` 是**持久化交接记录**。同名不同物，是产品语言事故的高发点（研究稿 2026-11-02 已记）。

### 2. 真正的同层竞争只有三处

「上一轮干了什么」的第一直觉入口、Copilot Memory 的 Repository scope、发现并打开 Claude Code / Codex session。三处都压缩**用户回忆**的成本，但不吃掉 Cinder 的位置：**跨 agent 中立 + 协议可读 + agent 主动写结构化结论 + 本地无账号 + 人审默认**。流水 ≠ 结论，发现 ≠ 读取，IDE 内部实现细节 ≠ 可被任意 MCP client 消费的知识资产。

### 3. 布局差距的结论是“确认减法”，不是“加面板”

对标 VS Code 级设计体验后，可合理化的点与 ADR-0034 / 0038 / 0039 / 0042 / 0044 / 0050 已拍板的方向一致：主次重构、常驻右栏改上下文化、正文 `max-width:72ch` 保持、悬浮机器人不压正文、页内 tab 替代堆叠区块、页头固定/正文滚动、chrome 减法。

**结论：没有新增布局决策。** 这次对标只是确认既有减法方向仍然正确；不为对齐某个 IDE 的布局再开一轮 UI 改造。若真要做新布局动作，必须另开 ADR 并给出真机证据与纠正次数指标，不靠截图对比立项。

### 4. Hindsight 不是存储层

Hindsight 是**带抽取、检索、注入、接线、企业运维的全栈记忆产品**：Postgres + pgvector 只是它的存储选择；真正重的是 `retain` 必过 LLM 抽取、四层记忆（facts → experiences → observations → mental models/pages）、自动 consolidate 与证据修订、四路召回 + RRF + cross-encoder 重排、三个自训小模型、Memory Defense、22 家 harness 一键接线与 `fs mount`。把它读成“一个更好的数据库”会误判差距性质——差的是产品栈，不是 db 选型。

### 5. 三段出入的根因是目标函数不同

对方最重的三段——自动摄入 + LLM 抽取、consolidate / 分流 + 证据信念、检索重排 + 全 harness 接线与云运维——与 Cinder 出入大，根因是**商业公司全职维护、要走云平台化与多租户企业面**：覆盖更多 harness、零配置摄入、全栈体验是增长路径；Postgres / Docker / Helm / Oracle / Prometheus 是这条路径的运维形态。

Cinder 的目标函数是**本地单文件、零账号、协议中立、人审默认、零模型捕获路径**，单人维护按本机实际在用的 agent 排序，不追 22 家覆盖面。差异不是“能力落后清单”，是两套产品目标。照搬对方全栈等于走进对方主场，也等于放弃 ADR-0006 的范围冻结与 ADR-0065 的护城河。

## 决策

1. **维持 ADR-0065 的 A 路线**：不替换底层、不建混合双库；本篇不改代码、不新增吸收项排期。
2. **对标按“同层 / 异层”归类**：VS Code 的 session 存储与检索是对标但不打的功能层；Hindsight 的摄入 / consolidate / 重排 / 接线是全栈产品层，只走 ADR-0065 已定义的定向吸收，逐项由后续 ADR 拍板（已落地的见 0066–0074，未排期的仍不排期）。
3. **布局层只确认既有减法**，不新开 UI 改造；真机证据 + 纠正次数指标是后续任何布局动作的前置门。
4. **明确不抄商业面**：云 / 多租户 / Helm / Oracle / Prometheus 企业运维面、22 家 harness 覆盖面竞赛、disposition traits、reflect 深思考——与本地单文件承诺正交，单人维护追不动；`repotory_ask`（ADR-0033）已占住“带出处的答案”这一位。
5. **体验北极星不变**：减少人工纠正次数，不是解题率，也不是 recall@10。

## 后果

- 得：避免把竞争信号误读成功能清单；护城河（人审默认 + 本地单文件 + 零模型路径 + 跨 agent 协议中立）不被对标稀释；已落地的 0066–0074 有明确的归位。
- 失：接受覆盖面、云同步、企业运维面与自动 consolidate 上的对外差距；这些差距是选择的结果，不假装补齐。
- 代价承受方式：只对“能直接降纠正次数且不破三条承重边界”的项单独立项；其余留在研究稿里当观察信号。

## 翻转触发

- 外部需求变成“必须拿 Hindsight 生态当卖点” → 重开 ADR-0065 的 C 路线（混合双库），不在本篇扩权。
- 真机数据证明既有布局减法仍显著推高纠正次数 → 新开布局 ADR，带前后对照。
- VS Code Chronicle 从流水汇总进一步输出结构化会话结论，并成为用户回忆的第一入口 → 重做 2026-11-02 研究稿第 4.3 节的信号评估，再决定是否动 `repotory_context` 的呈现形态。

## 落地记录

- 2026-11-24：用户确认“补”。本篇与中英索引、两份产品评审稿的指向同批落库；无代码改动。
