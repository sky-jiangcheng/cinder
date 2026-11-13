# outputs/screenshots/ — 过程截图归档

> **去留决定（TODO 13.4③，2026-11-13）**：**保留在版本库内**（B 档，与 `outputs/` 其它证据目录同一纪律——它们是 CHANGELOG / TODO / ADR 散文引用的现场证据，删了不产生死链但等于把证据抽走）。LFS 与外置 evaluated 后否决：7 个集合共约 12.6MB、月增长 <10MB，LFS 的额外工具链成本（clone 需 LFS 支持、CI 需缓存迁移）换不回这个量级的收益。

## 现状清单（2026-11-13 盘点）

| 集合 | 数量 | 体积 | 引用处 |
|---|---|---|---|
| [2026-10-21-fix-verification/](2026-10-21-fix-verification/) | 13 | 1.4MB | 无正文链接——当日工作记录（本机 `.workbuddy`）点名入库的核验对照；纯过程证据 |
| [2026-10-22-adr0022-closure/](2026-10-22-adr0022-closure/) | 5 | 656KB | [ADR-0022](../../docs/adr/0022-agent-memory-discovery-sync.md)「GUI 真机冒烟已做」的现场 |
| [2026-10-29-gui-smoke/](2026-10-29-gui-smoke/) | 20 | 7.9MB | TODO 13.4 前身 D25/ADR-0034 的真机核验；CHANGELOG `[2.7.0]` |
| [2026-11-02-ide-chat-console/](2026-11-02-ide-chat-console/) | 2 | 120KB | CHANGELOG（ADR-0036 面板核验 + preview 页） |
| [2026-11-03-adr0042-page-shell/](2026-11-03-adr0042-page-shell/) | 6 | 1.0MB | [ADR-0042](../../docs/adr/0042-page-shell-pinned-header-and-scrolling-body.md)「截图归档」节逐张点名 |
| [2026-11-03-knowledge-select-arrow/](2026-11-03-knowledge-select-arrow/) | 9 | 1.0MB | CHANGELOG（select 箭头平铺缺陷，`selectArrow.test.ts` 防复发门的实测对照） |
| [2026-11-04-ide-bot-provider/](2026-11-04-ide-bot-provider/) | 6 | 460KB | 无正文链接——当日工作记录点名入库的 provider 对话验证截图 |

## 纪律

1. **新集合必须有归档理由**：正文（CHANGELOG / TODO / ADR）点名目录，或在这里的表格加一行说明它是哪条记录的现场证据。无引用、无说明的截图不进版本库。
2. **PNG 是最终形态**，不 re-encode、不裁剪——像素是证据的一部分。
3. **单集合 >20MB 才需要重议**（届时先试 `pngquant` 有损压缩 + `docs-mirror` 式引用完整性门，而不是直接上 LFS）。

## 仓库卫生注记（同批盘点）

- `.git/objects/` 里 123 个 `tmp_obj_*`（约 680KB）是进程中断留下的临时对象，`git prune` 可清；pack 本体 21.5MiB，仓库的 clone 成本大头不在截图。
- `outputs/` 其余目录（e2e 巡检、process-logs、迁移清单）沿用 2026-10-29 的 B 档定性，本文件不重复登记。
