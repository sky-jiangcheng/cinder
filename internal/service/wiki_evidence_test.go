package service

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/search/abeval"
)

// The W2 acceptance gate, run on a fixture corpus.
//
// ADR-0014 sets W2's bar as "过 cmd/abeval 门（真实标注 query 集 + Recall@k /
// NDCG@k delta），不接受『感觉变好了』". The honest version of that under the
// chosen sequencing (pages cannot exist in volume before W3 compiles them) is
// what this file does: the MECHANISM is measured against labelled cases on a
// fixture, while any claim about the user's real corpus stays unproven. t.Logf
// prints the numbers so a regression is visible, and the assertions are what stop
// "evidence retrieval" from shipping as a slower way of doing nothing.

// id-space encoding shared by both retrievers under test.
//
// abeval.DocID carries the type tag, so page ids and note ids cannot be confused:
// {page,7} never satisfies a label of {note,7}. This used to be a hand-rolled
// int64 epoch (page ids += 1_000_000), which enforced the same separation but
// only by convention — any new caller that forgot the offset reintroduced the
// cross-type collision that keeps SearchAll out of the RRF fuse.
func encodedRef(item EvidenceItem) abeval.DocID {
	return abeval.DocID{Type: item.Type, ID: item.ID}
}

// legacyNoteIDs reproduces the pre-W2 behaviour exactly: the ten notes a
// recency-ordered listing put first, with no relevance anywhere in the choice.
// Under the old created_at ASC order that was the ten OLDEST notes, which is
// the baseline this gate is written against ("retrieval must beat stuffing the
// ten oldest"). Sorting by created_at here rather than reusing ListNotes keeps
// the baseline pinned to that meaning: when ListNotes flipped to updated_at
// DESC the naive "first 10" quietly became the ten NEWEST — i.e. the newest
// notes, which is no longer a stuffing baseline at all.
func legacyNoteIDs(t *testing.T, svc *Service, projectID int64) []int64 {
	t.Helper()
	notes, err := db.ListNotes(svc.db, projectID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].CreatedAt != notes[j].CreatedAt {
			return notes[i].CreatedAt < notes[j].CreatedAt
		}
		return notes[i].ID < notes[j].ID
	})
	var out []int64
	for i, n := range notes {
		if i >= 10 {
			break
		}
		out = append(out, n.ID)
	}
	return out
}

func evidenceIDs(t *testing.T, svc *Service, projectID int64, query string) []abeval.DocID {
	t.Helper()
	ev := svc.GatherEvidence(projectID, query, EvidenceBudget{})
	out := make([]abeval.DocID, 0, len(ev.Items))
	for _, it := range ev.Items {
		out = append(out, encodedRef(it))
	}
	return out
}

// buildGateCorpus creates a project whose knowledge is deliberately buried: the
// six answer-bearing notes are written FIRST, then 24 filler notes, so the
// recency-ordered legacy block cannot see them. Six pages carry the same topics in
// title/content so retrieval can find them.
func buildGateCorpus(t *testing.T, svc *Service) (projectID int64, noteIDs, pageIDs []int64) {
	t.Helper()
	projectID = seedProject(t, svc.db, "gate", "/tmp/gate")

	// 24 filler notes are written FIRST on purpose: db.ListNotes orders
	// `pinned DESC, sort_order ASC, created_at ASC, id ASC`, i.e. oldest first, so
	// the legacy "10 notes" window is the ten oldest ever written. Answers created
	// afterwards therefore fall outside it, which is what makes the gate measure
	// relevance rather than ordering.
	for i := 0; i < 24; i++ {
		if _, err := db.CreateNoteEx(svc.db, projectID,
			fmt.Sprintf("chore note %d", i), "routine scratch content with no bearing on any question",
			"", "log", "manual"); err != nil {
			t.Fatal(err)
		}
	}

	topics := []struct {
		slug, title, body string
	}{
		{"payment-gateway-retry", "Payment Gateway retry policy",
			"Payments retry three times with exponential backoff; the idempotency key is the invoice id."},
		{"session-timeout", "Session timeout behaviour",
			"Sessions expire after 30 minutes idle; refresh tokens rotate on every use."},
		{"release-checklist", "Release checklist",
			"Releases need a migration dry-run, a sha256 manifest fill and a rollback note."},
		{"sqlite-vec-choice", "Why sqlite-vec",
			"Vector storage stays local because the pure-Go port keeps the zero-CGO build promise."},
		{"handoff-protocol", "Handoff note protocol",
			"Handoff notes carry the handoff tag and refuse to be overwritten by notes_update."},
		{"scan-depth", "Scan depth semantics",
			"scan_depth bounds directory descent; repositories already known are always re-scanned."},
	}
	for _, tp := range topics {
		n, err := db.CreateNoteEx(svc.db, projectID, tp.title, tp.body, "gate", "knowledge", "manual")
		if err != nil {
			t.Fatal(err)
		}
		noteIDs = append(noteIDs, n.ID)
		p, err := db.CreateWikiPage(svc.db, db.WikiKindEntity, tp.slug, tp.title, projectID, tp.body)
		if err != nil {
			t.Fatal(err)
		}
		pageIDs = append(pageIDs, p.ID)
	}
	return projectID, noteIDs, pageIDs
}

