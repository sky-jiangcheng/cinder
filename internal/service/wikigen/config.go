package wikigen

import (
	"database/sql"
	"strconv"
	"strings"

	"repotory/internal/db"
	"repotory/internal/graph"
)

// Config gates for the model-driven passes. Every one of them is an explicit
// opt-in: a missing row, an empty value and garbage all mean OFF, because
// db.GetConfig maps "no row" to "" and comparing against "1" is what makes the
// safe state the default state. The keys moved here with the kernels that read
// them; the service package's remaining callers (lint's model half, the job
// submit paths) go through wrappers, so there is still exactly one definition
// of each key.
const (
	// KeyCompile gates ingest-time compilation entirely.
	KeyCompile = "wiki_compile"
	// KeyLintLLM gates ONLY the model-backed lint checks — the umbrella any
	// model call inside lint sits behind, including merge-suggest.
	KeyLintLLM = "wiki_lint_llm"
	// KeyMergeSuggest gates the merge second opinion separately from the
	// contradiction pass (ADR-0019 待决 3): "let a model judge similarity" is
	// its own consent, even though both run inside the lint pass.
	KeyMergeSuggest = "wiki_merge_suggest"
	// KeyCommunity is the community-summary consent (ADR-0020).
	KeyCommunity = "wiki_community_summary"
	// KeyCommunityMinSize / KeyCommunityMaxSize are 待决 5's two thresholds.
	KeyCommunityMinSize = "community_min_size"
	KeyCommunityMaxSize = "community_max_size"
)

// enabled1 is the shared body of every opt-in predicate: the key is on iff its
// config row is exactly "1".
func enabled1(database *sql.DB, key string) bool {
	v, err := db.GetConfig(database, key)
	return err == nil && v == "1"
}

// CompileEnabled reports whether ingest-time compilation may run.
func CompileEnabled(database *sql.DB) bool { return enabled1(database, KeyCompile) }

// LintLLMEnabled reports whether ANY model call inside lint may run. It is
// read by merge-suggest below and by the lint pass itself, which still lives
// in the service package and reaches it through a wrapper.
func LintLLMEnabled(database *sql.DB) bool { return enabled1(database, KeyLintLLM) }

// MergeSuggestEnabled reports whether the model may judge merge pairs.
func MergeSuggestEnabled(database *sql.DB) bool { return enabled1(database, KeyMergeSuggest) }

// CommunityEnabled reports whether community synthesis may write.
func CommunityEnabled(database *sql.DB) bool { return enabled1(database, KeyCommunity) }

// CommunitySizeThresholds reads 待决 5's two overrides, falling back to the ADR
// defaults on a missing row or a nonsense value. Zero and negatives are refused
// rather than honoured: MinSize 0 would make every page its own community, and
// MaxSize 0 would discard all of them.
func CommunitySizeThresholds(database *sql.DB) (int, int) {
	minSize, maxSize := graph.DefaultMinSize, graph.DefaultMaxSize
	if v, err := db.GetConfig(database, KeyCommunityMinSize); err == nil {
		if n, cerr := strconv.Atoi(strings.TrimSpace(v)); cerr == nil && n > 0 {
			minSize = n
		}
	}
	if v, err := db.GetConfig(database, KeyCommunityMaxSize); err == nil {
		if n, cerr := strconv.Atoi(strings.TrimSpace(v)); cerr == nil && n > 0 {
			maxSize = n
		}
	}
	if minSize > maxSize {
		// An inverted range would silently discard every community, so neither
		// number the user typed is safer than the documented default.
		return graph.DefaultMinSize, graph.DefaultMaxSize
	}
	return minSize, maxSize
}
