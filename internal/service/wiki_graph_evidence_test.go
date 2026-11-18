package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"repotory/internal/db"
)

// The tests here pin the seed-rescue contract of ADR-0018 lane 2's evidence
// path: with wiki_graph_search ON, a query the trigram lexicon misses entirely
// still reaches the right page through page-vector recall ("向量粗筛"), and the
// whitelist-edge expansion ("图精排") can surface a neighbour that NEITHER FTS
// NOR vector recall returned. With the switch OFF, the same query must return
// NO pages — the off state is the exact FTS behaviour (zero regression).

// The query is chosen so no page below contains any of its trigrams, which is
// what makes the FTS miss structural rather than incidental.
const vectorSeedQuery = "陌生领域探索"

// pageSeedStubEmbedServer maps embedding inputs to fixed 4-dim vectors so the
// test controls the KNN geometry without a model:
//
//	甲 → [1,0,0,0]   (exact match for the query vector — always recalled first)
//	丙 → [0,1,0,0]   (filler mass at distance √2)
//	乙 → [0,0,0,1]   (farthest, √3 — deterministically excluded from top-20 KNN
//	                  once ≥20 fillers exist, so it is reachable ONLY via the graph)
//	the query itself → [1,0,0,0]
//
// A page's embedding input is PageEmbeddingText = title + "\n" + content, so
// one marker rune in the title is enough to steer its vector.
func pageSeedStubEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req hybridEmbedReq
		_ = json.Unmarshal(b, &req)
		type datum struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]datum, len(req.Input))
		for i, s := range req.Input {
			var v []float32
			switch {
			case s == vectorSeedQuery:
				v = []float32{1, 0, 0, 0}
			case containsRune(s, '甲'):
				v = []float32{1, 0, 0, 0}
			case containsRune(s, '丙'):
				v = []float32{0, 1, 0, 0}
			case containsRune(s, '乙'):
				v = []float32{0, 0, 0, 1}
			default:
				v = []float32{0, 0, 1, 0}
			}
			data[i] = datum{Index: i, Embedding: v}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// seedGraphPage creates one approved page whose embedding input carries marker.
func seedGraphPage(t *testing.T, svc *Service, projectID int64, slug, title, content string) int64 {
	t.Helper()
	p, err := db.CreateWikiPageAs(svc.db, db.WikiKindConcept, slug, title, projectID, content, db.WikiStatusApproved, "manual")
	if err != nil {
		t.Fatalf("create page %s: %v", slug, err)
	}
	return p.ID
}

// configurePageEmbeddings points the shared embedder at the stub with a known
// dimension and turns consent on.
func configurePageEmbeddings(t *testing.T, svc *Service, srv *httptest.Server) {
	t.Helper()
	for k, v := range map[string]string{
		"embedding_base_url": srv.URL + "/v1",
		"embedding_model":    "stub-embed",
		"embedding_dim":      "4",
		"semantic_search":    "1",
	} {
		if err := db.SetConfig(svc.db, k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
}

// pageIDsOf collects the page-type evidence items in ranked order.
func pageIDsOf(ev *Evidence) []int64 {
	var out []int64
	for _, it := range ev.Items {
		if it.Type == "page" {
			out = append(out, it.ID)
		}
	}
	return out
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

var pageEvalBudget = EvidenceBudget{MaxItems: 60, MaxChars: 400000, Deadline: 30 * time.Second}

// TestPageVectorRecallGatesAndOrder pins the degrade-to-nil gates (consent,
// endpoint, index readiness) and the KNN-ordered output of the seed-recall
// stage itself.
func TestPageVectorRecallGatesAndOrder(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "graphproj", "/tmp/graphproj")
	pageA := seedGraphPage(t, svc, pid, "jia-ti-xi", "甲体系", "甲的内容描述")
	pageB := seedGraphPage(t, svc, pid, "yi-ye-mian", "乙页面", "乙的内容描述")

	// No consent, no endpoint, no index: every gate must hold the stage dark.
	if got := svc.pageVectorRecall(vectorSeedQuery, 5); got != nil {
		t.Fatalf("recall without any setup must be nil, got %d pages", len(got))
	}

	srv := pageSeedStubEmbedServer(t)
	defer srv.Close()
	configurePageEmbeddings(t, svc, srv)

	// Consent + endpoint present, but the index was never built: still nil.
	if got := svc.pageVectorRecall(vectorSeedQuery, 5); got != nil {
		t.Fatalf("recall without a built index must be nil, got %d pages", len(got))
	}

	if n, err := svc.RebuildPageEmbeddings(); err != nil || n != 2 {
		t.Fatalf("RebuildPageEmbeddings = %d, %v; want 2, nil", n, err)
	}

	rec := svc.pageVectorRecall(vectorSeedQuery, 5)
	if len(rec) != 2 {
		t.Fatalf("recall = %d pages, want 2", len(rec))
	}
	if rec[0].ID != pageA || rec[1].ID != pageB {
		t.Fatalf("recall order = [%d, %d], want KNN order [%d, %d]", rec[0].ID, rec[1].ID, pageA, pageB)
	}
	// A tighter k truncates in KNN order, never reshuffles it.
	if rec = svc.pageVectorRecall(vectorSeedQuery, 1); len(rec) != 1 || rec[0].ID != pageA {
		t.Fatalf("recall(k=1) = %v, want [%d] only", rec, pageA)
	}

	// Withdrawing consent at query time must darken the stage again — page
	// embedding inherits semantic_search's consent, exactly like the note path.
	if err := db.SetConfig(svc.db, "semantic_search", "0"); err != nil {
		t.Fatal(err)
	}
	if got := svc.pageVectorRecall(vectorSeedQuery, 5); got != nil {
		t.Fatalf("recall without consent must be nil, got %d pages", len(got))
	}
}

// TestEvidencePagesVectorSeedRescue: with graph search ON, a query FTS misses
// entirely still surfaces pages (vector seeds); with the switch OFF the same
// query surfaces none (the off state is pure FTS — the zero-regression bar).
func TestEvidencePagesVectorSeedRescue(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "graphproj", "/tmp/graphproj")
	pageA := seedGraphPage(t, svc, pid, "jia-ti-xi", "甲体系", "甲的内容描述")
	pageB := seedGraphPage(t, svc, pid, "yi-ye-mian", "乙页面", "乙的内容描述")
	if err := db.LinkWikiPages(svc.db, pageA, pageB, db.RelationRef); err != nil {
		t.Fatal(err)
	}

	// Precondition: the trigram index genuinely has nothing for this query.
	if hits, err := db.SearchWikiPages(svc.db, vectorSeedQuery, 0, 10); err != nil || len(hits) != 0 {
		t.Fatalf("precondition: FTS should miss %q, got %d hits (err %v)", vectorSeedQuery, len(hits), err)
	}

	srv := pageSeedStubEmbedServer(t)
	defer srv.Close()
	configurePageEmbeddings(t, svc, srv)
	if _, err := svc.RebuildPageEmbeddings(); err != nil {
		t.Fatal(err)
	}

	// OFF (default): no pages at all.
	_ = db.SetConfig(svc.db, "wiki_graph_search", "0")
	if ids := pageIDsOf(svc.GatherEvidence(context.Background(), 0, vectorSeedQuery, pageEvalBudget)); len(ids) != 0 {
		t.Fatalf("off state must surface no pages, got %v", ids)
	}

	// ON: both pages surface, vector-recall order preserved (A exact match first).
	_ = db.SetConfig(svc.db, "wiki_graph_search", "1")
	ids := pageIDsOf(svc.GatherEvidence(context.Background(), 0, vectorSeedQuery, pageEvalBudget))
	if len(ids) != 2 {
		t.Fatalf("graph arm = %v, want exactly the two seeded pages", ids)
	}
	if ids[0] != pageA || ids[1] != pageB {
		t.Fatalf("graph arm order = %v, want [%d, %d]", ids, pageA, pageB)
	}
}

// TestEvidencePagesGraphHopBeyondVectorRecall proves the "图精排" half: a page
// that FTS misses AND vector recall misses (farthest vector, pushed out of the
// top-20 KNN by filler mass) is still surfaced — only the whitelist-edge hop
// from the seed can reach it.
func TestEvidencePagesGraphHopBeyondVectorRecall(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "graphproj", "/tmp/graphproj")
	pageA := seedGraphPage(t, svc, pid, "jia-ti-xi", "甲体系", "甲的内容描述")
	pageB := seedGraphPage(t, svc, pid, "yi-ye-mian", "乙页面", "乙的内容描述")
	if err := db.LinkWikiPages(svc.db, pageA, pageB, db.RelationRef); err != nil {
		t.Fatal(err)
	}
	// 20 fillers at distance √2 fill the top-20 KNN slots around A; pageB sits at
	// √3 and is therefore deterministically outside vector recall.
	for i := 1; i <= 20; i++ {
		seedGraphPage(t, svc, pid, "bei-xuan-"+string(rune('a'+i-1)), "丙备选"+string(rune('A'+i-1)), "丙的内容若干")
	}

	if hits, err := db.SearchWikiPages(svc.db, vectorSeedQuery, 0, 10); err != nil || len(hits) != 0 {
		t.Fatalf("precondition: FTS should miss %q, got %d hits (err %v)", vectorSeedQuery, len(hits), err)
	}

	srv := pageSeedStubEmbedServer(t)
	defer srv.Close()
	configurePageEmbeddings(t, svc, srv)
	if _, err := svc.RebuildPageEmbeddings(); err != nil {
		t.Fatal(err)
	}

	// Precondition: pageB is beyond the vector-recall cutoff.
	rec := svc.pageVectorRecall(vectorSeedQuery, pageVecRecallK)
	if containsID(idsOfPages(rec), pageB) {
		t.Fatal("precondition broken: pageB came back from vector recall; the test would not exercise the graph hop")
	}
	if !containsID(idsOfPages(rec), pageA) {
		t.Fatal("precondition broken: seed pageA itself was not recalled")
	}

	// OFF: nothing (pure FTS).
	_ = db.SetConfig(svc.db, "wiki_graph_search", "0")
	if ids := pageIDsOf(svc.GatherEvidence(context.Background(), 0, vectorSeedQuery, pageEvalBudget)); len(ids) != 0 {
		t.Fatalf("off state must surface no pages, got %v", ids)
	}

	// ON: pageB surfaces through the A --ref--> B hop.
	_ = db.SetConfig(svc.db, "wiki_graph_search", "1")
	ids := pageIDsOf(svc.GatherEvidence(context.Background(), 0, vectorSeedQuery, pageEvalBudget))
	if !containsID(ids, pageB) {
		t.Fatalf("graph hop failed: pageB %d not in evidence %v", pageB, ids)
	}
	if !containsID(ids, pageA) {
		t.Fatalf("seed pageA %d not in evidence %v", pageA, ids)
	}
}

// TestEvidencePagesFTSPrefixPreservedUnderGraph pins the zero-regression
// guarantee of the staged shape: when FTS hits, the page prefix under the graph
// arm must be BYTE-IDENTICAL to the FTS ordering (the graph stage may only
// APPEND neighbours behind it) — this is what makes condition 10②'s "no case
// regression" structural rather than aspirational.
func TestEvidencePagesFTSPrefixPreservedUnderGraph(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "graphproj", "/tmp/graphproj")
	// C lexically matches the query; D is its whitelist neighbour and does not.
	pageC := seedGraphPage(t, svc, pid, "bing-ji", "丙机机制", "陌生领域探索的实际机制与记录")
	pageD := seedGraphPage(t, svc, pid, "ding-ji", "丁记页面", "丁的内容描述")
	if err := db.LinkWikiPages(svc.db, pageC, pageD, db.RelationRef); err != nil {
		t.Fatal(err)
	}

	if hits, err := db.SearchWikiPages(svc.db, vectorSeedQuery, 0, 10); err != nil || len(hits) != 1 || hits[0].ID != pageC {
		t.Fatalf("precondition: FTS should return exactly page %d, got %v (err %v)", pageC, hits, err)
	}

	// OFF: pure FTS.
	_ = db.SetConfig(svc.db, "wiki_graph_search", "0")
	offIDs := pageIDsOf(svc.GatherEvidence(context.Background(), 0, vectorSeedQuery, pageEvalBudget))
	if len(offIDs) != 1 || offIDs[0] != pageC {
		t.Fatalf("off state = %v, want [%d]", offIDs, pageC)
	}

	// ON: the FTS prefix stands untouched; the neighbour appends behind it.
	_ = db.SetConfig(svc.db, "wiki_graph_search", "1")
	onIDs := pageIDsOf(svc.GatherEvidence(context.Background(), 0, vectorSeedQuery, pageEvalBudget))
	if len(onIDs) != 2 || onIDs[0] != pageC || onIDs[1] != pageD {
		t.Fatalf("on state = %v, want [%d %d] (prefix preserved, neighbour appended)", onIDs, pageC, pageD)
	}
	// The OFF prefix is a prefix of the ON list, position for position.
	for i, id := range offIDs {
		if onIDs[i] != id {
			t.Fatalf("graph arm reordered the FTS prefix at %d: %v vs %v", i, onIDs, offIDs)
		}
	}
}

func idsOfPages(pages []db.WikiPage) []int64 {
	out := make([]int64, len(pages))
	for i, p := range pages {
		out[i] = p.ID
	}
	return out
}