func gateCases(noteIDs, pageIDs []int64) []abeval.Case[abeval.DocID] {
	at := func(ids []int64, i int) int64 { return ids[i] }
	return []abeval.Case[abeval.DocID]{
		{Query: "payment gateway retry idempotency backoff",
			Relevant: []abeval.DocID{{Type: "note", ID: at(noteIDs, 0)}, {Type: "page", ID: at(pageIDs, 0)}}},
		{Query: "when does the session timeout rotate refresh tokens",
			Relevant: []abeval.DocID{{Type: "note", ID: at(noteIDs, 1)}, {Type: "page", ID: at(pageIDs, 1)}}},
		{Query: "release checklist migration dry run rollback",
			Relevant: []abeval.DocID{{Type: "note", ID: at(noteIDs, 2)}, {Type: "page", ID: at(pageIDs, 2)}}},
		{Query: "why sqlite-vec zero CGO vector storage",
			Relevant: []abeval.DocID{{Type: "note", ID: at(noteIDs, 3)}, {Type: "page", ID: at(pageIDs, 3)}}},
		{Query: "handoff notes refuse notes_update overwrite",
			Relevant: []abeval.DocID{{Type: "note", ID: at(noteIDs, 4)}, {Type: "page", ID: at(pageIDs, 4)}}},
		{Query: "scan depth directory descent repositories",
			Relevant: []abeval.DocID{{Type: "note", ID: at(noteIDs, 5)}, {Type: "page", ID: at(pageIDs, 5)}}},
	}
}

