package service

import (
	"fmt"
	"strconv"
	"strings"

	"repotory/internal/db"
)

// ConfigData holds the application configuration sent to the frontend.
type ConfigData struct {
	Config    map[string]string `json:"config"`
	ScanRoots []string          `json:"scan_roots"`
}

// allowedConfigKeys is the allow-list of user-settable configuration keys.
var allowedConfigKeys = map[string]bool{
	"daily_code_standard": true,
	"scan_depth":          true,
	"git_author":          true,
	"auto_import":         true,
	// M1 Claude session auto-capture: "1" enables on-demand capture, anything
	// else (incl. unset) keeps it OFF. Reading session transcripts is
	// privacy-sensitive, so it is never implicitly enabled (ADR-0010).
	"claude_session_capture": true,
	// Target project for the agent-GLOBAL memory importers (openclaw / hermes).
	// Value is a project name or numeric id; unset -> those sources skip. See
	// memsrc.TargetProject and ADR-0011.
	"openclaw_project": true,
	"hermes_project":   true,
	// Curated CodeBuddy memory (~/.codebuddy/memery/*.md +
	// Public/.memories/*.mdc) is agent-GLOBAL, so it attaches to the project
	// named here — same contract as openclaw_project / hermes_project.
	"codebuddy_project": true,
	// M3-A semantic search (ADR-0012). All default-OFF: semantic search runs
	// only when semantic_search=="1" AND a base_url+model are configured.
	// embedding_api_key is a SECRET and is redacted in GetConfig.
	"semantic_search":    true,
	"embedding_base_url": true,
	"embedding_model":    true,
	"embedding_api_key":  true,
	"embedding_dim":      true,
	// M3-A axis B (ADR-0013): vector STORE backend. Default local sqlite-vec;
	// vector_store="qdrant" + url (+api-key) opts into a remote/self-hosted
	// Qdrant, and any failure to reach it silently falls back to local.
	// vector_store_api_key is a SECRET and is redacted in GetConfig.
	"vector_store":            true,
	"vector_store_url":        true,
	"vector_store_api_key":    true,
	"vector_store_collection": true,
	// AI Q&A chat (OpenAI-compatible /chat/completions): LM Studio
	// (http://localhost:1234/v1) or any remote provider. ai_chat_api_key is
	// a SECRET and is redacted in GetConfig.
	// M6-W4 lint: gates ONLY the model-backed contradiction/staleness checks. The
	// structural half needs no permission because it costs nothing and cannot write
	// a page. Follows semantic_search's treatment (numeric 0/1, so no
	// stringConfigKeys entry).
	// M6-W3 compile: gates the ONLY path that lets a model write into the wiki
	// layer. Off by default, independent of semantic_search (that sends text out
	// to get a vector back; this sends text out to get stored content back) and
	// independent of wiki_lint_llm (which only reads).
	"wiki_compile":  true,
	"wiki_lint_llm": true,
	// M7/ADR-0019 merge suggestions: lets a model judge whether two pages are the
	// same concept. Needs wiki_lint_llm too (it runs inside the lint model half),
	// and its own key because judging similarity is separate consent from judging
	// contradictions. Suggestions only ever become todos — the merge itself is a
	// human click, with no gate because it is not automatic.
	"wiki_merge_suggest": true,
	// M7/ADR-0020 community synthesis: the ONLY path that lets a model write
	// synthesis pages. Off by default, never scheduled (it spends tokens and writes
	// pages, unlike lint), and the two size keys below are its tunables — numeric, so
	// no stringConfigKeys entry.
	"wiki_community_summary": true,
	"community_min_size":     true,
	"community_max_size":     true,
	// M7/ADR-0018 lane 2: graph-aware evidence. Default OFF. When on, AskAI's page
	// evidence expands a retrieval hit along ref/part-of/documents edges (1-2 hops)
	// and re-ranks with PageRank; when off, evidencePages keeps the exact FTS order
	// it has always used (zero behavior regression). Numeric 0/1, so no
	// stringConfigKeys entry. Independent of semantic_search: graph expansion is
	// pure local SQL, but page-vector recall within it needs semantic_search on.
	"wiki_graph_search": true,
	"ai_chat_base_url":  true,
	"ai_chat_model":     true,
	"ai_chat_api_key":   true,
	// Periodic knowledge sync. knowledge_sync_interval_minutes is the ticker
	// cadence (default 240; re-read every round, so a change applies without a
	// restart). Each sync_source_<name> is the per-source opt-in switch — every
	// transcript source defaults OFF in code, and curated claude memory
	// defaults ON. Numeric values only, enforced by the check below.
	"knowledge_sync_interval_minutes": true,
	"sync_source_claude":              true,
	"sync_source_codex":               true,
	"sync_source_opencode":            true,
	"sync_source_openclaw":            true,
	"sync_source_hermes":              true,
	"sync_source_cursor":              true,
	"sync_source_codebuddy":           true,
	"sync_source_codebuddy-memory":    true,
	// Update check (Settings → Updates). update_check_enabled is numeric 0/1
	// (default ON — an absent row reads as enabled, see UpdateCheckEnabled);
	// update_proxy is an optional http(s) proxy URL for the GitHub Releases
	// API call, because api.github.com is unreachable behind the GFW most
	// users sit behind and a Finder-launched GUI app inherits no HTTP_PROXY.
	"update_check_enabled": true,
	"update_proxy":         true,
	// AI call audit and content review (ADR-0030). All four default OFF: with
	// them closed, behaviour is byte-for-byte what it was before and an
	// existing user notices nothing.
	//
	// ai_audit_enabled writes one ai_call_log row per outbound model call.
	// ai_review_input screens the prompt BEFORE it leaves; ai_review_output
	// screens the reply as it arrives (streaming output review can only
	// withhold the tail — the head is already on screen).
	// ai_review_rules holds the user's own patterns, one regex per line;
	// Cinder ships no built-in blocklist, because a keyword list is a weak
	// control and a false promise of coverage.
	"ai_audit_enabled": true,
	"ai_review_input":  true,
	"ai_review_output": true,
	"ai_review_rules":  true,
}

