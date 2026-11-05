#!/bin/bash
# M7 lane 2 晋升收尾脚本（ADR-0018 条件 10：密度 + delta 双门）。
#
# 背景：P0 图密度成果（26 篇 ADR 知识笔记 + LLM 编译出的 18 个实质页 + 37 条边）
# 产生在 agent sandbox 的**工作副本**上（sandbox 对 live 库内核级只读）。
# 本脚本是「最后一公里」，在普通终端执行：
#   1) 迁移  副本 → live 库（cmd/promote-m7，自动备份、幂等、孤立边校验）
#   2) 密度  复核条件 10①（approved>=15 且 edges>=20）
#   3) 门    abeval -mode evidence（条件 10②：delta>+0.05 且 k=2..20 全区间为正）
#   4) 诊断  abevaldiag 逐案（条件 10③：增益形如「命中种子页→沿白名单边带出邻接页」）
#
# 前置（务必）：
#   - 先完全退出 Cinder Desktop（WAL 写竞争）
#   - 本地 embedding 端点可用（默认 LM Studio http://localhost:1234/v1）
#   - 工作副本在 .agnes/work/p0run/live-copy/repotory.db（agent 会话产物）
#
# 用法：
#   bash scripts/promote-m7-lane2.sh                 # 全流程
#   bash scripts/promote-m7-lane2.sh migrate-only     # 只迁移 + 密度复核
set -euo pipefail

cd "$(dirname "$0")/.."

# TMPDIR 指到可写位置（部分环境 os.TempDir() 不可写，CopyForTooling 依赖它）
export TMPDIR="${TMPDIR:-$(mktemp -d)}"
GOFLAGS="${GOFLAGS:--mod=mod}" GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" GOSUMDB="${GOSUMDB:-off}" export GOFLAGS GOPROXY GOSUMDB

FROM="${1:-}"; shift 2>/dev/null || true
MODE="${FROM:-all}"   # all | migrate-only
FROM="${FROM:-.agnes/work/p0run/live-copy/repotory.db}"
LIVE="${HOME}/Library/Application Support/repotory/repotory.db"
CASES=".annotation-prep/evidence-cases-enriched.jsonl"

echo "== 前置检查 =="
[ -f "$FROM" ] || { echo "✗ 工作副本不存在: $FROM（agent 会话产物，路径可传第一个参数覆盖）"; exit 1; }
if pgrep -x repotory >/dev/null 2>&1 || pgrep -f "Cinder.app" >/dev/null 2>&1; then
  echo "✗ 检测到 Cinder Desktop 在运行——请先完全退出再跑（WAL 写竞争会污染迁移）"; exit 1
fi
command -v go >/dev/null || { echo "✗ 找不到 go"; exit 1; }
pgrep -f "lmstudio|LM Studio" >/dev/null 2>&1 || echo "（提示）LM Studio 未见运行——门②③需要本地 embedding 端点"

echo "== ① 迁移（自动备份 live 库） =="
go build -buildvcs=false -o "$TMPDIR/promote-m7" ./cmd/promote-m7
"$TMPDIR/promote-m7" -from "$FROM" -to "$LIVE" -gate
if [ "$MODE" = "migrate-only" ]; then
  echo "== migrate-only：到此结束（门②③请自行择时跑） =="
  exit 0
fi

echo ""
echo "== ② 密度复核（条件 10①） =="
# live 库是 WAL 模式：sqlite3 -readonly 无法创建 -shm，会报
# "unable to open database file (14)"。先落临时快照（含 -wal/-shm 若在），
# 对快照做只读复核，不触碰 live。
SNAP="$TMPDIR/density-snapshot.db"
cp "$LIVE" "$SNAP"
if [ -f "$LIVE-wal" ]; then cp "$LIVE-wal" "$SNAP-wal"; fi
if [ -f "$LIVE-shm" ]; then cp "$LIVE-shm" "$SNAP-shm"; fi
sqlite3 "$SNAP" \
  "select 'approved pages: '||count(*) from wiki_pages where status='approved';
   select 'graph edges:    '||count(*) from page_links;
   select 'appr<->appr:    '||count(*) from page_links pl
     join wiki_pages a on a.id=pl.from_page_id and a.status='approved'
     join wiki_pages b on b.id=pl.to_page_id and b.status='approved';"
rm -f "$SNAP" "$SNAP-wal" "$SNAP-shm"

echo ""
echo "== ③ 证据门（条件 10②，live 库直评） =="
# abeval -db 缺省 = app 的 live 库；它自己 CopyForTooling 快照评估，不写 live
GATE_STATUS=0
go run -buildvcs=false ./cmd/abeval -mode evidence -cases "$CASES" -k 10 -min-recall 0.05 || GATE_STATUS=$?

echo ""
echo "== ④ 逐案诊断（条件 10③） =="
go run -buildvcs=false ./cmd/abevaldiag -mode evidence -cases "$CASES" -k 10 -only-miss

echo ""
echo "===== 结论 ====="
echo "门② delta 过 +0.05 且 ③ 诊断形如「种子页→白名单边→邻接页」："
echo "  → docs/adr/0018 状态行 lane 2 改 Accepted（+英文镜像），开 wiki_graph_search。"
echo "  不过 → lane 2 维持 Proposed（密度欠账已还，delta 是独立命题，不粉饰）。"
exit "$GATE_STATUS"