// The gate itself: retrieval must beat "the ten oldest notes" on labelled cases.
//
// Read the numbers with the fixture in mind: the corpus is built so that every
// answer-bearing note sits OUTSIDE the legacy window, which is why the baseline
// measures 0.000 rather than merely low. That proves the mechanism routes to the
// answer instead of to recency; it does NOT say how much real users gain, because
// real note orders are not this adversarial. The honest version of the W2 gate on
// real data still needs a labelled set drawn from an actual library.
func TestEvidenceRetrievalGate_BeatsLegacyStuffing(t *testing.T) {
	svc, _ := setupService(t)
	projectID, noteIDs, pageIDs := buildGateCorpus(t, svc)

	legacy := legacyNoteIDs(t, svc, projectID)
	if len(legacy) != 10 {
		t.Fatalf("legacy window = %d notes, want the documented 10", len(legacy))
	}
	// Guard the fixture itself: if an answer-bearing note happened to land inside
	// the legacy window, the comparison would be measuring noise, not the claim.
	inWindow := 0
	for _, want := range noteIDs {
		for _, got := range legacy {
			if got == want {
				inWindow++
			}
		}
	}
	if inWindow > 2 {
		t.Fatalf("%d/6 answer notes are already inside the legacy 10-note window; the corpus no longer separates relevance from recency", inWindow)
	}

	cases := gateCases(noteIDs, pageIDs)
	const k = 8
	lex, hyb, dRecall, dNDCG := abeval.Compare(cases, k,
		func(q string) []abeval.DocID {
			out := make([]abeval.DocID, 0, len(legacy))
			for _, id := range legacy {
				out = append(out, abeval.DocID{Type: "note", ID: id})
			}
			return out
		},
		func(q string) []abeval.DocID { return evidenceIDs(t, svc, projectID, q) })

	t.Logf("legacy (the 10 OLDEST notes, per ListNotes): %s", lex.SummaryLine("legacy"))
	t.Logf("evidence retrieval      : %s", hyb.SummaryLine("evidence"))
	t.Logf("delta Recall@%d = %+.3f, NDCG@%d = %+.3f", k, dRecall, k, dNDCG)

	if hyb.MeanRecall <= lex.MeanRecall {
		t.Errorf("evidence retrieval Recall@%d = %.3f did not beat legacy %.3f — the whole W2 claim is that ranking beats recency", k, hyb.MeanRecall, lex.MeanRecall)
	}
	if hyb.MeanNDCG <= lex.MeanNDCG {
		t.Errorf("evidence NDCG@%d = %.3f did not beat legacy %.3f", k, hyb.MeanNDCG, lex.MeanNDCG)
	}
	// A gate that only just clears zero is indistinguishable from noise; demand a
	// real margin on a corpus built to be winnable.
	if dRecall < 0.5 {
		t.Errorf("Recall delta %+.3f is below the 0.5 bar this fixture should clear", dRecall)
	}
	// Pages must be what carries the win: if evidence only ever returns notes, the
	// wiki layer is decorative.
	all := svc.GatherEvidence(projectID, "release checklist migration dry run rollback", EvidenceBudget{})
	var sawPage, sawNote bool
	for _, it := range all.Items {
		if it.Type == "page" {
			sawPage = true
		}
		if it.Type == "note" {
			sawNote = true
		}
	}
	if !sawPage {
		t.Error("evidence returned no pages; the wiki layer is not being used")
	}
	if !sawNote {
		t.Error("evidence returned no notes; existing knowledge is being ignored")
	}
}

// Budget: the three caps ADR-0014 asks for, each enforced on its own terms.
func TestEvidenceBudgetCaps(t *testing.T) {
	svc, _ := setupService(t)
	_, _, _ = buildGateCorpus(t, svc)
	query := "payment gateway retry idempotency backoff"

	capped := svc.GatherEvidence(0, query, EvidenceBudget{MaxItems: 2})
	uncapped := svc.GatherEvidence(0, query, EvidenceBudget{MaxItems: 20})
	if len(capped.Items) > 2 {
		t.Errorf("MaxItems 2 produced %d items", len(capped.Items))
	}
	// Truncation is only meaningful when there WAS more to give; asserting it
	// against a 2-candidate corpus would be asserting the fixture, not the cap.
	if len(uncapped.Items) > 2 && (!capped.Truncated || capped.Dropped == 0) {
		t.Errorf("%d candidates available but a 2-item cap reported no truncation (truncated=%v dropped=%d)",
			len(uncapped.Items), capped.Truncated, capped.Dropped)
	}
	// A character quota smaller than one long snippet must still return something
	// rather than looping or returning nothing: it is a ceiling, not a filter.
	tight := svc.GatherEvidence(0, query, EvidenceBudget{MaxItems: 12, MaxChars: 40})
	if len(tight.Items) == 0 {
		t.Error("a 40-char budget returned nothing; the first item must always fit or the cap is a mute button")
	}
	chars := 0
	for _, it := range tight.Items {
		chars += len([]rune(it.Title)) + len([]rune(it.Snippet))
	}
	// A 40-char quota admits exactly one item (the first always fits); two would
	// mean the ceiling is not applied per item.
	if len(tight.Items) != 1 {
		t.Errorf("a 40-char budget produced %d items, want exactly 1 (chars=%d)", len(tight.Items), chars)
	}
	// An empty or whitespace query must not flood the prompt with "everything".
	if ev := svc.GatherEvidence(0, "   ", EvidenceBudget{}); len(ev.Items) != 0 {
		t.Errorf("blank query returned %d items, want 0", len(ev.Items))
	}
	// Rendered refs must be unique and sequential per type, else citations collide.
	ev := svc.GatherEvidence(0, query, EvidenceBudget{})
	seen := map[string]bool{}
	for _, it := range ev.Items {
		if it.Ref == "" {
			t.Fatalf("item %s %d has no citation ref", it.Type, it.ID)
		}
		if seen[it.Ref] {
			t.Errorf("duplicate citation ref %s", it.Ref)
		}
		seen[it.Ref] = true
	}
	if block := ev.Render(); block == "" {
		t.Error("non-empty evidence rendered to nothing")
	} else if !strings.Contains(block, "[P") && !strings.Contains(block, "[N") {
		t.Error("rendered block carries no citation markers")
	}
}

