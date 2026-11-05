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

## 日志文件位置

| 平台 | 日志路径 |
|------|---------|
| macOS | `~/Library/Logs/repotory.log` |
| Linux | `$XDG_STATE_HOME/repotory/repotory.log`（默认 `~/.local/state/repotory/repotory.log`） |
| Windows | `%APPDATA%\repotory\logs\repotory.log` |

日志同时记录 PATH 环境与插件运行时状态，报告问题时请附带。

## 数据安全须知

- Cinder 是**本地优先**桌面应用：不监听网络端口、不上传任何数据；统计通过本机 `git` CLI 读取
- 数据库与配置存储在用户应用数据目录（位置见[快速开始](getting-started.md)），卸载不会自动删除；备份、迁移与彻底清除见[数据与备份](data-management.md)
- 桌面 WebView 响应带 CSP 等安全头（`default-src 'self'` 等）

## 报告问题

如发现 Bug 或有功能建议，请在 [GitHub Issues](https://github.com/sky-jiangcheng/repotory/issues) 提交（含版本号、平台、日志片段）。安全漏洞请走[私密报告渠道](https://github.com/sky-jiangcheng/repotory/security/advisories/new)。
