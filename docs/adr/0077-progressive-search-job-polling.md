# ADR-0077：渐进式搜索——本地命中即刻返回，语义召回走异步 job 轮询

- **状态**：Accepted（2026-11-24，用户拍板「搜索改后端加异步 job + 轮询」）
- **日期**：2026-11-24
- **关联**：ADR-0012（语义检索开闸）、ADR-0016（异步 job 先例）、ADR-0070（时间轴臂）、ADR-0071（融合调参）、ADR-0072（召回来源徽章）

## 背景

搜索在用户侧是「输入后 2–8 秒没有任何反应」。实测定位到两段成本相差三个数量级：

| 阶段 | 实现 | 活库实测（162 条笔记 / 0.27 MB 正文） |
|------|------|----------------------------------------|
| 本地词法（FTS5 + LIKE 兜底 + todo） | `db.SearchAll` | **3 ms** |
| 查询嵌入（远程端点 `qwen3-embedding-8b`） | `fuseSemantic` → `RemoteEmbedder` | **4–12 s** |
| 时间轴臂 | `fuseTemporal`（纯本地解析） | < 1 ms |

问题不在「慢」，在**把两段合成一个同步响应**：`SearchAll` 必须等远程嵌入回来才能返回第一行结果。前端 300 ms 防抖之后是数秒空白，用户看到的是「搜索框坏了」，重新输入只会再排一次同样的等待。远程端点是网络依赖，不是能靠索引优化消掉的开销——这条路的上限由端点的 RTT 决定。

## 决策

把一次搜索拆成**两阶段 + 一个句柄**，只有 UI 走这条路，agent 与 CLI 保持原样。

1. **阶段一（同步，毫秒级）**：`searchLexical` 跑 FTS5/LIKE，打 recall 词法通道与 source_type，直接作为 job 的第一批结果返回。
2. **阶段二（异步，秒级）**：`fuseHits` 在后台跑时间轴 + 语义两臂，完成后写回同一个 job。
3. **句柄**：`StartSearchJob(query)` 立即返回 `{id, state, hits}`——`hits` 一定有值，`state` 为 `refining` 时前端每 400 ms 调 `GetSearchJob(id)`；`state=done` 即终值。
4. **语义未开时零成本**：`semanticArmed()` 为假（默认关，或没配嵌入端点）→ 阶段二没有远程活可干，job 直接以 `done` 返回，前端**从不轮询**。所谓异步机制在默认安装里一次都不触发。
5. **job 存内存，不落库**：子分钟级、重启即失效（轮询方已不在）、而且把用户敲过的每个查询写进知识库文件，是在一个会长期留存的载体里泄漏搜索历史。TTL 2 分钟 / 上限 32 条 / 同 query 复用进行中的 job（防抖抖动不会变成一串远程嵌入账单）。
6. **`SearchAll` 保留**：agent、CLI、MCP 工具、首页「问答」的证据链都要一次拿到完整答案——半份证据不是半个答案。同步语义由 `SearchAll = searchLexical + fuseHits` 表达，两条路共用同一份融合定义。

新增两个绑定均标 `SurfaceDesktopOnly`：agent 调 `repotory_notes_search` 一次拿到融合结果，没有轮询要照看。**MCP 工具面与 CoreVersion 不变**。

## 代价

- 多两个绑定 + 一个内存注册表；`surface.go` 与写契约（`StartSearchJob` 名字里有 "Start" 但是纯读，已登记豁免）。
- 前端多一层轮询状态机：seq guard 之外还要能**取消**（换 query 时停掉旧轮询；后端 job 自己跑完，结果无人读）。
- 结果列表会**变一次**：语义命中插入后排名变化。前端用「正在补充语义结果…」明示这一层，而不是让列表无声地跳动。
- 语义臂失败（端点不可达）时 job 仍以 `done` 收尾，只是没有 semantic 命中——这与 ADR-0012「语义是可选增益」的降级口径一致，不额外弹错误。

## 翻转触发

- 端点换成**本地**嵌入（同机 RTT < 50 ms）→ 阶段二不再需要异步，退回单次同步调用更简单。
- 前端需要「流式」而不是「两批」（例如逐条追加语义命中）→ 改用 Wails event 推送，轮询退场。
- job 需要跨进程/跨重启（多端同时搜索、或桌面端与 server 端共享）→ 内存注册表不够，那时再考虑落库 + 单写者路由。

## 落地记录（2026-11-24）

- 后端：`internal/service/search.go` 拆成 `searchLexical` / `fuseHits`（`SearchAll` 保持签名与语义）；新增 `internal/service/search_job.go`（`SearchJob`、`searchJobStore`、`StartSearchJob`、`GetSearchJob`、`semanticArmed`），后台 goroutine 走 `bgGo`（panic 兜底 + Shutdown 等待）并以 60 s 超时兜住挂死的嵌入端点；`internal/app/bindings.go` 两个绑定，`internal/app/surface.go` 两条 `SurfaceDesktopOnly`，`write_contract_test.go` 登记「名字像写、实际纯读」。
- 测试：`search_job_test.go` 6 例——语义关闭即刻 done（且与 `SearchAll` 逐条一致）、空 query、门控嵌入端点下 `refining → done` 且语义命中补入、同 query 复用同一 job、容量淘汰、淘汰后 finish 丢弃；`-race` 绿。
- 前端：`api/endpoints.ts` 增 `startSearchJob` / `getSearchJob` / `runProgressiveSearch`（含 400 ms 轮询、150 次上限、cancel）；`useKnowledgePage` 与首页搜模式接 job（先渲染词法命中，`refining` 时显示提示，`done` 后换最终列表）；`Knowledge.tsx` 提示落在搜索结果标题行，旧「整列表 0.45 透明度」陈旧态删除（列表现在本身就是答案，压暗反而像坏了）。
- 顺带修掉两处同源问题：扫描完成只写进 `visually-hidden` 的 live region（屏幕阅读器能看到、人看不到）→ 仪表盘补 toast，失败走错误横幅；扫描进度直接显示后端英文 `ScanStatus.message`（中文界面出现 "Scanning 12/12…"）→ 前端用 `progress/total` 自行渲染 `扫描中 12/12…`，仪表盘与设置页同批。
- 真机验证（隔离库副本 + headless server）：`StartSearchJob` 往返 **4.5 ms**（原 6–8 s）；浏览器实测首批卡片 **476 ms** 出现并同时显示「正在补充语义结果…」，**10.7 s** 后语义命中补入（20 → 44 条，30 条带 semantic 通道）；语义端点不可达时 job 以 done 收尾、只剩词法结果，界面无报错。扫描点确认 → 按钮显示「扫描中 12/12…」→ 完成后右上角 toast「扫描完成」。
