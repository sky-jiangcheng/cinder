---
title: 故障排查
order: 10
---

# 故障排查

## 常见问题

### 仪表盘显示「暂未发现仓库」

**原因**：扫描根目录未配置或指定目录内无 Git 仓库。

**解决**：进入 **设置 → 扫描目录**，添加包含 Git 仓库的目录，点击 **重新扫描**。

> 首次扫描仅登记仓库名称，不扫描历史提交数据。

---

### 仓库卡片只显示名称，无统计数据

**原因**：仓库未收藏，或收藏后未回填历史数据。

**解决**：
1. 点击卡片上的星标收藏仓库
2. 点击 **刷新历史** 按钮，等待 git log 回填完成

---

### 统计数据显示为零

**原因**：历史记录尚未回填，或所选日期无提交。

**解决**：在仓库卡片上点击 **刷新历史**，系统会扫描该仓库近 365 天的 git log。确认 git 已安装且在 PATH 中（从 Finder/Dock 启动时应用会自动补全常见 PATH）。

---

### 如何快速找到某个仓库？

仪表盘搜索框联合搜索仓库 / 笔记 / 待办；或使用 `⌘/Ctrl+K` 命令面板（见[命令面板](features/command-palette.md)）。搜索结果中可直接点击星标切换收藏状态。

---

### 如何调整项目分组？

多个仓库被错误归为一组（或相反）时，进入项目详情页，点击头部操作按钮：

- **向下拆分**：多仓库项目拆分为每仓库一个项目（原项目保留首个仓库的笔记与待办）
- **向上合并**：合并同父目录的兄弟项目（仓库、笔记、待办随迁）

两个操作均为单事务，失败时不会留下半完成状态。

---

### 知识库搜索搜不到两字中文词？

FTS5 trigram 索引最短匹配 3 字符；更短的查询自动降级为 LIKE 全表扫描（仍可命中，只是无相关性排序）。无需处理。

---

### 知识源没有导入？

1. 确认该源的触发方式：只有 `claude` 随 `auto_import` 在启动时自动跑（默认关），其余五源需在 **设置 → 插件** 手动点「立即导入」
2. `openclaw` / `hermes` 需先配置目标项目（`openclaw_project` / `hermes_project`），未配置时静默跳过、只体现在 `skipped` 计数
3. 详见[知识源导入](plugins/overview.md)

> 进程内脚本插件（yaegi）已于 C11 移除：`plugins/` 目录不再被加载，如目录里还有旧脚本插件可自行删除。

---

### 改坏了 / 误删了，怎么回到之前？

1. **先看内置快照**。数据目录下的 `backups/` 里有两组文件：每日一份 `repotory-<YYYY-MM-DD>.db`（保留 14 份），以及日内滚动快照 `repotory-<YYYYMMDD-HHMM>.db`（距上一份满 30 分钟且期间确有写入才写，保留 6 份）。前者是「今天首次打开前」，后者是「最近几小时」。
2. **恢复不会自动替换正在用的库**——这是刻意的：
   ```bash
   cinder restore --from <快照文件> --to <新路径>
   ```
   它会校验源快照、原子写目标、目标已存在就失败，并且**拒绝**写到 live 库路径。恢复完退出 Cinder，自己把新文件挪到数据库位置即可。桌面版的可执行文件在 `/Applications/Cinder.app/Contents/MacOS/Cinder`。
3. **只错了一条笔记，别整库恢复**：笔记编辑器里每一版都留了「改前态」，先试版本历史，代价小得多。

---

## 日志文件位置

| 平台 | 日志目录 |
|------|---------|
| macOS | `~/Library/Logs/` |
| Linux | `$XDG_STATE_HOME/repotory/`（默认 `~/.local/state/repotory/`） |
| Windows | `%APPDATA%\repotory\logs\` |

**三个进程各写一份**（ADR-0055 D4：`slog` JSON 行、按 5MiB 轮转、保留一份 `.1` 旧文件）：

| 进程 | 文件 |
|------|------|
| Cinder Desktop（Wails） | `repotory.log` |
| `cinder-mcp`（IDE 里的 stdio 服务） | `repotory-mcp.log` |
| `cinder-server`（无头 HTTP） | `repotory-server.log` |

MCP 进程的 **stdout 只走 JSON-RPC 协议**，日志一律落文件或 stderr；客户端没捕获 stderr 时以 `repotory-mcp.log` 为准。日志含 `time` / `level` / `msg` 与 `request_id`；HTTP 请求回写 `X-Request-ID`，同一个 id 可串起「工具调用 → service → 外部 LLM / 向量库」。日志同时记录 PATH 环境与插件运行时状态，报告问题时请附带。

**快速自检**：无头模式访问 `http://127.0.0.1:18765/health`（返回 `status` / `version` / `core_version` 与 `db_size_bytes` / `compile_jobs_failed_24h` / `last_backup_at`）；MCP 侧用 `repotory_integrity` 跑 6 项数据一致性检查、`repotory_agent_score` 看安装就绪度。工具失败现在一律 `IsError=true`，`structuredContent` 里带 `code` 与 `request_id`，报问题时请一并附上。

## 数据安全须知

- Cinder **本地优先**：桌面 App 本身不监听网络端口；无头模式 `cinder-server` 只在 `127.0.0.1:18765` 提供本地 HTTP API，不对局域网暴露。数据默认不出本机——只有显式配置远端备份（S3 等）或外部 AI 端点才会外发；统计通过本机 `git` CLI 读取
- 数据库与配置存储在用户应用数据目录（位置见[快速开始](getting-started.md)），卸载不会自动删除；备份、迁移与彻底清除见[数据与备份](data-management.md)
- 桌面 WebView 响应带 CSP 等安全头（`default-src 'self'` 等）

## 报告问题

如发现 Bug 或有功能建议，请在 [GitHub Issues](https://github.com/sky-jiangcheng/cinder/issues) 提交（含版本号、平台、日志片段）。安全漏洞请走[私密报告渠道](https://github.com/sky-jiangcheng/cinder/security/advisories/new)。
