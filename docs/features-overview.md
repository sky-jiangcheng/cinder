---
title: 功能总览
order: 2
---

# 功能总览

> 层级图例：**Core**（补完 发现 → 理解 → 沉淀 → 检索 → 交给 AI 闭环）｜ **Support**（让闭环可读）｜ **Experimental**（保留不再扩张）｜ **Paused**（不再投入）——见 [ADR-0006](adr/0006-scope-freeze.md)。本页是全量功能表；每个能力的深入手册见侧栏「功能手册」各页。

**定位**：这一页列的是**本地优先、agent 中立**的记忆层。记忆层不绑 harness——任何会讲 MCP 的编程 Agent（Claude Code / Cursor / Codex / Copilot 等）读写的是同一份记忆，靠的是协议而不是某个插件。**无账号、无云端**：整份记忆落在本机一个 SQLite 文件里。之所以要有一层中立的：各家 IDE / agent 自己的会话记录都锁在自家生态内部（VS Code 的 session 索引扩展至今读不到），跨 agent 的那一层需要一个中立的落点。

## 功能

> 分级图例：**Core**（完成「发现 → 理解 → 记录 → 检索 → AI」闭环）| **Support**（让闭环可读可感）| **Experimental**（保留但不扩展）| **Paused**（不再投入）。见 [ADR-0006](adr/0006-scope-freeze.md)。

### 知识库（Core）

| 功能 | 说明 |
|------|------|
| Markdown 笔记 | 标题 / 标签 / 分类（knowledge · journal · ideas · other）/ 置顶 / 跨项目移动；草稿自动保存 |
| 块编辑器 | 输入 `/` 打开块面板，插入 callout、标签页、折叠块、代码、Mermaid、数学、表格……拖拽排序；输出仍是纯 Markdown（**Experimental**：复杂块暂停扩展） |
| 富文本渲染 | highlight.js 代码高亮、Mermaid 图表、KaTeX 数学、GFM callout 与任务列表 |
| FTS5 全文搜索 | trigram + bm25 相关度排序、片段高亮，覆盖笔记与待办；短 CJK 查询自动回退 LIKE |
| 版本历史 | 每次保存自动快照；任意版本与当前版本行级 diff，一键恢复 |
| 全局搜索 | ⌘/Ctrl+K 命令面板 + 独立搜索页（联邦检索：仓库 / 笔记 / 待办） |

### AI 就绪接口（Core）

