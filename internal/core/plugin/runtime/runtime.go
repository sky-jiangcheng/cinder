// Package runtime implements the knowledge-source runtime for Cinder.
//
// There are no user-authored plugins: the yaegi script-plugin runtime was
// removed (C11 方案B, amending ADR-0002). What lives here is the registry the
// built-in knowledge importers register into (RegisterSource /
// RegisterSourceManual), the trigger/upsert path (TriggerImport -> upsertDoc)
// and the startup auto-import pass (ImportAll). The eight built-in importers
// (claude, codex, opencode, openclaw, hermes, cursor, codebuddy,
// codebuddy-memory) are the only tenants — see service.registerBuiltinImporters
// and docs/plugins/overview.md.
package runtime

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"repotory/internal/core/plugin"
	"repotory/internal/db"
)

// SourceType classifies a knowledge source for display in the settings page.
// It is derived from the source name (sourceTypeFor) rather than stored:
// importers declare their artifact shape, and the UI only needs a stable label
// plus the four buckets below. Unknown names fall back to Manual so a future
// or test-only source is never hidden behind an empty label.
type SourceType string

const (
	// SourceTypeAgentImport is an agent's curated long-term memory files
	// (claude ~/.claude/**/memory/*.md, openclaw workspace/*.md, hermes
	// memories/*.md, codebuddy-memory curated memory) imported as knowledge.
	SourceTypeAgentImport SourceType = "agent-import"
	// SourceTypeConversation is a session/transcript feed (codex, opencode,
	// cursor, codebuddy session history) turned into compact notes.
	SourceTypeConversation SourceType = "user-conversation"
	// SourceTypeProjectExtract is a source that mines knowledge out of the
	// project itself (no built-in importer uses it yet; kept as a bucket).
	SourceTypeProjectExtract SourceType = "project-extract"
	// SourceTypeManual is the fallback for unknown or unclassified sources.
	SourceTypeManual SourceType = "manual"
)

// SourceTypeFor derives the display bucket from a source name. The mapping
// mirrors each importer's artifact shape (see internal/importers/*/).
//
// Exported because ADR-0022 决策 4 makes this the SINGLE server-side
// derivation point for the display bucket: the settings page chip and the
// note card badge must agree, and a frontend copy of this switch would let
// the two drift. The service layer calls it when decorating notes with their
// derived type.
func SourceTypeFor(name string) SourceType {
	switch name {
	case "claude", "openclaw", "hermes", "codebuddy-memory":
		return SourceTypeAgentImport
	case "codex", "opencode", "cursor", "codebuddy":
		return SourceTypeConversation
	default:
		return SourceTypeManual
	}
}

// SourceStatus describes a registered knowledge source for the settings page.
// Detected/Detail come from the source's optional probe (did the agent's data
// actually show up on this machine); Enabled keeps its original meaning (did
// the last import succeed); Type is the derived display bucket. ADR-0022 决策 1.
//
// LastRun/LastError are PER SOURCE (ADR-0022 决策 3 asks for them here, not
// only at the aggregate round level): they record the most recent import
// attempt of THIS source, whether it came from the periodic ticker, the
// settings-page button, or the startup pass. The global round status stays
// for "did the last round have a problem"; these answer "which source".
type SourceStatus struct {
	Name      string     `json:"name"`
	Plugin    string     `json:"plugin"`
	Detected  bool       `json:"detected"`
	Enabled   bool       `json:"enabled"`
	Detail    string     `json:"detail"`
	Type      SourceType `json:"type"`
	LastRun   string     `json:"last_run,omitempty"`   // RFC3339; empty when never run
	LastError string     `json:"last_error,omitempty"` // empty when the last run was clean
	// Failed counts the documents the last run REFUSED to produce (a file it
	// could not read or parse). Zero with an empty LastError means the source
	// is simply idle; a non-zero Failed with an empty LastError means the
	// importer is broken in a way that does not raise — the case that used to
	// be invisible.
	Failed int `json:"failed"`
	// Errors carries a bounded sample of the rejection reasons.
	Errors []string `json:"errors,omitempty"`
}

