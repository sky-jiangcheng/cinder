# ADR-0067：install all 一键接线 CLI——挂在 cinder-capture 上的 init / uninstall

- **状态**：Accepted（2026-11-23）
- **日期**：2026-11-23
- **关联**：[ADR-0065](0065-hindsight-absorb-not-replace.md)（对标 Hindsight 的吸收路线，P0 第二项）、[ADR-0066](0066-session-start-brief-injection.md)（SessionStart 简报，闭环的另一半）、[ADR-0009](0009-ide-presence.md)（MCP 薄客户端接线）、[ADR-0010](0010-session-auto-capture.md)（捕捉默认关闭）、[ADR-0043](0043-handoff-instructions-in-agent-instruction-files.md)（受管指令块）、[ADR-0054](0054-rolling-checkpoint-handoff.md)（SessionEnd 两级兜底）

## 背景

ADR-0065 缺口清单 P0 第二项：Hindsight 的 `install all` 一条命令接齐所有 harness，且 `uninstall all` 精确移除。Cinder 的接线能力早已存在（`internal/agentinit` 四家客户端 + 双 hook + 受管指令块，Go/mjs 双写契约钉死），但入口只有两个：桌面设置页的按钮，和需要 node 的 `scripts/repotory-init/index.mjs`。终端用户没有一条「装了二进制就能跑」的命令；卸载则完全没有——装过的东西只能手工对着文档删，删多删少全凭运气。

## 决策

1. **宿主：`cinder-capture init` / `cinder-capture uninstall` 子命令。** capture 是每个 agent 侧安装都已携带的伴侣二进制（install.sh / brew / scoop 均分发，含 `repotory-capture` 兼容符号链接），挂在它上面**零新增发布资产**。被否的三个方案：新 `cinder-init` 二进制（单人维护多一条发布流水线，违背仓库瘦身立场）；`cinder-mcp init`（stdio server 的 main 要重构，且服务器身份不该兼做安装器）；只留 mjs（node 依赖破坏「单文件零依赖」承诺）。
2. **`init --all` = 全部客户端 + 双 hook + 指令块；裸 `init` = 只注册客户端**（与 mjs 默认一致）。子命令在 capture 的 flag 集之前分发，**全程不开数据库**：安装 hook 不等于启用转录读取，`claude_session_capture` 保持默认关闭（ADR-0010 决策 1/2 不动）。Go flag 包同时接受 `-all` 与 `--all`，两种写法与 mjs 兼容。
3. **精确卸载语义（`agentinit.Unregister`，mjs `--uninstall` 逐条镜像）**：只移除我们写入的东西——各客户端配置里的 `repotory` 条目（其他 server 不动）、`settings.json` 里指向两个脚本的 hook 条目（其他 hook 不动；清空的 event 键与空 hooks 映射一并剪掉，文件回到安装前形状）、hook 脚本（**仅当字节仍等于安装常量**；被用户改过的脚本留在原地并警告——那是用户的编辑，不是我们的待删物）、受管指令块（标记外内容逐字节保留；只含块的文件删除，回到「没有文件」的安装前状态）。每次覆写先备份（`.repotory-init.bak`）；文件/条目不存在 = unchanged，二次卸载是无声 no-op。
4. **契约双写扩到卸载后状态。** 新 fixture `testdata/contract-uninstall.json`：JSON 按语义比对，脚本/指令文件按精确文本，`null` 表示「必须不存在」。Go `TestContractFixture` 与 `check-agent-init-contract.mjs` 都先比安装产物、再跑卸载比残留——「装得一样」和「拆得一样」是同一个承诺的两半。
5. **mjs 保留自己的 `--dry-run`**（Go CLI 无此 flag：agentinit 没有 dry-run 模式，不为对称发明一个）；mjs 卸载路径在 dry-run 下只打印 would-remove，不删文件。
6. **不动 MCP 工具面、不动 schema、不动桌面按钮。** CoreVersion 不变；桌面设置页走同一个 `agentinit` 包，行为自动一致。

## 代价（接受）

- 终端有两条等价入口（`cinder-capture init` 与 `node index.mjs`）要记——契约 fixture 保证它们产物一致，但文档必须同时教两条。
- Windsurf 配置在全局 home 目录，`uninstall` 默认会触碰它（与安装对称）；`--client` 可收窄范围。
- 备份文件不自动清理：卸载后 `.repotory-init.bak` 留在原地，这是「用户文件永不丢失」的代价。
- Go CLI 无交互确认（无 prompt 可跳过），也就不提供 mjs 的 `--yes`；误装可用 `uninstall` 精确撤销，可逆性由卸载保证而非确认弹窗。

## 翻转触发

- 品牌合并出统一的 `cinder` 多功能 CLI → 子命令迁过去，capture 保留兼容别名。
- 客户端集合超出四家契约（如 Hindsight 式 22 harness）→ 重估「一家一个配置文件」的接线模型，而不是继续往 fixture 里堆。

## 落地记录（2026-11-23）

- 代码：`internal/agentinit/unregister.go`（+6 测试：只删我们的、幂等、改过的脚本留置、外来 hook 保留、从未安装目录 no-op、坏 JSON 报告不覆写）；`cmd/repotory-capture/init.go` 子命令（+4 测试：init --all 不碰 HOME/数据库、uninstall 往返幂等、坏客户端 exit 2、缺二进制 exit 1 带指引）；mjs `--uninstall` 双写 + VSIX 同步；`contract-uninstall.json` fixture + 两侧契约检查扩展。
- 验证：`go test ./internal/agentinit ./cmd/repotory-capture` 全绿；`check-agent-init-contract.mjs` 安装态 + 卸载态双比对通过；真实二进制临时目录冒烟：`init -all` 六文件齐、`uninstall` 精确移除带备份、二次卸载全 unchanged。
- 文档：`getting-started.md`、`features/ai-integration.md` 双语同步。