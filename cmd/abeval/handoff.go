// handoff.go — the cross-session memory gate (TODO 2.1 度量口径).
//
// The question this gate answers is the one the external review called the
// only hard PMF signal there is: "can it remember last week's gotcha across
// sessions?" Until now the answer was a qualitative claim — "the handoff
// lands first" — with nothing anyone could re-run. This file is that
// repeatable 口径.
//
// Two measurements, kept apart because they answer different questions:
//
//	A. CONTEXT LEAD RATE (constructive) — per project that has handoffs:
//	   render the context exactly the way an agent gets it (svc.ResolveProject
//	   → svc.BuildProjectContext) and check whether the FIRST entry under
//	   "## Knowledge Notes" is the newest handoff. This is the literal
//	   "repotory_context 首位命中率": "3/3 projects" is the number. It is a
//	   WIRING check, not a quality score: it can only say the exit record is
//	   where the next session looks first.
//
//	B. RETRIEVAL PROBE (production path) — per handoff: derive a short probe
//	   fragment from what was actually written (gotcha → decision → change →
//	   next step → summary) and run it through svc.SearchAll, the function
//	   every real query goes through (five frontend entry points, MCP
//	   repotory_notes_search / repotory_ask, HTTP /api/search?all=1). Reports
//	   hit@1 / recall@k / ndcg@k for the lexical and hybrid arms.
//
// The verdict covers A plus "nothing recorded is unreachable" in B. B's hit@1
// number is REPORTED, never gated on: with a personal corpus's handful of
// handoffs it measures reachability, not answer quality, and gating on it
// would dress a tautology up as a quality signal.
//
// Why the probe is a FRAGMENT and not the whole bullet: a later session asks
// with what it half-remembers, so the gate must ask with what it half-remembers
// too. A probe that copied the note verbatim would measure FTS against itself.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	"repotory/internal/db"
	"repotory/internal/platform"
	"repotory/internal/search/abeval"
	"repotory/internal/service"
)

// Probe length bounds. The upper bound keeps a query shaped like a real
// cross-session question ("那个幂等键的问题" / "the idempotency key problem")
// instead of a pasted bullet; the lower bound keeps the fragment long enough
// for the trigram tokenizer to produce a usable number of grams.
const (
	handoffProbeMaxRunes = 48
	handoffProbeMinRunes = 6
)

// handoffProbeSections is the order a probe is drawn from. A gotcha is what a
// later session most needs to recall, a decision is next, changes are less
// durable, and the summary is last because it is prose — prose makes the
// weakest probe.
var handoffProbeSections = []string{"Gotchas", "Decisions", "Changes", "Next Steps", "Summary"}

// handoffProbe is one derived retrieval case: a fragment of what was written,
// and the note it must find.
type handoffProbe struct {
	NoteID    int64
	ProjectID int64
	Query     string
	Section   string // the handoff section the probe came from
}