// Cross-project bleed: a page owned by another project must not answer here.
func TestEvidenceRespectsProjectScoping(t *testing.T) {
	svc, _ := setupService(t)
	other := seedProject(t, svc.db, "other", "/tmp/other")
	if _, err := db.CreateWikiPage(svc.db, db.WikiKindEntity, "secret-of-other", "Secret Of Other",
		other, "invoice idempotency and retry policy live here"); err != nil {
		t.Fatal(err)
	}
	home := seedProject(t, svc.db, "home", "/tmp/home")
	if _, err := db.CreateWikiPage(svc.db, db.WikiKindEntity, "home-page", "Home Page",
		home, "nothing to do with payments at all"); err != nil {
		t.Fatal(err)
	}
	ev := svc.GatherEvidence(home, "invoice idempotency retry policy", EvidenceBudget{})
	for _, it := range ev.Items {
		if it.Type == "page" && strings.Contains(it.Title, "Secret Of Other") {
			t.Fatalf("another project's page leaked into evidence: %+v", it)
		}
	}
	// Global pages (project_id 0) are allowed everywhere — that is the point of them.
	glob, err := db.CreateWikiPage(svc.db, db.WikiKindConcept, "global-convention", "Global Convention",
		0, "invoice idempotency conventions across all projects")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range svc.GatherEvidence(home, "invoice idempotency conventions", EvidenceBudget{}).Items {
		if it.Type == "page" && it.ID == glob.ID {
			found = true
		}
	}
	if !found {
		t.Error("a global page was excluded from a project-scoped query")
	}
}

