package service

import (
	"context"
	"log"
	"time"

	"repotory/internal/service/wikigen"
	"repotory/internal/service/wikilint"
)

// Wiki lint (ADR-0014 决策 5 / TODO M6-W4). The rules moved to
// internal/service/wikilint (ADR-0060 step four): the four structural checks and
// the model-backed half are the pass's policy, and none of them needs anything
// from the Service but the database and a chat seam. What stays here is the
// scheduling side — the ticker below and the lint job queue in
// wiki_compile_job.go — plus the facade the ticker, the job worker and the
// bindings all call by its old name.

// WikiLintReport is an alias, not a copy: its json tags cross the RPC bridge to
// the browser and the Wails binding to the frontend.
type WikiLintReport = wikilint.WikiLintReport

// WikiFinding is an alias for the finding type the write side owns; this package
// still names it where a facade returns findings (suggestMerges).
type WikiFinding = wikigen.WikiFinding

// Compat aliases for the vocabulary the lint rules did not take with them: the
// gate key and the machine-generated todo marker are still named here by the
// gate wrapper (config.go) and by the lint tests. Const aliases cannot drift.
const (
	wikiLintLLMKey = wikigen.KeyLintLLM
	lintTodoPrefix = wikigen.LintTodoPrefix
	// wikiLintInterval: lint is maintenance, not a live feature, so the scheduled
	// pass is deliberately slow.
	wikiLintInterval = 6 * time.Hour
)

// startWikiLintTicker wires the scheduled half of ADR-0014 决策 5 ("定时与手动触发
// 各一条路径"). Background-tracked, so Shutdown waits for it.
//
// The first tick lands a full interval later rather than at startup: filing todos
// into the user's project list as a side effect of opening the app would be an
// unasked-for write, and nothing about lint is urgent.
func (s *Service) startWikiLintTicker() {
	s.bgGo("wiki-lint", func(ctx context.Context) {
		t := time.NewTicker(wikiLintInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := s.RunWikiLint(0, false); err != nil {
					log.Printf("wiki lint (scheduled): %v", err)
				}
			}
		}
	})
}

// RunWikiLint lints projectID (0 = every page) and files findings as todos.
// wantLLM asks for the model-backed checks too; they still need their own config
// gate, so this is permission to consider, not an override.
func (s *Service) RunWikiLint(projectID int64, wantLLM bool) (*WikiLintReport, error) {
	return wikilint.Run(context.Background(), s.db, wikigenChat{s}, projectID, wantLLM)
}
