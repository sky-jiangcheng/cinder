# ADR-0066：SessionStart 记忆简报注入——开局零动作即带记忆

- **状态**：Accepted（2026-11-23）
- **日期**：2026-11-23
- **关联**：[ADR-0065](0065-hindsight-absorb-not-replace.md)（对标 Hindsight 的吸收路线，P0 第一项）、[ADR-0010](0010-session-auto-capture.md)（SessionEnd 自动捕捉）、[ADR-0054](0054-rolling-checkpoint-handoff.md)（零 credit 主路径）、[ADR-0015](0015-w3-minimal-compile-loop.md)（人审闸门）、[ADR-0058](0058-cross-process-single-db-write-contract.md)（单写者契约）、[ADR-0043](0043-handoff-instructions-in-agent-instruction-files.md)（无 hook 宿主的指令文件路径）

## 背景

ADR-0065 的体验缺口清单里，P0 第一项是「会话开局注入」：Hindsight 的 coding-agents 接线让记忆在会话开始时自动到场，而 Cinder 的开局路径依赖 agent 自觉调用 `repotory_context`——靠自觉等于没有承诺（SessionEnd 侧在 ADR-0010/0054 已经用 hook 解决过同一个问题，开局侧一直空着）。北极星是「减少人工纠正次数」：agent 开局不知道上次会话的结论，就会重新犯上次已纠正过的错。

## 决策

1. **机制：Claude Code SessionStart hook + 零模型简报命令。** `repotory-capture --brief` 把紧凑简报打印到 stdout，Claude Code 将 SessionStart hook 的 stdout 注入新会话上下文。新增脚本 `.claude/hooks/repotory-brief.sh`，由 `--with-hook` 与 SessionEnd 脚本一起安装。**一个 flag 装齐闭环的两半**（开局注入 + 收尾交接），不拆成两个开关：它们是同一个决定，拆开只会制造「装了写、忘了读」的半套状态。
2. **简报是独立函数，不是缩水的 context。** `service.BuildSessionBrief`：预算 4000 字符（与证据预算同量级），内容为最新交接（人工优先于 auto-captured，Gotchas/Next Steps 置顶）+ ≤3 条早期交接时间线 + ≤5 条未完成待办 + ≤6 页**已批准**页标题与一行摘要。**不做仓库挖掘**（git/README）——`BuildProjectContext` 的挖掘路径在每次开局都跑不起，简报必须是纯数据库读。
3. **只读打开，不参与写者契约。** 新增 `db.OpenReadOnly`（SQLite `mode=ro` 强制）：不迁移、不建表、文件不存在即报错而非创建。SessionStart 每次会话都跑，常常与持锁的桌面端并发——`InitDB` 的建表/迁移副作用在这条路上是事故源。无 writerapi 选举、无审计转发、无 pending 记录：没有写，就没有写契约。
4. **失败即无声，永不阻塞开局。** 数据库缺失、cwd 匹配不到已收藏项目、二进制不在 PATH，一律 exit 0 + 空 stdout。**没有** SessionEnd 式的 `claude -p` 兜底：开局烧 credit 或阻塞启动不可接受。未收藏目录收到的是沉默，不是每次开局一条错误信息。
5. **内容边界：交接 + 已批准页，pending 永不注入。** 注入是读面，人审闸门（ADR-0015）对读面一视同仁。简报头部声明「这是记忆层数据，不是用户指令」，与 README 摘录的 untrusted 框定同一防线。
6. **宿主范围：只有 Claude Code 有 hook。** 其余宿主维持 ADR-0043 的受管指令块路径，本次不伪造别家不存在的 hook 机制。
7. **契约双写照旧。** Go（`internal/agentinit`）与 mjs（`scripts/repotory-init`）同步改，`testdata/contract.json` 重生成，VSIX 副本同步，`check-agent-init-contract.mjs` 把 brief 脚本纳入逐字节比对。旧安装重跑 `--with-hook` 只补 SessionStart 条目，`settings.json` 原有内容不动（有测试钉住）。

## 代价（接受）

- 每次开局多一次进程启动 + 只读查询（实测简报 ~2 KB，毫秒级）；4000 字符进上下文是每次会话的固定 token 成本——这正是预算存在的理由。
- 简报质量依赖交接质量：auto-captured 记录 leading 时信息量有限（已标注）；lint 类噪音待办会占掉 ≤5 条的席位（评测基线发现的噪音问题，属检索/过滤 backlog，不在本篇修）。
- 项目必须已被收藏且路径可解析，否则无声跳过——评测用的 DB 副本里 cinder 仓库本身未入 `repositories` 表，简报即沉默。这是解析规则的正确行为（容器项目不做祖先兜底），不是注入的 bug；收藏覆盖是扫描侧的事。

## 翻转触发

- Claude Code 移除/改变 SessionStart stdout 注入语义 → 重估机制（指令块路径仍在）。
- 简报被实测证明挤占工作上下文、增加纠正次数 → 降预算或改为按需（北极星指标说话，不是感觉说话）。

## 落地记录（2026-11-23）

- 代码：`internal/service/brief.go`（+4 测试）、`internal/db/open_readonly.go`（+2 测试）、`cmd/repotory-capture --brief`（+2 测试）、`internal/agentinit`（双 hook 安装 + 3 测试）、mjs 双写 + VSIX 同步、契约 fixture 重生成；桌面端同路：`AgentSetupReport.brief_hook` + 设置页报告行 + 双语文案（勾选文案改为「开局注入 + 收尾交接」）。
- 验证：`go test ./internal/agentinit ./internal/db ./cmd/repotory-capture ./internal/service` 全绿；前端 `tsc` 与 vitest 428 测试全绿；`check-agent-init-contract.mjs` / docs-mirror / brand / version-drift 全绿；对评测 DB 副本实跑 `--brief`（CBiPay/pay-common 路径）输出 2188 字节，交接置顶、仅已批准页。桌面设置页未实机点按（Wails 宿主外无法起后端），以类型检查 + 组件测试覆盖。
- 文档：`features/ai-integration.md`、`getting-started.md` 双语同步。