| 通道 | 说明 |
|------|------|
| MCP 服务器 | `repotory-mcp` stdio 服务器，18 个工具（仓库扫描 + 上下文注入 + 会话交接 + 笔记 CRUD/整理 + 项目查询 + 搜索 + 四项自检 + 知识页检索）；可接入 Claude Code / Cursor 等（AI 执行动作的唯一通道） |
| `repotory_scan` | 一次冷启动：播种默认扫描根并同步扫描，发现本地 Git 仓库。纯 MCP 安装（无桌面应用）也能把知识库从零建起来 |
| `repotory_context` | 会话开始一次调用拉齐项目全貌：技术栈 / README 摘要 / 依赖 / 最近提交 / 未完成待办 / 高相关笔记（交接笔记优先）——替代 3-4 次链式查询 |
| `repotory_handoff` | 会话结束时结构化交接：summary / changes / decisions / gotchas / next_steps 渲染统一模板并落库；下一个会话（任何 Agent）自动读到 |
| llms.txt | `GenerateLLMsTxt` 输出面向 LLM 的知识库 Markdown 总览 |
| 笔记导出 | 任意笔记导出为带 YAML frontmatter 的 `.md` |
| 知识源导入 | 把其它 Agent 的记忆带进来：**Claude**、**Codex**、**OpenCode**、**CodeBuddy**（含 CodeBuddy 记忆）、**Cursor**、**OpenClaw**、**Hermes**——记忆类源按情况随启动自动导入；会话转录类源一律**显式手动触发**（白名单限定）。幂等 upsert：重跑是更新，不是堆叠 |
| 会话自动捕获（M1） | 按需抓取项目最近一次 Claude Code 会话转录成交接笔记；`repotory-capture` CLI 承载 B 端 SessionEnd hook 自动化。**默认关**（`claude_session_capture`），隐私门控 |
| 便携记忆导出 | `ExportMemoryJSON` 以 [Open Memory Protocol](https://github.com/SMJAI/open-memory-protocol) 风格导出知识库（临时口径；OMP 尚未 1.0） |
| 数据完整性自检 | `repotory_integrity` 六项只读检查：FTS 索引漂移、孤儿行、schema 形状与版本戳、扫描覆盖、知识缓存新鲜度、版本快照孤儿。索引漂移会让搜索**静默漏结果**且无其它机制能发现——这是唯一的检测手段 |

### 知识图谱 & LLM Wiki（Support——人把关）

受控关系词表组织的编译页面，加上一道「人是闸门」的审核队列：

| 功能 | 说明 |
|------|------|
| LLM Wiki 编译器 | 把笔记编译成结构化 wiki 页面（entity / concept / synthesis …），一律为**待审**草稿；编译器永不改已批准页（[ADR-0015](adr/0015-w3-minimal-compile-loop.md)） |
| 审核队列 | 批准 / 拒绝 / 删除待审页；被拒页保留为墓碑（`reject ≠ delete`） |
| 页面版本 | 每次页面变更由数据库触发器快照进 append-only 的 `page_versions` 表；行级 diff 与恢复（[ADR-0021](adr/0021-temporal-graph-page-versions.md)） |
| 合并建议 | 结构候选发现（共享来源 / slug 词形 / 标题重叠 / 已互链——至少两个独立信号）；模型只加第二意见，**合并只能由人采纳**；合并留下墓碑并重指向边（[ADR-0019](adr/0019-entity-resolution-merge.md)） |
| 社区摘要 | 在已批准图上跑 Label Propagation，按主题起草 synthesis 页；**opt-in 门**（`wiki_community_summary`），硬预算，外发字节可审计（[ADR-0020](adr/0020-community-synthesis.md)） |
| 图感知取证 | `evidencePages` 沿白名单关系词表 1 跳扩召回 + 纯 Go PPR 重排；**默认关**（`wiki_graph_search`），关闭时零行为回退（[ADR-0018](adr/0018-knowledge-graph-foundation.md)） |
| 语义检索（M3） | 可选的 FTS5 + 向量混合召回（RRF 融合），**向量库可插拔**：本地 **sqlite-vec**（默认，纯 Go/零 CGO）或远端 **Qdrant** / **Weaviate**（不可达自动回退本地）。**默认关**，由 A/B 评估门（`cmd/abeval`）决定是否开启（[ADR-0012](adr/0012-semantic-search.md)、[ADR-0013](adr/0013-vector-database-selection.md)） |

### 内置备份（Core）

本地优先的数据库只躺在一块盘上，离灾难就差一个坏扇区：

| 功能 | 说明 |
|------|------|
| 每日本地快照 | 应用每天第一次打开时用 `VACUUM INTO` 给数据库拍一份快照，落在 `<数据目录>/backups/`；保留 14 份、权限 0600，**默认开、零配置**（[ADR-0025](adr/0025-backup-recovery.md)） |
| 恢复 = 拷回一个文件 | 快照是完整独立的 SQLite 文件，恢复就是换文件；已在真库上端到端演练 |
| 异地副本（opt-in，规划中） | Litestream 式 WAL 流式归档或快照上传 S3 兼容存储——ADR-0025 L2 只定型未实现。红线：活库永不放进云盘同步文件夹 |

### 仓库挖掘（Core）

项目详情页自动抽取：README 摘要、技术栈清单（识别 20+ 清单格式）、语言 LOC 占比、依赖列表（npm / go.mod 含 block require / cargo）、主要贡献者、活跃统计、最近提交流；结果缓存进 `repo_meta` 避免重复扫描。

### 仪表盘与统计（Support）

> 首页是单栏工作台：知识库智能助手、记忆环条（最近交接 + 全局搜索入口）、重点项目速览与今日动态聚合在一页；`/search` 是独立的全局搜索页；主导航顺序为 首页 → 仪表盘 → 搜索 → 知识库 → 审核 → 设置。

| 功能 | 说明 |
|------|------|
| 自动发现仓库 | 配置扫描根递归发现全部 Git 仓库；默认值随平台自适应 |
| 可视化仪表盘 | 每日目标进度环、项目卡片、趋势折线（7 / 30 天 / 全部）、提交热力图 |
| 仓库收藏 | 收藏仓库显示完整统计卡片；未收藏只显示名字，按需跟进 |
| 按需历史回填 | 收藏卡片上的「回填历史」抓取该仓库最近 365 天的每日统计 |
| 智能项目分组 | 自动识别 monorepo 与单仓库；手动拆分/合并单事务执行（笔记与待办随项目移动） |
| 工作日检查 | 自定义每日代码目标，未达标提醒 |
| 状态栏 | 实时展示最近一次提交（仓库 / 分支 / 时间，30s 缓存） |
| 主题切换 | 亮色 / 暗色 / 跟随系统，重启后保留 |

### 其它

| 功能 | 说明 |
|------|------|
| i18n | 中 / 英一键切换（react-i18next，zh-CN + en） |
| 单文件跨平台 | 单个 Go 二进制，无运行时依赖 |
| 设计上离线 | 无账号、无遥测；只有你显式配置端点时才产生外发 |