// stringConfigKeys are exempt from the numeric-value check: they carry free-text
// (author name, project name/id, embedding endpoint/model/key, vector store).
var stringConfigKeys = map[string]bool{
	"git_author":              true,
	"openclaw_project":        true,
	"hermes_project":          true,
	"codebuddy_project":       true,
	"embedding_base_url":      true,
	"embedding_model":         true,
	"embedding_api_key":       true,
	"vector_store":            true,
	"vector_store_url":        true,
	"vector_store_api_key":    true,
	"vector_store_collection": true,
	"ai_chat_base_url":        true,
	"ai_chat_model":           true,
	"ai_chat_api_key":         true,
	"update_proxy":            true,
	"ai_review_rules":         true,
}

// secretConfigKeys are never returned in plaintext by GetConfig — a set value
// is masked so the frontend learns "configured" without seeing the credential.
// The backend still reads the real value via db.GetConfig directly.
//
// backup_remote_secret_key belongs here for the same reason ai_chat_api_key
// does: it is a credential for an outbound service. The settings form treats
// the mask as "unchanged" and skips writing it back (the same rule the AI tab
// already follows), so masking costs the user nothing and keeps the secret out
// of the payload the frontend receives.
var secretConfigKeys = map[string]bool{
	"embedding_api_key":        true,
	"vector_store_api_key":     true,
	"ai_chat_api_key":          true,
	"backup_remote_secret_key": true,
}

// secretMask replaces a configured secret in responses sent to the frontend.
const secretMask = "********"