// The query loop: an answer becomes a `query` page linked to what it cited, so the
// store grows from use and not only from compiles.
func TestFileAnswerAsPageClosesTheLoop(t *testing.T) {
	svc, _ := setupService(t)
	projectID, _, pageIDs := buildGateCorpus(t, svc)

	refs := []string{"P1", "P2"}
	ev := svc.GatherEvidence(projectID, "release checklist migration dry run rollback", EvidenceBudget{})
	for _, it := range ev.Items {
		if it.Type == "page" {
			refs = append(refs, it.Ref)
		}
	}
	evForFiling := svc.GatherEvidence(projectID, "release checklist migration dry run rollback", EvidenceBudget{})
	id, err := svc.FileAnswerAsPage(projectID, "release checklist migration dry run rollback",
		"Always dry-run the migration, then fill the sha256 manifest, then write the rollback note.",
		evForFiling, refs)
	if err != nil {
		t.Fatalf("file answer: %v", err)
	}
	page, err := db.GetWikiPageByID(svc.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if page.Kind != db.WikiKindQuery {
		t.Errorf("filed page kind = %q, want %q", page.Kind, db.WikiKindQuery)
	}
	if !strings.Contains(page.Content, "rollback note") {
		t.Error("filed page lost the answer body")
	}
	out, err := db.WikiEdgesFrom(svc.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("a filed answer has no citation edges; the loop did not close")
	}
	var sawRef bool
	for _, e := range out {
		if e.Relation == db.RelationRef {
			sawRef = true
		}
	}
	if !sawRef {
		t.Errorf("filed answer edges = %+v, want at least one ref edge to a cited page", out)
	}

	// Filing the same question twice is a re-run: it updates rather than erroring
	// or spawning a shadow page.
	second, err := svc.FileAnswerAsPage(projectID, "release checklist migration dry run rollback",
		"Updated answer.", evForFiling, []string{"P1"})
	if err != nil {
		t.Fatalf("refiling the same question errored: %v", err)
	}
	if second != id {
		t.Errorf("refiling produced page %d, want the same page %d", second, id)
	}
	pages, _ := db.ListWikiPages(svc.db, db.WikiKindQuery, 0)
	if len(pages) != 1 {
		t.Errorf("%d query pages after two filings, want 1", len(pages))
	}
	// Cited notes attach as source material, not as peer pages. The note must be
	// reachable by ref, so it is written on-topic and its ref is read back out of
	// the same evidence set — which also proves ref -> object mapping resolves to
	// the right note rather than to the note with that numeric id.
	const loopQuery = "sqlite-vec note keeps vector storage local"
	if _, err := db.CreateNoteEx(svc.db, projectID, "sqlite-vec note",
		"sqlite-vec note keeps vector storage local and zero CGO.", "gate", "knowledge", "manual"); err != nil {
		t.Fatal(err)
	}
	noteEv := svc.GatherEvidence(projectID, loopQuery, EvidenceBudget{})
	var noteRef string
	var noteID int64
	for _, it := range noteEv.Items {
		if it.Type == "note" && strings.Contains(it.Title, "sqlite-vec note") {
			noteRef, noteID = it.Ref, it.ID
		}
	}
	if noteRef == "" {
		t.Fatal("fixture note was not retrieved; the loop test cannot assert attachment")
	}
	filedAgain, err := svc.FileAnswerAsPage(projectID, loopQuery, "Because.",
		noteEv, []string{noteRef, "P1"})
	if err != nil {
		t.Fatal(err)
	}
	// A different question is a different page: assert on the page that was just
	// created, not on the first one (an earlier version of this test did exactly
	// that and "passed" nothing).
	attached, err := db.NotesForPage(svc.db, filedAgain)
	if err != nil {
		t.Fatal(err)
	}
	var got bool
	for _, a := range attached {
		if a == noteID {
			got = true
		}
	}
	if !got {
		t.Errorf("cited note %d was not attached to page %d (attached=%v)", noteID, id, attached)
	}
	// A ref that was never in the evidence set must not resolve to anything.
	if items := resolveAgainstEvidence(noteEv, []string{"P999", "N0", "N1", ""}); len(items) != 0 {
		t.Errorf("invented refs resolved to %d items, want 0", len(items))
	}
	_ = pageIDs
}

// L0/L1 two-phase packing (adapted from ai-memory's brief tiers): the character
// budget first buys DISTINCT evidence at a short excerpt, then deepens the best
// items toward the upstream ceiling. The old single-tier admission spent ~600
// runes on the first page and dropped every candidate behind it; the same
// budget must now see the full candidate set.
func TestEvidenceL0L1Packing(t *testing.T) {
	svc, _ := setupService(t)
	projectID := seedProject(t, svc.db, "l0l1", "/tmp/l0l1")

	const n = 12
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("深度主题%d %s", i, strings.Repeat("细节段落内容用于测试深度截断行为", 30))
		if _, err := db.CreateWikiPage(svc.db, db.WikiKindConcept,
			fmt.Sprintf("l0l1-page-%d", i), fmt.Sprintf("深度主题 %d", i), projectID, body); err != nil {
			t.Fatal(err)
		}
	}

	ev := svc.GatherEvidence(projectID, "深度主题", EvidenceBudget{MaxChars: 4000})

	if len(ev.Items) != n {
		t.Fatalf("L0 admission produced %d items; want all %d distinct candidates", len(ev.Items), n)
	}
	used := 0
	for _, it := range ev.Items {
		used += len([]rune(it.Title)) + len([]rune(it.Snippet))
	}
	if used > 4000 {
		t.Fatalf("packed %d chars over the 4000 budget", used)
	}
	// Deepening runs best-ranked first: item 0 must carry the FULL body (~454
	// runes), the last admitted item must still be at its L0 clip (200).
	first := len([]rune(ev.Items[0].Snippet))
	last := len([]rune(ev.Items[n-1].Snippet))
	if first <= last {
		t.Fatalf("deepening order wrong: first item %d runes <= last item %d runes", first, last)
	}
	if first < 400 {
		t.Fatalf("rank-1 snippet = %d runes; want the deepened full body (>=400)", first)
	}
	if last > 220 {
		t.Fatalf("last snippet = %d runes; want it untouched at L0 (<=200)", last)
	}
	// All-or-nothing upgrades: every snippet is either the full body or the exact
	// L0 clip — nothing in between.
	for i, it := range ev.Items {
		l := len([]rune(it.Snippet))
		if l > 220 && l < 400 {
			t.Fatalf("item %d snippet = %d runes: a half-deepened snippet slipped through", i, l)
		}
	}
}