// sourceEntry is a registered knowledge importer with its latest run result.
type sourceEntry struct {
	name     string
	plugin   string
	importFn func() ([]plugin.ImportDoc, error)
	probeFn  func() plugin.ProbeResult
	// rejectionsFn reports documents the importer refused to produce. Nil when
	// the importer does not implement plugin.RejectionReporter.
	rejectionsFn func() []plugin.Rejection
	imported     int
	lastErr      error
	// lastFailed / lastErrors are what the LAST run refused to produce. They
	// are what the settings page shows as "could not read N files", the
	// difference between "nothing new to import" and "the importer is broken".
	lastFailed int
	lastErrors []string
	// lastRun is when this source was last IMPORT ATTEMPTED (RFC3339, empty
	// when never). It is per source rather than per round, so the settings
	// page can say "hermes synced 2 minutes ago, codex failed 1 hour ago"
	// instead of one aggregate timestamp for the whole set.
	lastRun string
	// auto marks sources eligible for the startup "import everything" pass
	// (ImportAll). Curated sources (claude memory) are auto; privacy-sensitive
	// transcript sources (codex, opencode, cursor, ...) are opt-in and only run
	// when triggered explicitly by name. See ADR-0011 决策 4.
	auto bool
}

// Runtime supervises the registered knowledge sources. It is safe for
// concurrent use.
type Runtime struct {
	mu      sync.Mutex
	db      *sql.DB
	sources map[string]*sourceEntry
	// importMu serializes TriggerImport so concurrent invocations of the same
	// source (e.g. UI button + startup auto-import) cannot double-import.
	importMu sync.Mutex
}

// New creates a Runtime bound to the given database handle.
func New(database *sql.DB) *Runtime {
	return &Runtime{
		db:      database,
		sources: make(map[string]*sourceEntry),
	}
}

// SourceStatuses returns the registered knowledge sources with their state.
// Presence (Detected/Detail) is checked live on every call: probes are cheap
// stat/readdir checks, and a fresh answer matches what the user has actually
// installed rather than what was present at startup.
func (r *Runtime) SourceStatuses() []SourceStatus {
	r.mu.Lock()
	type snapshot struct {
		name       string
		plugin     string
		enabled    bool
		lastRun    string
		lastErr    error
		lastFailed int
		lastErrors []string
		probeFn    func() plugin.ProbeResult
	}
	entries := make([]snapshot, 0, len(r.sources))
	for _, s := range r.sources {
		entries = append(entries, snapshot{
			name:       s.name,
			plugin:     s.plugin,
			enabled:    s.lastErr == nil,
			lastRun:    s.lastRun,
			lastErr:    s.lastErr,
			lastFailed: s.lastFailed,
			lastErrors: s.lastErrors,
			probeFn:    s.probeFn,
		})
	}
	r.mu.Unlock()

	out := make([]SourceStatus, 0, len(entries))
	for _, e := range entries {
		st := SourceStatus{
			Name:      e.name,
			Plugin:    e.plugin,
			Enabled:   e.enabled,
			Type:      SourceTypeFor(e.name),
			LastRun:   e.lastRun,
			LastError: errString(e.lastErr),
			Failed:    e.lastFailed,
			Errors:    e.lastErrors,
		}
		if e.probeFn != nil {
			pr := safeProbe(e.probeFn)
			st.Detected = pr.Detected
			st.Detail = pr.Detail
		} else {
			// Sources without a probe (test doubles, future capabilities) are
			// still registered importers: treat them as present so the settings
			// page never hides an importable source just because it lacks a
			// detection hook.
			st.Detected = true
		}
		out = append(out, st)
	}
	return out
}

// safeProbe runs a source's presence check, recovering any panic so one broken
// probe can never take down the whole settings page.
func safeProbe(fn func() plugin.ProbeResult) (pr plugin.ProbeResult) {
	defer func() {
		if rec := recover(); rec != nil {
			pr = plugin.ProbeResult{Detail: "探活失败：" + fmt.Sprint(rec)}
		}
	}()
	return fn()
}

