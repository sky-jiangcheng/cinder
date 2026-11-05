// Command promote-m7: 把 M7 lane 2 图密度成果（ADR-0018 条件 10①：approved ≥ 15 页 / ≥ 20 边）
// 从工作副本迁移进 live 知识库，并给出证据门重跑指引。
//
// 背景：agent sandbox 对 ~/Library/Application Support/repotory/ 是内核级只读，
// 所以 P0 的 26 篇 ADR 导入 + LLM 编译 + 页面批准全程在可写副本上完成。
// 本工具是「最后一公里」：在普通终端（无 sandbox）里把副本增量迁进 live 库。
//
// 用法（在仓库根目录，普通终端；务必先关闭 Cinder Desktop，避免 WAL 写竞争）：
//
//	go run ./cmd/promote-m7 \
//	  -from .agnes/work/p0run/live-copy/repotory.db \
//	  -to "$HOME/Library/Application Support/repotory/repotory.db"
//
// 脚本封装（迁移 + 证据门 + 逐案诊断三步走）：
//
//	bash scripts/promote-m7-lane2.sh
//
// 幂等：所有 INSERT 均为 OR IGNORE（按主键/UNIQUE 约束去重），重跑无副作用。
// 只迁移增量（副本里 source='repotory-adr' 的笔记、id 大于 live 上限的页/边/投影），
// live 库既有数据一个字节不动；live 先备份到 <to>.backup-<stamp>。
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"repotory/internal/db"
)

