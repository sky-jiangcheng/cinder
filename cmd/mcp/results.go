package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"repotory/internal/service"
	"repotory/internal/version"
)

func makeTextResult(text string) *mcp.CallToolResult {
	return mcp.NewToolResultText(text)
}

func makeJSONResult(name string, data any) (*mcp.CallToolResult, error) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return makeTextResult(fmt.Sprintf("marshal error: %v", err)), nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: string(bytes)},
		},
	}, nil
}

func runAgentScore(svc *service.Service) string {
	home, _ := os.UserHomeDir()
	parts := []string{}
	scores := []bool{}
	total := 0
	earned := 0

	record := func(ok bool) {
		scores = append(scores, ok)
		total++
		if ok {
			earned++
		}
	}

	// Count, not load: len(ListAllNotes()) used to materialize every note's
	// full content just to report how many there are.
	noteCount := svc.CountNotes()

	// 1. Database reachable. This used to be `noteCount > 0`, identical to
	// check 2 below — the same signal counted twice, inflating the score of an
	// empty install. Health() actually pings the handle, so it can fail where
	// "there are no notes yet" is perfectly healthy.
	if h := svc.Health(); h["status"] == "ok" {
		parts = append(parts, fmt.Sprintf("  ✅ Database reachable (schema v%s, %d note(s))", h["version"], noteCount))
		record(true)
	} else {
		parts = append(parts, fmt.Sprintf("  ❌ Database unavailable: %v — nothing below can be trusted", h["message"]))
		record(false)
	}

	// 2. Notes exist (now the only check on noteCount)
	if noteCount > 0 {
		parts = append(parts, fmt.Sprintf("  ✅ Notes exist: %d", noteCount))
		record(true)
	} else {
		parts = append(parts, "  ⚠️  No notes — import Claude memory or create some")
		record(false)
	}

	// 3. Search works. `hits != nil` looks like it should be `len(hits) > 0`
	// and is worth a note, because the two are NOT equivalent here:
	// internal/db/search.go returns a nil slice for a successful zero-hit
	// query, but internal/service/search.go normalises that to an empty
	// non-nil slice, and returns nil *only* when the query actually errored.
	// So nil means "the search path is broken" and empty means "searched
	// fine, nothing matched" — which is the healthy state for a fresh install.
	hits := svc.SearchAll("test")
	if hits != nil {
		parts = append(parts, fmt.Sprintf("  ✅ Search (FTS5) operational (%d hit(s) for probe)", len(hits)))
		record(true)
	} else {
		parts = append(parts, "  ⚠️  Search not available — the FTS5 query path returned an error")
		record(false)
	}

	// 4. Claude memory importable
	claudeMemory := filepath.Join(home, ".claude", "projects")
	if info, err := os.Stat(claudeMemory); err == nil && info.IsDir() {
		files, _ := filepath.Glob(filepath.Join(claudeMemory, "*", "memory", "*.md"))
		if len(files) > 0 {
			parts = append(parts, fmt.Sprintf("  ✅ Claude memory sources found: %d files", len(files)))
			record(true)
		} else {
			parts = append(parts, "  ⚠️  Claude memory directory exists but no .md files")
			record(false)
		}
	} else {
		parts = append(parts, "  ⚠️  No Claude memory directory found")
		record(false)
	}

	// 5. llms.txt export works
	if txt := svc.GenerateLLMsTxt(); len(txt) > 0 {
		parts = append(parts, "  ✅ llms.txt export generates content")
		record(true)
	} else {
		parts = append(parts, "  ⚠️  llms.txt export returned empty")
		record(false)
	}

	// 6. SKILL.md exists
	root := "."
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for {
			if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
				root = dir
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
		parts = append(parts, "  ✅ SKILL.md present")
		record(true)
	} else {
		parts = append(parts, "  ⚠️  SKILL.md not found in repo root")
		record(false)
	}

	// 7. i18n locales
	locales := []string{"zh-CN", "en"}
	foundLocales := 0
	for _, loc := range locales {
		if _, err := os.Stat(filepath.Join(root, "web", "src", "locales", loc, "common.json")); err == nil {
			foundLocales++
		}
	}
	if foundLocales >= len(locales) {
		parts = append(parts, fmt.Sprintf("  ✅ i18n: %d locale(s) found", foundLocales))
		record(true)
	} else {
		parts = append(parts, fmt.Sprintf("  ⚠️  i18n incomplete: %d/%d locales", foundLocales, len(locales)))
		record(false)
	}

	// Build report
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Cinder Agent Score (v%s) ===\n\n", version.Version)
	for _, p := range parts {
		fmt.Fprintln(&sb, p)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "Score: %d/%d\n", earned, total)
	pct := float64(earned) / float64(total) * 100
	fmt.Fprintf(&sb, "AI-readiness: %.0f%%\n\n", pct)

	if pct >= 75 {
		fmt.Fprintln(&sb, "✅ Cinder is agent-ready! MCP and tools are functional.")
	} else if pct >= 50 {
		fmt.Fprintln(&sb, "⚠️  Cinder is partially ready. Review warnings above.")
	} else {
		fmt.Fprintln(&sb, "❌ Cinder needs setup before agents can use it effectively.")
	}
	return sb.String()
}
