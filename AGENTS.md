# AGENTS — 开发入口（面向 AI agent / 新成员）

Repotory（原 RepoNest）：本地优先的代码项目知识库。Go + Wails 桌面/无头双端，React + TypeScript + Vite 前端，单 SQLite（FTS5 + sqlite-vec，零 CGO）。

## 按需查文档（按此顺序，避免全文探索）

1. **[TODO.md](TODO.md)** — 未完成事项 + 现状速查（品牌/版本/schema/近期大项）
2. **[CONTRIBUTING.md](CONTRIBUTING.md)** — 架构约定、命名分层、范围冻结、提交规范
3. **[docs/adr/index.md](docs/adr/index.md)** — 架构决策记录（每份含背景/决策/代价，编号连续递增）
4. **[CHANGELOG.md](CHANGELOG.md)** — 近期版本明细；v1.15.1 及更早在 [outputs/archive/CHANGELOG-v1.0-v1.15.md](outputs/archive/CHANGELOG-v1.0-v1.15.md)
5. **[outputs/](outputs/)** — 过程报告归档（P35 迁移清单、知识图谱实施计划、评估稿）

## 常用命令

```bash
# 后端测试（发布门）
go test ./...

# 前端测试 / 构建门 / lint（web/ 目录下）
npm test && npm run build && npm run lint

# 本地开发（无头 API 18765 + Vite 5173，双进程一键起）
bash scripts/dev.sh
```

## 高频陷阱

- **双语手册必须同批改**：`docs/**` 与 `docs/en/**` 是镜像，只改一边 = 双语漂移（历史上英文站曾讲出不存在的产品架构）。已有 CI 硬门 `docs-mirror`（`scripts/check-docs-mirror.mjs`）拦截——改中文页记得补英文 twin；有意只出中文的研究文档（`competitive-positioning` / `positioning-brief` / `product-review/*` / `product/*`）走该脚本的 `ZH_ONLY` 白名单
- **`docs/**/*.html` 是构建产物，不手改**；内容源是 `docs/**/*.md`
- **界面文案中英必须同批改**：`web/src/locales/{zh-CN,en}/common.json` 是镜像，只改一边 = 英文界面显示 key 本身，或被调用点的中文 `defaultValue` 悄悄接管。已有 CI 硬门 `i18n-keys`（`scripts/check-i18n-keys.mjs`）拦截——新增 `t()` key 时两份都要补，英文文案里也不该留中文
- **schema 变更**：`ExpectedSchemaVersion`（`internal/integrity/checks.go`）与迁移表（`internal/db/migrate.go`）+ 防漂移测试是守卫，版本号必须同步推进且可逆测试跟上
- **MCP 工具面变了要涨契约版本**：`internal/version/core.go` 的 `CoreVersion` 是**对外契约版本**（不是发布版本 `Version`）——`major` = 工具被删/改名或字段含义变，`minor` = 新增工具/新可选字段，`patch` = 契约没动。契约版本**钉在工具清单旁边**（`cmd/mcp/main_test.go` 的 `TestToolsRegistered` 里 `wantCoreVersion`），两份拷贝由 `scripts/check-version-drift.mjs` 第⑧道检查兜住——**加/删工具却忘了涨版本号，那道门会红**，别去改检查脚本绕开它。详见 [ADR-0048](docs/adr/0048-core-contract-version.md)
- **CSS 语义色顺序不变量**由 `web/src/styles/semanticInk.test.ts` 钉住；改全局 CSS 顺序前先读它
- **冻结标识**（`repotory` 二进制名/数据目录/MCP 前缀 `repotory_*`）绑定用户数据迁移史，不「顺手统一」——见 CONTRIBUTING「范围冻结」
- **图标三域**：`build/icon.svg` 是唯一母图；`generate-icons.mjs` 派生「品牌域方边（favicon/商店/JetBrains 工具窗内的品牌标记 PNG）」、「OS 应用域 squircle（appicon.png/阶梯/ico，macOS 不自掩码）」与「字形域单色透明底（VS Code 活动栏 24px 框+字母；JetBrains 工具窗侧栏 16px 仅字母，带 `_dark` 深色变体——该槽位由宿主光栅化不遮罩，奶油卡会读作发亮方块）」三套形状，改母图后必须跑生成器，CI 同步门兜底（先 `--check --tracked-only` 再生成，别反过来）——见 CONTRIBUTING「品牌图标三域规则」
