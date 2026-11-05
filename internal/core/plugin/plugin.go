// Package plugin defines the knowledge-source SPI for Cinder.
//
// The in-process script-plugin system (ADR-0002, yaegi interpreter) has been
// removed (C11 方案B): there is no user-authored plugin loader anymore. What
// remains is the small interface every built-in knowledge importer implements
// (claude, codex, opencode, openclaw, hermes, cursor, codebuddy,
// codebuddy-memory) and the document shape it produces. Registration, gating
// and the idempotent upsert path live in internal/core/plugin/runtime;
// docs/plugins/overview.md is the user-facing list of sources.
package plugin

// KnowledgeImporter imports knowledge (e.g. notes) from an external source
// into the Cinder knowledge base.
type KnowledgeImporter interface {
	// Source returns a stable source identifier, e.g. "claude".
	Source() string
	// Import fetches documents from the external source. Implementations
	// should be idempotent: re-running updates existing notes rather than
	// duplicating them.
	Import() ([]ImportDoc, error)
}

// ProbeResult is the outcome of a knowledge source's presence check: whether
// the agent's data actually exists on this machine, plus a short
// human-readable detail line (root path, match count) for the settings page.
// A probe is cheap and read-only — stat/readdir on the source's own
// allowlisted directory, never file content, never parent/sibling paths
// (ADR-0011 决策 3 hard scoping, ADR-0022 决策 1).
type ProbeResult struct {
	Detected bool   `json:"detected"`
	Detail   string `json:"detail"`
}

// Prober is an OPTIONAL capability of a KnowledgeImporter. Sources that
// implement it let the runtime report "which agent memory exists on this
// machine" in the settings page instead of listing every registered source
// blindly. Probe must stay cheap and must never read data outside the
// source's own declared directory.
type Prober interface {
	Probe() ProbeResult
}

// Rejection is one document an importer refused to produce, and why.
//
// It exists because "the importer silently dropped files" is invisible by
// construction: an importer that cannot parse one file returns fewer
// documents, and the runtime reports "created 0, updated 0, skipped 0" —
// indistinguishable from "there was nothing new to import". That is not a
// hypothetical. On 2026-10-18 the opencode importer's Summary field changed
// type upstream; every session file then failed to unmarshal, every session
// was silently rejected, and the import reported zero created notes with no
// error anywhere. The symptom was noticed days later, by a user wondering
// why nothing had come in.
type Rejection struct {
	// Path is the file that was rejected, when the importer knows it.
	Path string
	// Reason is a short human-readable explanation.
	Reason string
}

// RejectionReporter is an OPTIONAL capability of a KnowledgeImporter.
//
// An importer that can tell the difference between "nothing to import" and
// "I could not read this" implements it, and the runtime folds the result
// into the import report (ImportRun.Failed / ImportRun.Errors) and the
// settings page shows it.
//
// It is optional rather than required because the alternative — adding a
// rejections return value to Import() — would change every importer's
// signature at once, for a capability most sources have no use for. An
// importer that does not implement it keeps its current behaviour, which is
// correct for a source whose "nothing found" really does mean "nothing found".
type RejectionReporter interface {
	Rejections() []Rejection
}

// ImportDoc is a single document produced by a KnowledgeImporter, to be
// upserted into the knowledge base.
type ImportDoc struct {
	// ProjectID is the resolved target project. 0 means "the importer could not
	// match a project"; the runtime then LANDS the note rather than dropping it
	// (ADR-0023): onto a placeholder project keyed by RepoPath when one is known,
	// else onto the seeded default ("Uncategorized") project. A truly non-empty
	// RepoPath is what lets an unmatched repo-scoped source still get a home.
	ProjectID int64
	// RepoPath is the source's repository/workspace path (a real filesystem path
	// when the importer has one). Used only when ProjectID == 0 to create or reuse
	// an import-placeholder project; because it is stored as the placeholder's
	// root_path, a later scan that discovers the same repo adopts the row and
	// promotes it to a real project. Empty for agent-global sources (openclaw,
	// hermes, codebuddy-memory), which then land on the default project.
	RepoPath string
	// RepoName optionally overrides the placeholder project's display name; when
	// empty the runtime uses the base name of RepoPath. Sources whose match key is
	// a decoded label rather than a real path (claude) set this.
	RepoName string
	Title    string // note title
	Content  string // note body (Markdown)
	Tags     string // comma-separated tags
	Kind     string // note kind: knowledge | log | idea | other
	Source   string // provenance, e.g. "claude"
}
