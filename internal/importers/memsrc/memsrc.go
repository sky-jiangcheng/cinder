// Package memsrc holds the small, source-agnostic helpers shared by the
// built-in memory importers (claude, codex, ...). Extracted when the second
// importer landed (ADR-0011 决策 2): each importer supplies its own file
// discovery and document shaping, while project matching and bounded reads
// live here so they cannot drift apart across sources.
package memsrc

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
)

// MatchProject finds the Cinder project id for a display name, preferring an
// exact name, then a repository path ending with /name (or /name.git), then a
// project-name containment. Returns 0 when nothing matches; importers set
// ImportDoc.ProjectID = 0 in that case and the runtime counts it as skipped.
func MatchProject(displayName string, projects []db.Project, repos []db.Repository) int64 {
	lower := strings.ToLower(displayName)
	if lower == "" {
		return 0
	}
	// 1. exact project name
	for _, p := range projects {
		if p.Name == displayName {
			return p.ID
		}
	}
	// 2. repository path ending with /displayName
	for _, r := range repos {
		rp := strings.ToLower(r.Path)
		if strings.HasSuffix(rp, "/"+lower) || strings.HasSuffix(rp, "/"+lower+".git") {
			if r.ProjectID != nil {
				return *r.ProjectID
			}
		}
	}
	// 3. project name containment
	for _, p := range projects {
		if strings.Contains(strings.ToLower(p.Name), lower) {
			return p.ID
		}
	}
	return 0
}

// ReadCapped reads at most limit bytes from path. A file larger than the limit
// returns exactly limit bytes with no error, so the caller can distinguish
// "oversized" from "unreadable" and skip the file with a clear message rather
// than loading a multi-GB transcript fully into memory.
func ReadCapped(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(limit)))
}

// LastPathSegment returns the final element of a filesystem path:
// "/Users/me/Workspace/Foo" -> "Foo". Trailing slashes are trimmed. Returns ""
// for empty input.
func LastPathSegment(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

// TargetProject resolves the Cinder project a source whose memory is
// agent-global (not per-project) should attach its notes to, from a config
// value naming a project by name or numeric id. Empty/unreadable/unknown -> 0,
// which the runtime counts as skipped: a misconfigured source therefore lands
// nowhere rather than scattering notes across the wrong projects. Used by the
// openclaw and hermes importers.
func TargetProject(database *sql.DB, cfgKey string) int64 {
	val, err := db.GetConfig(database, cfgKey)
	if err != nil {
		return 0
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}
	if isNumeric(val) {
		if id, err := strconv.ParseInt(val, 10, 64); err == nil {
			return id
		}
		return 0
	}
	projects, _ := db.GetAllProjects(database)
	return MatchProject(val, projects, nil)
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// StripFrontmatter removes a leading YAML frontmatter block (between --- lines)
// from a Markdown string; input without frontmatter is returned trimmed. Shared
// by the markdown-backed importers (claude, openclaw, hermes).
func StripFrontmatter(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "---") {
		return s
	}
	idx := strings.Index(s, "\n")
	if idx < 0 {
		return s
	}
	if strings.TrimSpace(s[:idx]) != "---" {
		return s
	}
	remainder := s[idx+1:]
	lines := strings.Split(remainder, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if i+1 < len(lines) {
				return strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\r\n")
			}
			return ""
		}
	}
	return strings.TrimLeft(remainder, "\r\n")
}

// OpenReadOnly opens another application's live SQLite file without writing to
// it. Two DSN variants because live tools run in WAL mode: plain `mode=ro`
// reads the WAL correctly but fails when the sidecar `-shm` cannot be created
// or is owned by another user, and `immutable=1` skips WAL handling entirely at
// the cost of possibly reading a slightly stale snapshot. Preferring the
// accurate read and falling back to the tolerant one gets a useful import from
// a running app without ever risking its data.
//
// The returned handle is capped to ONE connection: this is a read of someone
// else's database, and SQLite serializes writers anyway.
func OpenReadOnly(path string) (*sql.DB, error) {
	dsns := []string{
		"file:" + filepath.ToSlash(path) + "?mode=ro",
		"file:" + filepath.ToSlash(path) + "?mode=ro&immutable=1",
	}
	var lastErr error
	for _, dsn := range dsns {
		st, err := sql.Open("sqlite", dsn)
		if err != nil {
			lastErr = err
			continue
		}
		// sql.Open is lazy; only a ping proves the file is readable as SQLite.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = st.PingContext(ctx)
		cancel()
		if err != nil {
			_ = st.Close()
			lastErr = err
			continue
		}
		st.SetMaxOpenConns(1)
		return st, nil
	}
	return nil, lastErr
}

// ClipToBytes returns s clipped to at most max BYTES without splitting a UTF-8
// rune. Importers compose a provenance header with the body, so the final note
// content must be clipped AFTER composition (not the body alone) to stay within
// db.MaxNoteContentLen while remaining valid UTF-8.
func ClipToBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	b := []byte(s)
	cut := max
	for cut > 0 && b[cut]&0xC0 == 0x80 { // back off off a multi-byte continuation
		cut--
	}
	return string(b[:cut])
}
