# Cinder 定位与叙事重构简报

## 一句话定位（对外）

**Cinder 是本地优先的“跨 agent 项目记忆层”**：帮你快速理解本地 Git 项目最近发生了什么、沉淀了哪些可检索知识，并在会话边界自动把这些记忆交给任何 AI agent 继续使用。

## 用户价值（做减法后的承诺）

1. **更快地发现哪些项目值得关注**
2. **更快地理解当前项目状态**（README、技术栈、依赖、活跃度）
3. **把零散知识沉淀到可检索的地方**（Markdown + 搜索 + 版本历史）
4. **降低“查找上下文”的时间成本**（全局检索、命令面板、项目跳转）
5. **把项目知识交给 AI 继续工作**（llms.txt、导出、MCP）

## 产品边界（四层分级）

### 1) 必须持续投入（核心）
- 本地仓库发现与项目分组
- 项目知识沉淀与检索（笔记、标签、版本历史、全文搜索）
- 项目理解能力（README/技术栈/依赖/贡献者/活跃度）
- AI 上下文出口（llms.txt、笔记导出、MCP）

### 2) 可保留但必须克制（支持）
- 提交统计、目标环、热力图、状态栏
- Claude 记忆导入
- i18n、主题、仪式感强但服务核心的小功能

### 3) 默认不扩展（实验）
- 复杂块编辑器高级块（Callout/Tabs/Mermaid 等仅保留，不新增）
- 插件/SPI 平台能力
- API 文档化、契约扩展、平台化入口

### 4) 停止投入或移出主叙事
- PWA（已移出桌面主构建，见 ADR-0008）/ SEO / Web-only 追加能力
- 易造成误解为“在线平台、团队 Dashboard、HTTP Server”的表达

## 对外叙事重构

### 统一对外口径（权威短文案）

> 本节为 PR1「定位治理」落地的统一对外口径。所有 Release / 推广 / 首页开篇 / Issue 模板以此为准，其它文案（含下方「完整」版与 README 开篇）仅作为它的展开，不得与之冲突。

**中文（三句定位）**

> Cinder 是本地优先的跨 agent 项目记忆层：开会话一次注入上下文（`repotory_context`）、收会话结构化交接（`repotory_handoff`），让任何 agent 都从上一次结束的地方继续。

**English**

> Cinder is the local-first memory layer for AI coding agents: one call injects full project context at session start (`repotory_context`), one call records a structured handoff at session end (`repotory_handoff`) — any agent picks up where the last one stopped.

**Release note**

> Positioning release: Cinder is reframed as the local-first, cross-agent project memory layer; the session loop (`repotory_scan` → `repotory_context` → `repotory_handoff`) becomes the headline capability. Dashboard/statistics stay supporting capabilities.

### 推荐（完整）
> Cinder 帮你把本地 Git 项目从“散落在终端和记忆里”变成“可检索、可复用的记忆层”。它能快速发现你关心的项目，理解每个项目当前状态，并把笔记、依赖、技术栈和活跃信息沉淀下来；随后在会话开始时一次注入给 AI（`repotory_context`），在会话结束时结构化收回（`repotory_handoff`），跨 agent 复用。

### 暂停（容易误导）
- “代码提交仪表盘 / 数据大盘”
- “本地开发数据平台 / 可观察平台”
- “插件中心 / API 平台”

## 为什么当前会“界面点不动 / 容易报错”

这次你遇到的不是单点按钮 bug，而是三个问题叠加：

1. **后端契约和前端调用规则不对齐**  
   桌面端走 Wails 绑定，浏览器开发态走 API；前后端假设没统一，导致错误链断裂。

2. **错误链和用户感知没闭环**  
   请求失败后，前端缺少统一异常、重试与恢复路径，用户体验上像“卡死”。

3. **产品叙事偏宽，优先级不聚焦**  
   一旦定位边沿能力偏多，测试和设计边界都会被拉长，回归成本会变高。

## 建议的产品评估标准（以后新增任何功能前）

新增功能必须至少命中以下一个环节：
1. 发现本地项目
2. 理解本地项目
3. 沉淀项目知识
4. 检索项目知识
5. 交给 AI 使用

如果一条都不命中，建议默认不进。

## 建议的发布与沟通次序

1. **先把“定位句 + 核心闭环”公开化**
2. **再修前端错误一致性与用户恢复路径**
3. **最后对仪表盘、插件、AI 能力做分级披露**  
   - 核心能力放首位  
   - 仪表盘作为“支持能力”  
   - 插件平台放在“当前不扩展/仅保留”

## 结论

如果你愿意，我下一步可以把这份简报直接落到：
- `README.md`
- `docs/index.md`
- `docs/getting-started.md`
- 首页与导航文案
- GitHub Release / Issue 模板
- 对外短文案（中文+英文）

这样产品认知和工程实现就不再各说各话了。