// errString renders an error for the status payload: empty when there is no
// error, so "the last run was clean" is an absent field rather than an empty
// string the UI has to special-case.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ImportRun summarizes a single knowledge import execution.
type ImportRun struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	// Failed counts documents the importer REFUSED to produce — a file it
	// could not read or parse. It is the difference between "there was
	// nothing to import" and "the importer is broken": without it, a source
	// whose format changed upstream reports a clean zero-import run.
	Failed int `json:"failed"`
	// Errors carries up to maxImportErrors human-readable rejection reasons.
	// Bounded because a broken source can reject thousands of files and the
	// report is rendered in a settings panel.
	Errors []string `json:"errors,omitempty"`
}

// maxImportErrors caps how many rejection reasons one import report carries.
// The COUNT (ImportRun.Failed) is never capped — only the sample of reasons.
const maxImportErrors = 10

// Total returns the number of documents processed.
func (r ImportRun) Total() int { return r.Created + r.Updated + r.Skipped }

// SourceRun pairs a knowledge source with its latest import run, returned by
// ImportAll for reporting on startup auto-imports.
type SourceRun struct {
	Name string    `json:"name"`
	Run  ImportRun `json:"run"`
	Err  string    `json:"error,omitempty"`
}

// ImportAll triggers every registered knowledge source and returns per-source
// results. Sources that fail are reported with Err set rather than aborting the
// remaining sources.
func (r *Runtime) ImportAll() []SourceRun {
	r.mu.Lock()
	names := make([]string, 0, len(r.sources))
	for name, src := range r.sources {
		// Skip opt-in sources (e.g. codex transcripts): the startup auto-import
		// pass runs curated sources only. Opt-in sources still run when
		// triggered explicitly by name via TriggerImport. ADR-0011 决策 4.
		if !src.auto {
			continue
		}
		names = append(names, name)
	}
	r.mu.Unlock()

	results := make([]SourceRun, 0, len(names))
	for _, name := range names {
		run, err := r.TriggerImport(name)
		sr := SourceRun{Name: name, Run: run}
		if err != nil {
			sr.Err = err.Error()
		}
		results = append(results, sr)
	}
	return results
}