// GetConfig returns all configuration settings and scan roots.
func (s *Service) GetConfig() (*ConfigData, error) {
	configs, err := db.GetAllConfigs(s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	roots, err := db.GetScanRoots(s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to load scan roots: %w", err)
	}
	// A nil slice serialises to JSON null, which the frontend spreads/filters
	// directly and would throw. Always emit an empty array instead.
	if roots == nil {
		roots = []string{}
	}
	// Never return secret values in plaintext: a configured secret is masked to
	// a sentinel so the UI can show "set" without exposing the credential. The
	// backend reads the real value via db.GetConfig directly (e.g. the M3-A
	// embedder), never through this response.
	for key := range secretConfigKeys {
		if configs[key] != "" {
			configs[key] = secretMask
		}
	}
	return &ConfigData{Config: configs, ScanRoots: roots}, nil
}

// wikiLintLLMEnabled is the explicit opt-in for the model-backed lint checks.
// Anything other than "1" skips them, and the report says why rather than looking
// like a clean run.
func (s *Service) wikiLintLLMEnabled() bool {
	v, err := db.GetConfig(s.db, wikiLintLLMKey)
	return err == nil && v == "1"
}

// autoImportEnabled reports whether startup may read other tools' memory files
// and write them into this database without being asked. Explicit opt-in only
// (ADR-0014 W0, schema v15): anything other than "1" means OFF.
//
// The absent-row case is the reason this is a helper rather than an inline test.
// db.GetConfig maps sql.ErrNoRows to ("", nil), so the previous predicate
// (`err == nil && v != "0"`) read an untouched database as ON — which is how a
// setting documented as "the user can disable it" stayed effectively enabled
// for anyone who never touched it. Comparing against "1" makes the safe state
// the default state and takes the load off the seeded row.
func (s *Service) autoImportEnabled() bool {
	v, err := db.GetConfig(s.db, "auto_import")
	return err == nil && v == "1"
}

// UpdateConfig sets a single configuration key-value pair after validating
// the key against the allow-list and numeric values.
func (s *Service) UpdateConfig(key, value string) error {
	if !allowedConfigKeys[key] {
		return fmt.Errorf("unknown config key: %s", key)
	}
	if !stringConfigKeys[key] {
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("config value must be a number")
		}
	}
	if err := db.SetConfig(s.db, key, value); err != nil {
		return err
	}
	// git_author is applied immediately so "mine" stats, heatmap and recent
	// commits reflect the new author without a restart.
	if key == "git_author" {
		s.setGitUser(value)
	}
	// Any vector_store* change (kind, url, api key, collection) drops the
	// memoised store — otherwise reconfiguring the endpoint from the settings
	// page would keep using the old handle, and its cached reachability verdict,
	// until restart.
	if strings.HasPrefix(key, "vector_store") {
		s.invalidateVectorStore()
	}
	// Pointing at a different endpoint or model is exactly when the
	// "returned vectors don't match the configured dimension — rebuild" notice
	// becomes worth repeating, so un-mute it here.
	if strings.HasPrefix(key, "embedding_") {
		s.embedDimWarned.Store(false)
	}
	return nil
}

// UpdateScanRoots replaces the entire scan root list atomically.
//
// The submitted list is normalised first (see normalizeScanRoots): only real
// directories are stored, in canonical form, deduplicated. Entries that could
// not be accepted come back in Rejected instead of failing the whole call — the
// caller keeps the roots it asked for and learns exactly which ones were
// dropped and why.
//
// Storing the normalised list (rather than echoing the input back) is what
// keeps the persisted config and the scan itself in agreement: a root the
// scanner never visits must never appear to be configured.
func (s *Service) UpdateScanRoots(scanRoots []string) (*ScanRootsResult, error) {
	roots, rejected := normalizeScanRoots(scanRoots)
	if err := db.ReplaceScanRoots(s.db, roots); err != nil {
		return nil, fmt.Errorf("failed to update scan roots: %w", err)
	}
	// Both slices are always emitted as arrays, never null: the settings page
	// iterates them directly, and a null here throws a TypeError in the very tab
	// the user is looking at when the update succeeds.
	if roots == nil {
		roots = []string{}
	}
	if rejected == nil {
		rejected = []ScanRootRejection{}
	}
	return &ScanRootsResult{ScanRoots: roots, Rejected: rejected}, nil
}
