package service

import (
	"encoding/json"
	"fmt"

	pluginruntime "repotory/internal/core/plugin/runtime"
	"repotory/internal/importers/claude"
	"repotory/internal/importers/codebuddy"
	"repotory/internal/importers/codex"
	"repotory/internal/importers/cursor"
	"repotory/internal/importers/hermes"
	"repotory/internal/importers/openclaw"
	"repotory/internal/importers/opencode"
)

// jsonUnmarshal decodes data into v, returning whether it succeeded. Cached
// repo_meta payloads may be legacy or empty; failures are non-fatal.
func jsonUnmarshal(data string, v any) bool {
	return json.Unmarshal([]byte(data), v) == nil
}

// GetKnowledgeSources returns registered knowledge importers with their state.
func (s *Service) GetKnowledgeSources() []pluginruntime.SourceStatus {
	if s.rt == nil {
		return []pluginruntime.SourceStatus{}
	}
	return s.rt.SourceStatuses()
}

// emitImportEvent forwards an import result to the registered UI handler
// (which forwards it to the frontend).
func (s *Service) emitImportEvent(name string, run pluginruntime.ImportRun, err error) {
	if s.onImportEvent != nil {
		payload := ImportEventPayload{"source": name, "created": run.Created, "updated": run.Updated, "skipped": run.Skipped}
		if err != nil {
			payload["error"] = err.Error()
		}
		s.onImportEvent(payload)
	}
}

// TriggerKnowledgeImport runs the knowledge source registered under name and
// returns the import statistics.
func (s *Service) TriggerKnowledgeImport(name string) (pluginruntime.ImportRun, error) {
	if s.rt == nil {
		return pluginruntime.ImportRun{}, fmt.Errorf("plugin runtime not initialized")
	}
	run, err := s.rt.TriggerImport(name)
	s.emitImportEvent(name, run, err)
	return run, err
}

// PreviewKnowledgeImport runs one knowledge source in read-only preview mode:
// it reports which notes would be created or updated without writing anything
// (ADR-0011 决策 4 dry-run). Unlike the trigger it does not emit an import
// event — nothing was imported.
func (s *Service) PreviewKnowledgeImport(name string) (pluginruntime.ImportPreview, error) {
	if s.rt == nil {
		return pluginruntime.ImportPreview{}, fmt.Errorf("plugin runtime not initialized")
	}
	return s.rt.PreviewImport(name)
}

// TriggerAllKnowledgeImports runs every registered knowledge source and emits
// an import.completed event for each, so the UI can surface per-source results.
func (s *Service) TriggerAllKnowledgeImports() []pluginruntime.SourceRun {
	if s.rt == nil {
		return []pluginruntime.SourceRun{}
	}
	results := s.rt.ImportAll()
	for _, r := range results {
		var err error
		if r.Err != "" {
			err = fmt.Errorf("%s", r.Err)
		}
		s.emitImportEvent(r.Name, r.Run, err)
	}
	return results
}

// registerBuiltinImporters registers the built-in knowledge importers with the
// plugin runtime. Sources are registered with different auto-import policies:
//   - claude:   AUTO — it reads curated ~/.claude/.../memory/*.md the user keeps.
//   - codex:    MANUAL — raw ~/.codex session transcripts, excluded from the
//     startup auto-import; only runs when triggered explicitly by name.
//   - opencode: MANUAL — ~/.local/share/opencode session summaries, same gate.
//   - openclaw: MANUAL — agent-GLOBAL ~/.openclaw-autoclaw/workspace/*.md,
//     allowlisted to that dir (parent holds keys/vault); attaches to the
//     `openclaw_project` config-named project (unset -> skipped).
//   - hermes:   MANUAL — agent-GLOBAL ~/.hermes/memories/*.md (Nous Research,
//     a product separate from OpenClaw), allowlisted to memories/; `hermes_project`.
//   - cursor:   MANUAL — Cursor session transcripts from its globalStorage SQLite
//     state DB, read-only. BEST EFFORT by design (undocumented, versioned format):
//     an unrecognized layout fails the source loudly instead of importing zero
//     notes quietly. Sessions are attributed through workspaceStorage folder
//     lookup, so an unmatched workspace skips rather than guessing.
//   - codebuddy: MANUAL — CodeBuddy session transcripts under
//     CodeBuddyExtension/Data/<uid>/<IDE>/history/*/index.json +
//     messages/*.json, attributed per-session via the first-user message's
//     `Workspace Folder:` line. Two separate sources on purpose (ADR-0011 决策 4):
//     codebuddy-memory covers the curated agent-GLOBAL memory
//     (~/.codebuddy/memery/*.md + Public/.memories/*.mdc) and attaches to the
//     `codebuddy_project` config-named project (unset -> skipped).
//
// Manual sources still appear in GetKnowledgeSources, so each stays one-click
// triggerable. ADR-0011 决策 4.
func (s *Service) registerBuiltinImporters() {
	s.rt.RegisterSource(claude.SourceName, claude.New(s.db))
	s.rt.RegisterSourceManual(codex.SourceName, codex.New(s.db))
	s.rt.RegisterSourceManual(opencode.SourceName, opencode.New(s.db))
	s.rt.RegisterSourceManual(openclaw.SourceName, openclaw.New(s.db))
	s.rt.RegisterSourceManual(hermes.SourceName, hermes.New(s.db))
	s.rt.RegisterSourceManual(cursor.SourceName, cursor.New(s.db))
	s.rt.RegisterSourceManual(codebuddy.SourceName, codebuddy.New(s.db))
	s.rt.RegisterSourceManual(codebuddy.MemorySourceName, codebuddy.NewMemory(s.db))
}

// ImportResult summarizes a Claude memory import run.
type ImportResult struct {
	Synced  int `json:"synced"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

// ImportClaudeMemory imports notes from Claude's per-project memory directory
// (~/.claude/projects/*/memory/*.md) into Cinder, matching each to a project
// by name or repository path. The import is delegated to the built-in Claude
// KnowledgeImporter through the plugin runtime, so it is idempotent and shares
// the same upsert and statistics path as the other knowledge sources (issue #35).
func (s *Service) ImportClaudeMemory() (*ImportResult, error) {
	if s.rt == nil {
		return &ImportResult{}, nil
	}
	run, err := s.rt.TriggerImport(claude.SourceName)
	if err != nil {
		return &ImportResult{}, err
	}
	return &ImportResult{
		Synced:  run.Created,
		Updated: run.Updated,
		Skipped: run.Skipped,
	}, nil
}