// runHandoffGate measures the cross-session memory layer and returns the
// process exit code.
func runHandoffGate(dbPath string, k int) int {
	path := dbPath
	if path == "" {
		path = platform.GetDbPath()
	}
	// Same discipline as the other gates: measure on a copy, never on the live
	// database. The hybrid arm rebuilds the vector index, which rewrites rows.
	cp, err := db.CopyForTooling(path, db.OpenCopyOptions{})
	if err != nil {
		log.Fatalf("abeval: copy db: %v", err)
	}
	defer func() { _ = cp.Close() }()
	database := cp.DB
	svc := service.New(database, "abeval")

	// semanticEnabled() reads app_config on EVERY search, so the arms must flip
	// the key immediately before searching. The value is restored on exit,
	// including on SIGINT/SIGTERM, so a gate run never leaves the copy with the
	// semantic switch flipped.
	original, _ := db.GetConfig(database, "semantic_search")
	restore := func() {
		if original == "" {
			_ = db.DeleteConfig(database, "semantic_search")
			return
		}
		_ = db.SetConfig(database, "semantic_search", original)
	}
	if err := restoreOnSignal(restore); err != nil {
		log.Printf("abeval: signal restore not installed: %v", err)
	}

	notes, err := db.ListRecentHandoffNotes(database, 0, 0)
	if err != nil {
		log.Fatalf("abeval: list handoff notes: %v", err)
	}
	if len(notes) == 0 {
		restore()
		fmt.Println("abeval: no handoff notes in this database — nothing has been recorded across sessions yet")
		fmt.Println("GATE SKIP: no handoff corpus to measure (record one handoff and re-run)")
		return 0
	}

	probes := deriveHandoffProbes(notes)
	fmt.Printf("handoff corpus: %d handoff note(s) across %d project(s)\n", len(notes), countProjects(notes))
	fmt.Printf("probes derived: %d (skipped %d: no usable section content)\n", len(probes), len(notes)-len(probes))

	// ---- B. retrieval probe, on the production search path -----------------
	lexical := func(q string) []int64 {
		_ = db.SetConfig(database, "semantic_search", "0")
		return noteIDs(svc.SearchAll(context.Background(), q))
	}
	hybridAvailable := true
	if err := db.SetConfig(database, "semantic_search", "1"); err != nil {
		log.Printf("abeval: could not enable semantic_search in the copy: %v", err)
	}
	switch n, err := svc.RebuildEmbeddings(); {
	case err != nil:
		hybridAvailable = false
		fmt.Printf("abeval: semantic rebuild unavailable (%v) — the hybrid arm equals the lexical arm\n", err)
	case n == 0:
		// Zero vectors is not a working hybrid arm: with no embedding to
		// recall, fuseSemantic degrades to the lexical list, so calling the
		// row "hybrid" would report a measurement that never happened.
		hybridAvailable = false
		fmt.Println("abeval: semantic rebuild embedded 0 notes — the hybrid arm equals the lexical arm")
	default:
		fmt.Printf("abeval: embedded %d notes for the hybrid arm\n", n)
	}
	hybrid := func(q string) []int64 {
		_ = db.SetConfig(database, "semantic_search", "1")
		return noteIDs(svc.SearchAll(context.Background(), q))
	}

	cases := make([]abeval.Case[int64], 0, len(probes))
	for _, p := range probes {
		cases = append(cases, abeval.Case[int64]{Query: p.Query, Relevant: []int64{p.NoteID}})
	}
	lexRet := make([]abeval.Retrieved[int64], 0, len(cases))
	hybRet := make([]abeval.Retrieved[int64], 0, len(cases))
	for _, c := range cases {
		lexRet = append(lexRet, abeval.Retrieved[int64]{Case: c, Ranks: lexical(c.Query)})
		hybRet = append(hybRet, abeval.Retrieved[int64]{Case: c, Ranks: hybrid(c.Query)})
	}
	lex := abeval.Score(lexRet, k)
	hyb := abeval.Score(hybRet, k)
	lexHit1 := abeval.Score(lexRet, 1).MeanRecall
	hybHit1 := abeval.Score(hybRet, 1).MeanRecall

	fmt.Println()
	fmt.Printf("%s: n=%d hit@1=%.3f recall@%d=%.3f ndcg@%d=%.3f\n",
		"lexical (production path)", lex.N, lexHit1, k, lex.MeanRecall, k, lex.MeanNDCG)
	fmt.Printf("%s: n=%d hit@1=%.3f recall@%d=%.3f ndcg@%d=%.3f\n",
		"hybrid  (production path)", hyb.N, hybHit1, k, hyb.MeanRecall, k, hyb.MeanNDCG)
	if !hybridAvailable {
		fmt.Println("note: the hybrid row above was measured with semantic search unavailable, so it repeats the lexical row")
	}
	reportProbeMisses(lexRet, k)

	// ---- A. context lead rate --------------------------------------------
	fmt.Println()
	leads := checkContextLeads(svc, database, notes)
	reportContextLeads(leads)

	restore()

	// The verdict. Reachability is checked on the LEXICAL arm because that is
	// the path every install actually runs (ADR-0010: semantic search is off
	// by default). If the always-on path cannot find what was recorded, the
	// memory layer is broken regardless of what the optional arm does.
	unreachable := unreachableProbes(lexRet, k)
	for _, p := range unreachable {
		fmt.Printf("GATE FAIL: handoff note %d is not reachable within top-%d of the lexical production path\n",
			p.NoteID, k)
	}
	for _, l := range leads {
		if l.Skipped {
			continue
		}
		if !l.OK {
			fmt.Printf("GATE FAIL: project %d (%s) does not lead its context with the newest handoff — it leads with %q, expected %q\n",
				l.ProjectID, l.ProjectName, l.LeadTitle, l.Expected)
		}
	}
	if len(unreachable) > 0 {
		return 1
	}
	for _, l := range leads {
		if !l.Skipped && !l.OK {
			return 1
		}
	}
	fmt.Printf("GATE PASS: %d/%d recorded handoff(s) reachable within top-%d, %d/%d project(s) lead their context with the newest handoff\n",
		len(probes)-len(unreachable), len(probes), k, countLeadOK(leads), countLeadChecked(leads))
	fmt.Printf("measured hit@1: lexical=%.3f hybrid=%.3f (reported, not gated — at this corpus size it measures reachability, not answer quality)\n",
		lexHit1, hybHit1)
	return 0
}

