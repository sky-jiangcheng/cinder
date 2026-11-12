// Package wikilint is the wiki lint pass (ADR-0014 决策 5 / TODO M6-W4) — the
// anti-corruption layer. It split out of internal/service in ADR-0060 step four,
// under the same strangler rules as reviewrun / wikipage / wikigen:
//
//   - nothing here takes a *Service; the database and one wikigen.Chat seam
//     arrive as parameters;
//   - what stays in internal/service is scheduling, not policy: the ticker
//     (bgGo), the job queue and the report's binding type all remain there, and
//     the Service keeps a RunWikiLint facade so bindings, the ticker and the job
//     worker call the same name as before;
//   - this package must never import internal/service, and `go list -deps` is
//     how that is checked.
//
// The model half reuses wikigen.Chat rather than inventing a seam: it has the
// same shape as merge-suggest (one batch of pages in, findings plus a "why it
// did not run" note out), and merge-suggest is already a wikigen kernel called
// from this pass. Sharing the seam is also what makes the gate order testable
// without a service — see wikigen's seam_test-style counting fake.
//
// The rule that makes this safe to leave on: a lint finding never mutates the
// wiki. Every finding, mechanical or LLM-suggested, becomes a todo on the project
// that owns the page. An LLM judging other LLM output will sometimes be wrong, and
// a wrong suggestion costs the user one glance at a checkbox; a wrong "fix" that
// rewrites a page corrupts the store one layer deeper and hides the corruption.
// TestWikiLint_NeverMutatesPages (service side, through the facade) is the
// contract test for that sentence.
package wikilint

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
	"repotory/internal/service/wikipage"
)

const (
	// lintMaxLLMPages bounds the model pass by cost and context, not correctness.
	lintMaxLLMPages = 24
	// lintExcerptBytes is how much of each page the model sees.
	lintExcerptBytes = 500
	// lintMinRefTitleLen keeps the missing-cross-reference heuristic from
	// suggesting links for two-word titles that appear everywhere.
	lintMinRefTitleLen = 6
	// lintMaxPairScan bounds the O(n^2) scan on a large store.
	lintMaxPairScan = 200
	// lintThinPageBytes: below this a page is a stub, not knowledge.
	lintThinPageBytes = 80
)

// WikiFinding is the range's finding type, in the write-side package because the
// model-driven kernels both produce and consume it; this alias keeps the lint
// code reading the way it always has. The service package carries the same alias
// for the report's binding type.
type WikiFinding = wikigen.WikiFinding