func main() {
	from := flag.String("from", "", "工作副本 db 路径（P0 成果所在）")
	to := flag.String("to", "", "live db 路径（macOS 默认 ~/Library/Application Support/repotory/repotory.db）")
	gate := flag.Bool("gate", false, "迁移后打印证据门重跑命令（不自动执行 LLM 评估）")
	flag.Parse()

	if *from == "" || *to == "" {
		log.Fatal("promote-m7: -from 和 -to 都必填")
	}

	// 1) 备份 live（VACUUM INTO 一致性快照，含 WAL 侧车）。
	//    走 db.InitDB 打开：自动把 live 迁到最新 schema（v26）、开 WAL、busy_timeout，
	//    与桌面 App 打开 live 库完全一致。
	database, err := db.InitDB(*to)
	if err != nil {
		log.Fatalf("打开 live db: %v（先确认已关闭 Cinder Desktop）", err)
	}
	defer database.Close()

	stamp := time.Now().Format("20060102-150405")
	backup := *to + ".backup-" + stamp
	// VACUUM INTO 不能绑定参数：把路径转义后内联成字符串字面量。
	if _, err := database.Exec("VACUUM INTO '" + strings.ReplaceAll(backup, "'", "''") + "'"); err != nil {
		log.Fatalf("备份 live db 失败: %v", err)
	}
	log.Printf("live 库已备份 → %s", backup)

	// 2) 附带副本（同机两个文件，ATTACH 零拷贝）
	if _, err := database.Exec("ATTACH DATABASE '" + strings.ReplaceAll(*from, "'", "''") + "' AS src"); err != nil {
		log.Fatalf("ATTACH 副本失败: %v", err)
	}
	defer database.Exec("DETACH DATABASE src") //nolint:errcheck // detach 失败只影响日志

	// 3) 计算增量边界
	var liveMaxPage, liveMaxNote int64
	database.QueryRow("SELECT COALESCE(MAX(id),0) FROM wiki_pages").Scan(&liveMaxPage)
	database.QueryRow("SELECT COALESCE(MAX(id),0) FROM project_notes").Scan(&liveMaxNote)
	log.Printf("live 边界: wiki_pages<=%d project_notes<=%d", liveMaxPage, liveMaxNote)

	// 4) 迁移（全部 OR IGNORE，幂等）。
	//    两侧此刻都已被 InitDB 迁到同一 schema（v26），SELECT * 列对齐安全；
	//    显式列名会在下次 schema 演进后悄悄漏列，* 不会。
	type step struct {
		name string
		stmt string
		args []any
	}
	steps := []step{
		{"project_notes(ADR)",
			"INSERT OR IGNORE INTO main.project_notes " +
				"SELECT * FROM src.project_notes WHERE source='repotory-adr'", nil},
		{"wiki_pages(新增)",
			"INSERT OR IGNORE INTO main.wiki_pages " +
				"SELECT * FROM src.wiki_pages WHERE id > ?", []any{liveMaxPage}},
		{"page_links(新增)",
			"INSERT OR IGNORE INTO main.page_links " +
				"SELECT * FROM src.page_links WHERE from_page_id > ? OR to_page_id > ?",
			[]any{liveMaxPage, liveMaxPage}},
		{"note_pages(投影)",
			"INSERT OR IGNORE INTO main.note_pages " +
				"SELECT * FROM src.note_pages WHERE page_id > ? OR note_id > ?",
			[]any{liveMaxPage, liveMaxNote}},
	}
	for _, st := range steps {
		res, err := database.Exec(st.stmt, st.args...)
		if err != nil {
			log.Fatalf("迁移 %s 失败: %v（live 未动，备份在 %s）", st.name, err, backup)
		}
		n, _ := res.RowsAffected()
		log.Printf("迁移 %-18s 写入 %d 行（OR IGNORE 去重后）", st.name, n)
	}

	// 5) 校验
	if _, err := database.Exec("ANALYZE"); err != nil {
		log.Fatalf("ANALYZE: %v", err)
	}
	var ok string
	database.QueryRow("PRAGMA integrity_check").Scan(&ok)
	if ok != "ok" {
		log.Fatalf("live 库完整性检查失败: %s（回滚：cp %s %s）", ok, backup, *to)
	}
	var appr, edges, orphans int
	database.QueryRow("SELECT count(*) FROM wiki_pages WHERE status='approved'").Scan(&appr)
	database.QueryRow("SELECT count(*) FROM page_links").Scan(&edges)
	database.QueryRow(`SELECT count(*) FROM page_links pl
		LEFT JOIN wiki_pages f ON f.id = pl.from_page_id
		LEFT JOIN wiki_pages t ON t.id = pl.to_page_id
		WHERE f.id IS NULL OR t.id IS NULL`).Scan(&orphans)
	log.Printf("迁移后 live: approved=%d edges=%d (目标 >=15/>=20) 孤立边=%d integrity=%s",
		appr, edges, orphans, ok)
	if orphans > 0 {
		log.Fatalf("存在 %d 条孤立边——live 库已写入但引用不完整，请用备份恢复: cp %s %s", orphans, backup, *to)
	}
	if appr < 15 || edges < 20 {
		log.Printf("警告：密度未达条件 10①（approved>=15 且 edges>=20），lane 2 维持 Proposed")
	} else {
		log.Printf("✓ 条件 10① 密度达标：approved=%d edges=%d", appr, edges)
	}

	// 6) 证据门指引（条件 10②③：delta > +0.05 且 k=2..20 全区间为正）
	fmt.Println("\n=== 下一步：证据门（条件 10②，迁移不会自动跑） ===")
	if *gate {
		fmt.Println("  go run ./cmd/abeval -mode evidence -cases .annotation-prep/evidence-cases-enriched.jsonl -k 10 -min-recall 0.05")
	} else {
		fmt.Println("  go run ./cmd/abeval -mode evidence -cases <你的标注用例>.jsonl -k 10 -min-recall 0.05")
	}
	fmt.Println("  # 逐案诊断（条件 10③）: go run ./cmd/abevaldiag -mode evidence -cases 同上")
	fmt.Println("  delta 过门：ADR-0018 lane 2 改 Accepted、开 wiki_graph_search；不过：维持 Proposed。")
}