// deriveHandoffProbes turns handoff notes into retrieval probes. A note with
// no usable section content yields no probe — a probe built from an empty
// string would query the retriever for nothing and score a miss that says
// nothing about the memory layer.
func deriveHandoffProbes(notes []db.NoteWithProject) []handoffProbe {
	out := make([]handoffProbe, 0, len(notes))
	for _, n := range notes {
		if p, ok := deriveHandoffProbe(n); ok {
			out = append(out, p)
		}
	}
	return out
}

// deriveHandoffProbe builds one probe from a handoff note: the first
// meaningful line of the first section that carries content, truncated to a
// query-shaped length.
func deriveHandoffProbe(note db.NoteWithProject) (handoffProbe, bool) {
	sections := parseHandoffSections(note.Content)
	for _, name := range handoffProbeSections {
		item := firstSectionItem(sections[name])
		if item == "" {
			continue
		}
		probe := truncateProbe(item, handoffProbeMaxRunes)
		if len([]rune(probe)) < handoffProbeMinRunes {
			continue
		}
		return handoffProbe{
			NoteID:    note.ID,
			ProjectID: note.ProjectID,
			Query:     probe,
			Section:   name,
		}, true
	}
	return handoffProbe{}, false
}

// parseHandoffSections splits the canonical handoff Markdown into its
// sections. The template writes "## <Name>" headings with the body
// underneath; a heading the template never writes is kept as-is, so an
// auto-captured note with a different shape still parses.
func parseHandoffSections(content string) map[string]string {
	out := map[string]string{}
	var current string
	var body []string
	flush := func() {
		if current != "" {
			out[current] = strings.TrimSpace(strings.Join(body, "\n"))
		}
	}
	for _, line := range strings.Split(content, "\n") {
		// "### " does not match "## " — the third character is '#', not a space.
		if strings.HasPrefix(line, "## ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			body = nil
			continue
		}
		if current != "" {
			body = append(body, line)
		}
	}
	flush()
	return out
}

// firstSectionItem returns the first meaningful line of a handoff section: a
// bullet's text for list sections, the first line of the prose Summary.
func firstSectionItem(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		}
		if line == "" {
			continue
		}
		return line
	}
	return ""
}

// truncateProbe cuts a fragment to a query-shaped length. Latin text is cut at
// a word boundary so the probe does not end mid-word; CJK has no spaces, so
// it is cut at a rune boundary (never mid-character).
func truncateProbe(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return strings.TrimSpace(s)
	}
	cut := string(r[:maxRunes])
	if i := strings.LastIndexAny(cut, " \t,;:，、；："); i > 0 {
		cut = strings.TrimSpace(cut[:i])
	}
	return cut
}

// contextLead records whether one project's context opens with its newest
// handoff.
type contextLead struct {
	ProjectID   int64
	ProjectName string
	LeadTitle   string
	Expected    string
	OK          bool
	Skipped     bool
	Reason      string
}

// checkContextLeads renders every project that owns a handoff and checks what
// its context opens with.
func checkContextLeads(svc *service.Service, database *sql.DB, notes []db.NoteWithProject) []contextLead {
	seen := map[int64]bool{}
	var out []contextLead
	for _, n := range notes {
		if seen[n.ProjectID] {
			continue
		}
		seen[n.ProjectID] = true
		out = append(out, checkContextLead(svc, database, n.ProjectID, n.ProjectName))
	}
	return out
}

// checkContextLead checks one project. The context is produced through the
// production path (ResolveProject → BuildProjectContext), not by re-implementing
// the ordering here — the point is to measure what an agent would receive.
func checkContextLead(svc *service.Service, database *sql.DB, projectID int64, name string) contextLead {
	lead := contextLead{ProjectID: projectID, ProjectName: name}

	// A project the scanner never backed has no context to build, so it is
	// skipped rather than failed: "no context" is a different condition from
	// "context that forgets the handoff".
	res := svc.ResolveProject(strconv.FormatInt(projectID, 10))
	if res == nil || res.Project == nil {
		lead.Skipped = true
		lead.Reason = "not a scanner-backed project — the context path has no entry for it"
		return lead
	}
	latest, err := db.LatestHandoffNote(database, projectID)
	if err != nil {
		lead.Skipped = true
		lead.Reason = "no handoff note to lead with"
		return lead
	}
	lead.Expected = latest.Title

	ctx := svc.BuildProjectContext(res)
	lead.LeadTitle = firstContextNoteTitle(ctx)
	lead.OK = lead.LeadTitle == lead.Expected

	// The lead must also carry the RECORD, not just the title: a context that
	// names the newest handoff but drops its body leaves the next session
	// with a label and no exit record.
	if fragment := handoffContentFragment(latest.Content); fragment != "" && !strings.Contains(ctx, fragment) {
		lead.OK = false
		if lead.LeadTitle == lead.Expected {
			lead.LeadTitle = lead.LeadTitle + " (body missing from the context)"
		}
	}
	return lead
}

