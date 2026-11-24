package wikigen

import (
	"strings"

	"repotory/internal/db"
)

// Compile triage — ADR-0065 gap 2.6, the Cinder shape of "small-model routing".
//
// Hindsight routes ingest through a decision model (0.8B) before the expensive
// pass; Cinder has one AI endpoint and a zero-model path it will not give up,
// so an extra model call per note would cost MORE than the compile it saves.
// The same job is done here with the structural signals the importers already
// write: kind=log is a transcript, and the rejection ledger (2026-10-20, 10 of
// 12 pending rejected) shows almost every rejected draft came from exactly
// those notes — Gemini session restatements, CodeBuddy import packaging, bare
// source attributions. Stopping them before the model call fixes both cost and
// the 40% rejection rate; telling the model "output [] for logs" (the prompt
// rule in askCompilePlan) never could, because the call was already paid for.
//
// This is a FILTER, not a verdict: it only decides which notes reach the
// compiler, never what a page says or whether a page is approved. Kill switch
// wiki_compile_triage=0 restores the old "send everything" behaviour.

// transcriptTitleMarks are the exact title shapes the session importers emit.
// Matching on the leading marker rather than a substring anywhere in the body
// avoids refusing a genuine note that merely quotes an environment block.
var transcriptTitleMarks = []string{
	"<environment_context>",        // codebuddy / generic session logs
	"# agents.md instructions for", // instruction-file snapshots, not knowledge
	"agents.md instructions for",   // the same shape without a leading '#'
	"session handoff",              // capture output: provenance, not a concept
}

// decisionMarks are the signals that a transcript carries a stable decision —
// the one case where a log note is worth compiling (1 of 94 in the live library
// on 2026-11-24). Presence of any mark overrides the kind=log skip.
var decisionMarks = []string{
	"决策:", "决策：", "决定:", "决定：", "规则:", "规则：",
	"convention:", "decision:", "adr-", "架构决策", "设计决策",
}

// CompileTriage decides whether a note is worth a compile call. The returned
// reason is displayed in the job note so a skipped note is never a silent
// partial success — the same discipline as the run-level budget stop.
func CompileTriage(note *db.Note) (bool, string) {
	if note == nil {
		return false, "空笔记"
	}
	title := strings.ToLower(strings.TrimSpace(note.Title))
	body := strings.TrimSpace(note.Content)

	for _, mark := range transcriptTitleMarks {
		if strings.HasPrefix(title, mark) {
			// A transcript that states a decision is the exception worth paying
			// for — check the body before refusing the whole kind.
			if hasDecisionMark(title + " " + body) {
				return true, ""
			}
			return false, "会话流水账（标题匹配，无稳定决策）"
		}
	}
	if note.Kind == "log" {
		if hasDecisionMark(title + " " + body) {
			return true, ""
		}
		return false, "日志类笔记（kind=log，无稳定决策）"
	}
	// No body-length rule: the rejection ledger points at transcripts, not at
	// short notes, and ADR-0071 already measured how a length/kind heuristic
	// demotes genuine answers along with the noise. Only the two signals with
	// live-library evidence (title shape, kind=log) gate a note here.
	return true, ""
}

func hasDecisionMark(s string) bool {
	lower := strings.ToLower(s)
	for _, mark := range decisionMarks {
		if strings.Contains(lower, mark) {
			return true
		}
	}
	return false
}

// TriageNotes filters transcript notes BEFORE CompileNoteTargets takes its
// head. That order matters: if the newest 20 notes are all session logs,
// truncating first would spend the whole run's budget on notes the filter
// would then refuse. Returns the kept notes and the number skipped.
// Disabled is deliberately a pass-through so the kill switch is exact.
func TriageNotes(notes []db.Note, enabled bool) (kept []db.Note, skipped int) {
	if !enabled {
		return notes, 0
	}
	kept = make([]db.Note, 0, len(notes))
	for i := range notes {
		ok, _ := CompileTriage(&notes[i])
		if ok {
			kept = append(kept, notes[i])
		} else {
			skipped++
		}
	}
	return kept, skipped
}