// Access reinforcement: a page surfaced into an answer's evidence has been USED.
// The bump lands on the page row (schema v26) and is the foundation for any
// later ranking/decay policy; nothing else changes about the answer.
func TestEvidenceReinforcesAccess(t *testing.T) {
	svc, _ := setupService(t)
	projectID, _, pageIDs := buildGateCorpus(t, svc)

	before := map[int64]int64{}
	for _, id := range pageIDs {
		var c int64
		if err := svc.db.QueryRow(`SELECT COALESCE(access_count, 0) FROM wiki_pages WHERE id = ?`, id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		before[id] = c
	}

	svc.GatherEvidence(projectID, "payment gateway retry idempotency backoff", EvidenceBudget{})

	touched := 0
	for _, id := range pageIDs {
		var c int64
		if err := svc.db.QueryRow(`SELECT COALESCE(access_count, 0) FROM wiki_pages WHERE id = ?`, id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		if c == before[id]+1 {
			touched++
		} else if c != before[id] {
			t.Fatalf("page %d access_count jumped from %d to %d; want +1 or +0", id, before[id], c)
		}
	}
	if touched == 0 {
		t.Error("no page was reinforced by evidence admission; the access signal is dead")
	}
}

// Evidence attribution covers the synthetic buckets, too.
//
// GatherEvidence names every project kind, including the seeded 未分类 and
// import-placeholder landing projects. That is deliberate and easy to "fix"
// the wrong way: ADR-0023 keeps those buckets visible on desktop surfaces, and a
// note filed in one is still the user's knowledge. Filtering the name lookup by
// project_kind would blank out ProjectName exactly where attribution matters
// most (an orphaned import), turning a diagnosable card into an anonymous one.
// The kind filter belongs to resolution/listing surfaces (filterRealProjects),
// not to display attribution — this test is what keeps that boundary from
// quietly moving.
func TestEvidenceProjectNamesCoverSyntheticBuckets(t *testing.T) {
	svc, _ := setupService(t)

	phID, err := db.EnsureImportPlaceholder(svc.db, "/code/ghost-repo", "ghost-repo")
	if err != nil {
		t.Fatal(err)
	}
	var defaultID int64
	if err := svc.db.QueryRow(`SELECT id FROM projects WHERE project_kind = 'default'`).Scan(&defaultID); err != nil {
		t.Fatal(err)
	}

	buckets := []struct {
		pid   int64
		title string
	}{
		{phID, "invoice idempotency retry policy ghost"},
		{defaultID, "invoice idempotency retry policy unfiled"},
	}
	for _, c := range buckets {
		if _, err := db.CreateNoteEx(svc.db, c.pid, c.title,
			"Idempotency keys and retry policy for invoices.", "", "knowledge", "manual"); err != nil {
			t.Fatal(err)
		}
	}

	ev := svc.GatherEvidence(0, "invoice idempotency retry policy", EvidenceBudget{})
	if len(ev.Items) == 0 {
		t.Fatal("no evidence at all; the fixture did not reach the retriever")
	}
	got := map[string]string{}
	for _, it := range ev.Items {
		got[it.Title] = it.ProjectName
	}
	want := map[string]string{
		"invoice idempotency retry policy ghost":   "ghost-repo",
		"invoice idempotency retry policy unfiled": domain.DefaultProjectName,
	}
	for title, wantName := range want {
		name, ok := got[title]
		if !ok {
			t.Errorf("note %q did not reach evidence", title)
			continue
		}
		if name != wantName {
			t.Errorf("note %q project name = %q, want %q (bucket must stay attributable)", title, name, wantName)
		}
	}
}

// A project-name lookup failure costs the card its label and nothing else: the
// answer still ships. The original code wrapped the whole block in
// `if err == nil`, so the failure was invisible; it must stay visible in the log
// while remaining non-fatal on the return path.
func TestGatherEvidenceSurvivesProjectLookupFailure(t *testing.T) {
	svc, _ := setupService(t)
	projectID, _, _ := buildGateCorpus(t, svc)
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	ev := svc.GatherEvidence(projectID, "release checklist migration dry run rollback", EvidenceBudget{})
	if ev == nil {
		t.Fatal("GatherEvidence returned nil on a closed db; it must degrade, not break")
	}
}

// --- ADR-0040: the unscoped ask, and handoff records as evidence -------------

// handoffCorpus seeds one handoff record. The tag is what makes it a handoff
// (the same predicate the memory ring and abeval use), so the fixture uses
// the same spelling.
func handoffCorpus(t *testing.T, svc *Service, projectID int64, title, content string) int64 {
	t.Helper()
	note, err := db.CreateNoteEx(svc.db, projectID, title, content, "handoff", "knowledge", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	return note.ID
}

// The unscoped ask used to carry no context at all: the assembly was skipped
// when projectID was 0, so the model received the retrieved snippets and
// nothing else. It could not answer "which projects do I have" because it had
// never been told.
func TestEvidenceMessages_UnscopedCarriesContext(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "asked-proj", "/tmp/asked-proj")
	if _, err := svc.db.Exec("INSERT INTO repositories (path, project_id) VALUES (?, ?)",
		"/tmp/asked-proj/repo", pid); err != nil {
		t.Fatal(err)
	}

	msgs := svc.evidenceMessages(0, "我有哪些项目", "EVIDENCE-BLOCK")
	if len(msgs) != 2 {
		t.Fatalf("expected a system message plus the question, got %d messages", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected the first message to be the system context, got %q", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "asked-proj") {
		t.Errorf("the unscoped context does not mention the project: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "EVIDENCE-BLOCK") {
		t.Errorf("the evidence block was dropped: %q", msgs[0].Content)
	}
}

// "昨天提交了多少代码" was answered by pasting the retrieved note cards back at
// the user: the prompt carried no date (so "yesterday" was unresolvable) and
// never said the query tools exist (so the model treated the evidence block as
// the answer). The header must be present in every assembly shape, including
// the empty one — a library with no memory and no hits still has tools.
func TestEvidenceMessages_HeaderNamesDateAndTools(t *testing.T) {
	svc, git := setupService(t)
	registered := svc.toolRegistry.definitions()
	if len(registered) == 0 {
		t.Fatal("the registry is empty; the tool guide can never render")
	}
	for _, tc := range []struct {
		name      string
		projectID int64
		ev        string
	}{
		{"full", 0, "EVIDENCE"},
		{"base-only", 0, ""},
		{"ev-only", -1, "EVIDENCE"}, // no project, no memory: only the evidence block
		{"bare", -1, ""},            // nothing but the header must survive
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := svc.evidenceMessages(tc.projectID, "q", tc.ev)
			if len(msgs) != 2 || msgs[0].Role != "system" {
				t.Fatalf("messages = %+v", msgs)
			}
			sys := msgs[0].Content
			if !strings.Contains(sys, git.GetTodayDate()) {
				t.Errorf("no today's date in header: %q", sys)
			}
			// Every registered tool must appear with its description — the
			// guide and the tools field share one source of truth, so the
			// test pins the SOURCE, not example names that would drift.
			for _, td := range registered {
				if !strings.Contains(sys, td.Function.Name) || !strings.Contains(sys, td.Function.Description) {
					t.Errorf("tool %q missing from the guide", td.Function.Name)
				}
			}
			// The loop contract: call, result comes back, then continue.
			if !strings.Contains(sys, "result comes back") {
				t.Errorf("the prompt does not explain the call→result→continue loop: %q", sys)
			}
			// The old contradiction must not come back through the fallback.
			if strings.Contains(sys, "Use only the context below") {
				t.Errorf("the pre-tool sentence contradicts the tool guidance: %q", sys)
			}
		})
	}
}

// A handoff record is the previous session's exit record. It has to reach the
// model on its own channel: its title is English ("Session Handoff …") and
// its body is a fixed template, so keyword search in Chinese never finds it.
func TestGatherEvidence_HandoffEntersEvidence(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "handoff-proj", "/tmp/handoff-proj")
	id := handoffCorpus(t, svc, pid, "Session Handoff 2026-10-26 18:34",
		"# Session Handoff\n\n## Summary\n把探针的目录解析修好了。\n")

	ev := svc.GatherEvidence(0, "最近有哪些会话交接记录", EvidenceBudget{})
	var found *EvidenceItem
	for i := range ev.Items {
		if ev.Items[i].ID == id {
			found = &ev.Items[i]
		}
	}
	if found == nil {
		t.Fatalf("the handoff record did not reach the evidence set: %+v", ev.Items)
	}
	if found.Kind != "handoff" {
		t.Errorf("expected kind=handoff, got %q", found.Kind)
	}
	if found.Ref == "" {
		t.Error("the handoff record is not citable (no ref)")
	}
	if !strings.Contains(found.Snippet, "探针") {
		t.Errorf("the handoff record was admitted without its content: %q", found.Snippet)
	}
}

// Scoped to a project, the handoff channel returns THAT project's handoffs
// and nobody else's.
func TestGatherEvidence_HandoffRespectsScope(t *testing.T) {
	svc, _ := setupService(t)
	alpha := seedProject(t, svc.db, "alpha", "/tmp/alpha")
	bravo := seedProject(t, svc.db, "bravo", "/tmp/bravo")
	alphaID := handoffCorpus(t, svc, alpha, "Session Handoff alpha", "# Session Handoff\n\n## Summary\nalpha 的交接。\n")
	bravoID := handoffCorpus(t, svc, bravo, "Session Handoff bravo", "# Session Handoff\n\n## Summary\nbravo 的交接。\n")

	ev := svc.GatherEvidence(alpha, "交接", EvidenceBudget{})
	seen := map[int64]bool{}
	for _, it := range ev.Items {
		seen[it.ID] = true
	}
	if !seen[alphaID] {
		t.Errorf("the scoped handoff is missing: %+v", ev.Items)
	}
	if seen[bravoID] {
		t.Errorf("a handoff from another project leaked into the scope: %+v", ev.Items)
	}
}

// The same record can be reached by both channels (a handoff whose text
// matches the query). It must appear once — a duplicated evidence item
// inflates the coverage claim without adding anything.
func TestGatherEvidence_HandoffIsNotDuplicated(t *testing.T) {
	svc, _ := setupService(t)
	pid := seedProject(t, svc.db, "dup-proj", "/tmp/dup-proj")
	id := handoffCorpus(t, svc, pid, "Session Handoff zephyrine",
		"# Session Handoff\n\n## Summary\nzephyrine 通道的交接记录。\n")

	ev := svc.GatherEvidence(pid, "zephyrine", EvidenceBudget{})
	count := 0
	for _, it := range ev.Items {
		if it.ID == id {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected the handoff exactly once, got %d copies: %+v", count, ev.Items)
	}
}