// firstContextNoteTitle returns the title of the first note rendered under
// "## Knowledge Notes" in a context document.
func firstContextNoteTitle(ctx string) string {
	marker := "## Knowledge Notes"
	idx := strings.Index(ctx, marker)
	if idx < 0 {
		return ""
	}
	for _, line := range strings.Split(ctx[idx+len(marker):], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "### ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "### "))
		}
	}
	return ""
}

// handoffContentFragment returns a distinctive slice of a handoff's body —
// the first line of its most informative section, clipped — to check that the
// context actually carries the record and not just its title.
func handoffContentFragment(content string) string {
	sections := parseHandoffSections(content)
	for _, name := range handoffProbeSections {
		if item := firstSectionItem(sections[name]); item != "" {
			return truncateProbe(item, 60)
		}
	}
	return ""
}

// unreachableProbes lists the probes whose handoff did not make the top-k of
// the given retrieval.
func unreachableProbes(ret []abeval.Retrieved[int64], k int) []handoffProbe {
	var out []handoffProbe
	for _, r := range ret {
		if abeval.RecallAtK(r.Ranks, r.Case.Relevant, k) < 1 {
			out = append(out, handoffProbe{NoteID: r.Case.Relevant[0], Query: r.Case.Query})
		}
	}
	return out
}

// reportProbeMisses prints the probes that did not land within the cutoff, so
// a reader can see WHICH memory went missing instead of only the aggregate.
func reportProbeMisses(ret []abeval.Retrieved[int64], k int) {
	misses := 0
	for _, r := range ret {
		if abeval.RecallAtK(r.Ranks, r.Case.Relevant, k) >= 1 {
			continue
		}
		misses++
		fmt.Printf("miss (lexical): handoff note %d — %q not in top-%d\n",
			r.Case.Relevant[0], truncateProbe(r.Case.Query, 40), k)
	}
	if misses == 0 {
		fmt.Println("reachability: every recorded handoff is findable within the cutoff")
	}
}

// reportContextLeads prints the per-project lead result — the 首位命中率 in
// its most legible form.
func reportContextLeads(leads []contextLead) {
	ok, skipped := 0, 0
	for _, l := range leads {
		switch {
		case l.Skipped:
			skipped++
			fmt.Printf("context lead: project %d (%s) — skipped: %s\n", l.ProjectID, l.ProjectName, l.Reason)
		case l.OK:
			ok++
			fmt.Printf("context lead: project %d (%s) — %q\n", l.ProjectID, l.ProjectName, l.LeadTitle)
		default:
			fmt.Printf("context lead: project %d (%s) — %q (expected %q)\n",
				l.ProjectID, l.ProjectName, l.LeadTitle, l.Expected)
		}
	}
	checked := len(leads) - skipped
	if checked > 0 {
		fmt.Printf("context lead rate: %d/%d project(s) open with their newest handoff (%.0f%%)\n",
			ok, checked, 100*float64(ok)/float64(checked))
	} else {
		fmt.Println("context lead rate: no project could be checked (none is scanner-backed)")
	}
}

// countProjects counts the distinct projects the handoff notes belong to.
func countProjects(notes []db.NoteWithProject) int {
	seen := map[int64]bool{}
	for _, n := range notes {
		seen[n.ProjectID] = true
	}
	return len(seen)
}

// countLeadOK counts the projects whose context opens with their newest
// handoff.
func countLeadOK(leads []contextLead) int {
	n := 0
	for _, l := range leads {
		if !l.Skipped && l.OK {
			n++
		}
	}
	return n
}

// countLeadChecked counts the projects the lead check could actually judge.
func countLeadChecked(leads []contextLead) int {
	n := 0
	for _, l := range leads {
		if !l.Skipped {
			n++
		}
	}
	return n
}