// WikiLintReport is one pass, and is what the binding returns. It is defined here
// and aliased in internal/service so the Wails binding names an unchanged type.
type WikiLintReport struct {
	ProjectID int64         `json:"project_id"`
	Pages     int           `json:"pages"`
	Findings  []WikiFinding `json:"findings"`
	// TodosCreated / TodosExisting separate "first run" from "idempotent re-run",
	// which is the difference between a maintenance tool and a todo spammer.
	TodosCreated  int `json:"todos_created"`
	TodosExisting int `json:"todos_existing"`
	TodosUnfiled  int `json:"todos_unfiled"` // no owning project, or a write failure
	// LLMRan says at least ONE model half executed. It used to mean "the model half
	// ran and had no complaint", which was exact while there was exactly one model
	// half; with ADR-0019's merge judgement sharing this pass, the two senses are
	// genuinely different questions and this one answers "did we ask the model
	// anything at all".
	LLMRan bool `json:"llm_ran"`
	// LLMNote lists, joined, why each model half did NOT run. "Nothing to report" and
	// "the feature is off" must not look identical in the output — and neither may
	// "contradictions ran while merge judging is switched off" read as a clean run of
	// everything, because a user then has no way to discover the second pass exists.
	LLMNote   string `json:"llm_note,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
	CheckedAt string `json:"checked_at"`
}

// Run lints projectID (0 = every page) and files findings as todos. wantLLM asks
// for the model-backed checks too; they still need their own config gate, so this
// is permission to consider, not an override.
func Run(ctx context.Context, database *sql.DB, chat wikigen.Chat, projectID int64, wantLLM bool) (*WikiLintReport, error) {
	start := time.Now()
	pages, err := db.ListWikiPages(database, "", projectID)
	if err != nil {
		return nil, err
	}
	report := &WikiLintReport{
		ProjectID: projectID,
		Pages:     len(pages),
		Findings:  []WikiFinding{},
		CheckedAt: start.UTC().Format(time.RFC3339),
	}
	// Structural half: pure SQL, no model, no cost, no page writes.
	report.Findings = append(report.Findings, lintLinkShape(database, pages)...)
	report.Findings = append(report.Findings, lintContent(database, pages)...)
	report.Findings = append(report.Findings, lintCoverage(database, pages)...)
	// ADR-0018 lane 1: relation-shape checks are structural too (SQL over the edge
	// table, no model), and they only became expressible once relation was a
	// controlled vocabulary — you cannot ask "is this contradicts edge symmetric?"
	// when nobody agreed how "contradicts" is spelled.
	report.Findings = append(report.Findings, lintRelationShape(database, pages)...)

	if wantLLM {
		var notes []string
		findings, note := runWikiLintLLM(ctx, database, chat, pages)
		if note != "" {
			notes = append(notes, note)
		} else {
			report.LLMRan = true
		}
		report.Findings = append(report.Findings, findings...)

		// ADR-0019 待决 3: merge equivalence runs INSIDE the lint model half and
		// reuses its gate, its job and its budget — but carries its own consent key,
		// so "let a model judge similarity" is a separate decision from "let a model
		// judge contradictions". Its findings land in the same slice and therefore
		// get the same treatment: a todo, never a page mutation.
		mergeFindings, mergeNote := wikigen.SuggestMerges(ctx, database, chat, pages)
		if mergeNote != "" {
			notes = append(notes, mergeNote)
		} else {
			report.LLMRan = true
		}
		report.Findings = append(report.Findings, mergeFindings...)

		report.LLMNote = strings.Join(notes, "；")
	}

	// Findings arrive from several passes; a stable order keeps the todo dedupe
	// and any later diffing from depending on map iteration order.
	sort.SliceStable(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.PageID != b.PageID {
			return a.PageID < b.PageID
		}
		return a.Detail < b.Detail
	})

	fileFindings(database, report)
	report.ElapsedMS = time.Since(start).Milliseconds()
	return report, nil
}

// lintLinkShape: orphan pages, dangling body links, and titles mentioned without
// a link.
func lintLinkShape(database *sql.DB, pages []db.WikiPage) []WikiFinding {
	var out []WikiFinding
	for _, p := range pages {
		back, err := db.WikiEdgesTo(database, p.ID)
		if err != nil {
			continue
		}
		if len(back) == 0 {
			out = append(out, WikiFinding{
				Check: "orphan", Severity: "info", ProjectID: p.ProjectID, PageID: p.ID,
				Detail: fmt.Sprintf("页面 [[%s]] 没有任何反向链接，图里它是座孤岛", p.Slug),
			})
		}
		for _, m := range wikipage.WikiLinkRe.FindAllStringSubmatch(p.Content, -1) {
			target := strings.TrimSpace(m[1])
			if _, ok := findPageBySlug(pages, target); ok {
				continue
			}
			out = append(out, WikiFinding{
				Check: "dangling-link", Severity: "warn", ProjectID: p.ProjectID, PageID: p.ID,
				Detail: fmt.Sprintf("[[%s]] 指向不存在的页（出现在 [[%s]] 正文里）", target, p.Slug),
			})
		}
	}
	return append(out, lintMissingRefs(database, pages)...)
}

// lintMissingRefs is a labelled heuristic: another page's title appears in a body
// with no link to it. It emits a suggestion for a human and nothing more — an
// automated "add the obvious link" can invent a relation that was never intended.
func lintMissingRefs(database *sql.DB, pages []db.WikiPage) []WikiFinding {
	scan := pages
	if len(scan) > lintMaxPairScan {
		scan = scan[:lintMaxPairScan]
	}
	// Edges are pre-fetched per page: asking inside the inner loop would turn this
	// O(n^2) scan into O(n^2) queries.
	linked := make(map[int64]map[string]bool, len(scan))
	for _, a := range scan {
		m := map[string]bool{}
		if edges, err := db.WikiEdgesFrom(database, a.ID); err == nil {
			for _, e := range edges {
				m[e.Slug] = true
			}
		}
		linked[a.ID] = m
	}
	var out []WikiFinding
	for _, a := range scan {
		hay := strings.ToLower(a.Content)
		for _, b := range pages {
			if b.ID == a.ID {
				continue
			}
			title := strings.TrimSpace(b.Title)
			if len([]rune(title)) < lintMinRefTitleLen {
				continue
			}
			if !strings.Contains(hay, strings.ToLower(title)) {
				continue
			}
			if linked[a.ID][b.Slug] {
				continue // already linked; the body merely repeats the title
			}
			out = append(out, WikiFinding{
				Check: "missing-ref", Severity: "info", ProjectID: a.ProjectID, PageID: a.ID,
				Detail: fmt.Sprintf("[[%s]] 正文提到了「%s」却没有指向 [[%s]]，考虑补一条链接", a.Slug, title, b.Slug),
			})
		}
	}
	return out
}

// lintContent reports data gaps inside a single page.
func lintContent(database *sql.DB, pages []db.WikiPage) []WikiFinding {
	var out []WikiFinding
	for _, p := range pages {
		body := strings.TrimSpace(p.Content)
		if len(body) < lintThinPageBytes {
			out = append(out, WikiFinding{
				Check: "thin-page", Severity: "info", ProjectID: p.ProjectID, PageID: p.ID,
				Detail: fmt.Sprintf("页面 [[%s]] 只有 %d 字节，还不构成可依赖的知识", p.Slug, len(body)),
			})
		}
		notes, err := db.NotesForPage(database, p.ID)
		if err == nil && len(notes) == 0 {
			// Provenance is the one thing a derived layer cannot afford to lack: a
			// page with no source note cannot be checked against anything once it
			// turns out wrong.
			out = append(out, WikiFinding{
				Check: "no-source", Severity: "warn", ProjectID: p.ProjectID, PageID: p.ID,
				Detail: fmt.Sprintf("页面 [[%s]] 没有挂任何来源笔记，无法追溯它凭什么这么写", p.Slug),
			})
		}
	}
	return out
}

// lintCoverage reports a gap in the shape of the store rather than in one page:
// raw pages with nothing ever synthesized is the "compiled knowledge" claim
// failing to materialize.
func lintCoverage(database *sql.DB, pages []db.WikiPage) []WikiFinding {
	if len(pages) == 0 {
		return nil
	}
	counts, err := db.WikiPageCount(database)
	if err != nil {
		return nil
	}
	leafish := counts[db.WikiKindEntity] + counts[db.WikiKindConcept] + counts[db.WikiKindSource]
	if leafish >= 5 && counts[db.WikiKindSynthesis] == 0 {
		return []WikiFinding{{
			Check: "no-synthesis", Severity: "info", ProjectID: pages[0].ProjectID,
			Detail: fmt.Sprintf("已有 %d 张实体/概念/来源页，却没有任何综述页：知识仍是散片，未合成观点", leafish),
		}}
	}
	return nil
}

// lintRelationShape checks the graph's edges against the relation vocabulary
// (ADR-0018 lane 1). Like the other structural checks it is pure SQL + memory,
// never a model, and it only files todos — an edge that looks wrong is a hint a
// human weighs, not something lint fixes by rewriting a page or unlinking.
//
// It is scoped to the pages being linted (edges whose source is one of them), so
// a per-project pass does not file findings about unrelated projects' edges.
//
// Three decidable checks:
//   - supersedes inversion: A claims to supersede B, but B was updated later, so
//     the buried claim is the newer one;
//   - relation-cycle: part-of / depends chains that loop back on themselves
//     (composition and dependency are meant to be acyclic);
//   - contradicts one-sided: A contradicts B without B contradicting A — a
//     contradiction is symmetric by nature, so the mirror is usually just missing.
func lintRelationShape(database *sql.DB, pages []db.WikiPage) []WikiFinding {
	edges, err := db.ListPageLinks(database)
	if err != nil || len(edges) == 0 {
		return nil
	}
	inSet := make(map[int64]bool, len(pages))
	for _, p := range pages {
		inSet[p.ID] = true
	}
	scoped := edges[:0]
	for _, e := range edges {
		if inSet[e.FromID] {
			scoped = append(scoped, e)
		}
	}
	edges = scoped

	var out []WikiFinding

	for _, e := range edges {
		if e.Relation == db.RelationSupersedes && e.FromUpdatedAt != "" && e.ToUpdatedAt != "" &&
			e.FromUpdatedAt < e.ToUpdatedAt {
			out = append(out, WikiFinding{
				Check: "relation-supersedes-inverted", Severity: "warn",
				ProjectID: e.FromProjectID, PageID: e.FromID,
				Detail: fmt.Sprintf("[[%s]] 声称取代 [[%s]]，但被取代页更新更晚（%s > %s），取代方向可能反了",
					e.FromSlug, e.ToSlug, e.ToUpdatedAt, e.FromUpdatedAt),
			})
		}
	}

	out = append(out, lintRelationCycles(edges, map[string]bool{
		db.RelationPartOf: true, db.RelationDepends: true,
	})...)

	contradict := map[[2]int64]bool{}
	for _, e := range edges {
		if e.Relation == db.RelationContradicts {
			contradict[[2]int64{e.FromID, e.ToID}] = true
		}
	}
	sawFrom := map[int64]bool{}
	for _, e := range edges {
		if e.Relation != db.RelationContradicts || contradict[[2]int64{e.ToID, e.FromID}] || sawFrom[e.FromID] {
			continue
		}
		sawFrom[e.FromID] = true
		out = append(out, WikiFinding{
			Check: "relation-contradicts-one-sided", Severity: "info",
			ProjectID: e.FromProjectID, PageID: e.FromID,
			Detail: fmt.Sprintf("[[%s]] 标记与 [[%s]] 冲突，但只有单向：冲突本应对称，考虑补一条反向链接",
				e.FromSlug, e.ToSlug),
		})
	}
	return out
}

// lintRelationCycles runs a colored DFS over the whitelisted relations and
// reports each node that closes a back edge (a cycle). It reports the closing
// node once, not the whole cycle membership — enough for a human to go look.
func lintRelationCycles(edges []db.PageLinkRow, allow map[string]bool) []WikiFinding {
	adj := map[int64][]int64{}
	slug := map[int64]string{}
	proj := map[int64]int64{}
	for _, e := range edges {
		if !allow[e.Relation] {
			continue
		}
		adj[e.FromID] = append(adj[e.FromID], e.ToID)
		slug[e.FromID], slug[e.ToID] = e.FromSlug, e.ToSlug
		proj[e.FromID] = e.FromProjectID
	}
	const white, gray, black = 0, 1, 2
	color := map[int64]int{}
	reported := map[int64]bool{}
	var out []WikiFinding

	var visit func(int64)
	visit = func(u int64) {
		color[u] = gray
		for _, v := range adj[u] {
			switch color[v] {
			case gray:
				if !reported[u] {
					reported[u] = true
					out = append(out, WikiFinding{
						Check: "relation-cycle", Severity: "warn",
						ProjectID: proj[u], PageID: u,
						Detail: fmt.Sprintf("[[%s]] 在 part-of/depends 关系里成环（有向边指回自身），组成与依赖应无环", slug[u]),
					})
				}
			case white:
				visit(v)
			}
		}
		color[u] = black
	}
	for id := range adj {
		if color[id] == white {
			visit(id)
		}
	}
	return out
}

// fileFindings turns findings into todos on the owning project, deduplicated
// against that project's OPEN todos. A closed todo may come back: if a page
// drifts again after being ticked off, that is new information, not spam.
func fileFindings(database *sql.DB, report *WikiLintReport) {
	seen := map[string]bool{}
	for _, f := range report.Findings {
		title := wikigen.LintTodoTitle(f)
		key := fmt.Sprintf("%d|%s", f.ProjectID, title)
		if seen[key] {
			report.TodosExisting++
			continue
		}
		seen[key] = true
		if f.ProjectID == 0 {
			// A global page has no project to own the todo. Surfacing it in the
			// report is honest; inventing a home for it is not.
			report.TodosUnfiled++
			continue
		}
		open, err := db.HasTodoWithTitle(database, f.ProjectID, title)
		if err != nil {
			log.Printf("wiki lint: dedupe check failed for project %d: %v", f.ProjectID, err)
		}
		if open {
			report.TodosExisting++
			continue
		}
		if _, err := db.CreateTodo(database, f.ProjectID, title); err != nil {
			log.Printf("wiki lint: could not file todo %q: %v", title, err)
			report.TodosUnfiled++
			continue
		}
		report.TodosCreated++
	}
}

func findPageBySlug(pages []db.WikiPage, slug string) (db.WikiPage, bool) {
	for _, p := range pages {
		if p.Slug == slug {
			return p, true
		}
	}
	return db.WikiPage{}, false
}