// TriggerImport runs the knowledge source registered under name and upserts
// the returned documents into the knowledge base. Concurrent invocations are
// serialized via importMu to avoid duplicate imports.
func (r *Runtime) TriggerImport(name string) (ImportRun, error) {
	r.importMu.Lock()
	defer r.importMu.Unlock()

	r.mu.Lock()
	src := r.sources[name]
	r.mu.Unlock()
	if src == nil {
		return ImportRun{}, fmt.Errorf("plugin runtime: unknown knowledge source %q", name)
	}

	docs, err := r.safeImport(src)
	if err != nil {
		r.recordSourceError(src, err)
		return ImportRun{}, err
	}

	var run ImportRun
	var firstErr error
	for _, d := range docs {
		// A note that matched no project is LANDED, never dropped (ADR-0023):
		// onto a repo-derived placeholder project when the importer knew the repo
		// path, else onto the seeded default ("Uncategorized") project. Only a doc
		// that cannot be landed at all (a placeholder/default failure) is skipped.
		if d.ProjectID <= 0 {
			landed, lerr := r.landUnmatched(d)
			if lerr != nil {
				log.Printf("plugin runtime: import %q doc %q could not be landed: %v", name, d.Title, lerr)
				if firstErr == nil {
					firstErr = lerr
				}
				run.Skipped++
				continue
			}
			if landed <= 0 {
				run.Skipped++
				continue
			}
			d.ProjectID = landed
		}
		created, err := r.upsertDoc(d, src.name)
		if err != nil {
			log.Printf("plugin runtime: import %q doc %q failed: %v", name, d.Title, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if created {
			run.Created++
		} else {
			run.Updated++
		}
	}

	// Fold in what the importer refused to produce. This runs BEFORE the
	// summary log so the log line carries the failure count: a run that
	// rejected forty files and imported nothing must not look like a clean
	// zero-import run in the log either.
	if src.rejectionsFn != nil {
		for _, rej := range src.rejectionsFn() {
			run.Failed++
			if len(run.Errors) < maxImportErrors {
				msg := rej.Reason
				if rej.Path != "" {
					msg = rej.Path + ": " + rej.Reason
				}
				run.Errors = append(run.Errors, msg)
			}
		}
		if run.Failed > 0 {
			log.Printf("plugin runtime: import %q REJECTED %d document(s) it could not read (first: %v)",
				name, run.Failed, run.Errors)
		}
	} else {
		// No reporter: a clean run must not inherit the previous run's
		// failures, or a fixed importer would keep showing the old breakage.
		run.Failed = 0
		run.Errors = nil
	}

	r.mu.Lock()
	src.imported = run.Total()
	src.lastErr = firstErr
	src.lastFailed = run.Failed
	src.lastErrors = run.Errors
	src.lastRun = time.Now().Format(time.RFC3339)
	r.mu.Unlock()

	log.Printf("plugin runtime: import %q -> created %d, updated %d, skipped %d, failed %d",
		name, run.Created, run.Updated, run.Skipped, run.Failed)
	return run, nil
}

// ImportPreviewItem is one document a preview run WOULD write, classified by
// the same idempotency key the real import upsert uses.
type ImportPreviewItem struct {
	Title     string `json:"title"`
	Action    string `json:"action"` // "create" | "update" | "skip"
	ProjectID int64  `json:"project_id"`
}

// ImportPreview reports what a knowledge source WOULD import, writing nothing:
// no note, no placeholder project, no import event, no source-stat update. It
// backs the settings page's dry-run (ADR-0011 决策 4: 导入前可预览将产生的
// 笔记条数/标题), and reuses the importer's own output so there is no second
// reading path to keep in sync.
type ImportPreview struct {
	Source  string              `json:"source"`
	Created int                 `json:"created"`
	Updated int                 `json:"updated"`
	Skipped int                 `json:"skipped"`
	Items   []ImportPreviewItem `json:"items"`
}

// Total returns the number of documents the preview examined.
func (p ImportPreview) Total() int { return p.Created + p.Updated + p.Skipped }

// PreviewImport runs the source's Import() and classifies every document
// against the same key the real upsert uses — (project, source, title) via
// db.GetNoteBySourceTitle. Landing is resolved read-only: a repo path maps to
// the project owning that root_path when one exists, else project 0 (the real
// import would CREATE a placeholder — the preview must not, so the item keeps
// project 0 and still classifies as a create against the source key); an empty
// repo path maps to the seeded default project. Serialization matches
// TriggerImport because Import() reads the same external directories.
func (r *Runtime) PreviewImport(name string) (ImportPreview, error) {
	r.importMu.Lock()
	defer r.importMu.Unlock()

	r.mu.Lock()
	src := r.sources[name]
	r.mu.Unlock()
	if src == nil {
		return ImportPreview{}, fmt.Errorf("plugin runtime: unknown knowledge source %q", name)
	}

	docs, err := r.safeImport(src)
	if err != nil {
		r.recordSourceError(src, err)
		return ImportPreview{}, err
	}

	out := ImportPreview{Source: name, Items: []ImportPreviewItem{}}
	for _, d := range docs {
		pid := d.ProjectID
		if pid <= 0 {
			if strings.TrimSpace(d.RepoPath) != "" {
				if found, lerr := db.FindProjectIDByRootPath(r.db, d.RepoPath); lerr == nil {
					pid = found
				}
			} else if def, derr := db.GetDefaultProjectID(r.db); derr == nil {
				pid = def
			}
		}
		item := ImportPreviewItem{Title: d.Title, ProjectID: pid, Action: "create"}
		_, lerr := db.GetNoteBySourceTitle(r.db, pid, src.name, d.Title)
		switch {
		case lerr == nil:
			item.Action = "update"
			out.Updated++
		case lerr == sql.ErrNoRows:
			out.Created++
		default:
			item.Action = "skip"
			out.Skipped++
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// recordSourceError stores a source-level failure so SourceStatus.Enabled
// reflects it instead of always reporting healthy.
func (r *Runtime) recordSourceError(src *sourceEntry, err error) {
	r.mu.Lock()
	src.lastErr = err
	src.lastRun = time.Now().Format(time.RFC3339)
	r.mu.Unlock()
}

// landUnmatched picks a landing project for a doc whose importer matched no
// project (ADR-0023). A known repo path yields a placeholder project (which a
// later scan can promote to a real one); no repo path — an agent-global source
// like openclaw/hermes — yields the seeded default ("Uncategorized") project.
func (r *Runtime) landUnmatched(d plugin.ImportDoc) (int64, error) {
	if strings.TrimSpace(d.RepoPath) != "" {
		return db.EnsureImportPlaceholder(r.db, d.RepoPath, d.RepoName)
	}
	return db.GetDefaultProjectID(r.db)
}

// upsertDoc creates or updates a note keyed by (project, source, title).
// Returns true when the note was created, false when updated.
func (r *Runtime) upsertDoc(doc plugin.ImportDoc, source string) (bool, error) {
	kind := doc.Kind
	if kind == "" {
		kind = "knowledge"
	}
	existing, err := db.GetNoteBySourceTitle(r.db, doc.ProjectID, source, doc.Title)
	if err == nil {
		if doc.Content != "" {
			// UpdateNoteFull runs content + metadata in a single transaction, so
			// the note_versions snapshot fires once with consistent data and a
			// metadata failure can never leave content updated but meta stale.
			if uerr := db.UpdateNoteFull(r.db, existing.ID, doc.Content, doc.Title, doc.Tags, kind, existing.Pinned); uerr != nil {
				log.Printf("plugin runtime: upsertDoc UpdateNoteFull failed (note %d): %v", existing.ID, uerr)
				return false, uerr
			}
		} else if merr := db.UpdateNoteMeta(r.db, existing.ID, doc.Title, doc.Tags, kind, existing.Pinned); merr != nil {
			log.Printf("plugin runtime: upsertDoc UpdateNoteMeta failed (note %d): %v", existing.ID, merr)
			return false, merr
		}
		return false, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	_, err = db.CreateNoteEx(r.db, doc.ProjectID, doc.Title, doc.Content, doc.Tags, kind, source)
	if err != nil {
		return false, err
	}
	return true, nil
}

// safeImport runs an importer, recovering any panic.
func (r *Runtime) safeImport(src *sourceEntry) (docs []plugin.ImportDoc, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			docs = nil
			err = fmt.Errorf("plugin runtime: import %q panicked: %v", src.name, rec)
		}
	}()
	return src.importFn()
}

// RegisterSource registers a Go-native knowledge importer as an AUTO source,
// eligible for the startup "import all" pass. Built-in curated importers (the
// Claude memory importer) use this.
func (r *Runtime) RegisterSource(name string, importer plugin.KnowledgeImporter) {
	r.registerSource(name, importer, true)
}

// RegisterSourceManual registers a Go-native importer that is excluded from the
// startup auto-import and only runs when triggered explicitly by name. Used for
// privacy-sensitive sources (codex session transcripts). ADR-0011 决策 4.
func (r *Runtime) RegisterSourceManual(name string, importer plugin.KnowledgeImporter) {
	r.registerSource(name, importer, false)
}

func (r *Runtime) registerSource(name string, importer plugin.KnowledgeImporter, auto bool) {
	if importer == nil {
		return
	}
	e := &sourceEntry{name: name, plugin: "builtin", importFn: importer.Import, auto: auto}
	// Optional presence check (ADR-0022 决策 1): importers that implement
	// plugin.Prober contribute their read-only probe to the settings page.
	if p, ok := importer.(plugin.Prober); ok {
		e.probeFn = p.Probe
	}
	// Optional rejection reporting: importers that can tell "nothing to
	// import" from "I could not read this" implement plugin.RejectionReporter,
	// and the runtime folds the result into the import report. Without it a
	// format change upstream turns into a silent zero-import run — the
	// 2026-10-18 opencode incident.
	if rr, ok := importer.(plugin.RejectionReporter); ok {
		e.rejectionsFn = rr.Rejections
	}
	r.mu.Lock()
	r.sources[name] = e
	r.mu.Unlock()
}
