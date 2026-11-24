package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"repotory/internal/db"
	"repotory/internal/stats"
)

// This file implements the stats pre-fetch for the AI chat console.
// Why it exists: the configured chat model is not obliged to call tools, and
// the free reasoning models frequently don't — observed 2026-11-24, a
// "昨日代码提交量" ask came back with a clarifying question ("which repo?")
// while list_projects sat unused in the registry, and an earlier ask (2026-11-12)
// was answered by pasting a stale metrics snapshot out of the evidence block.
// A tool the model may or may not call is not a measurement guarantee.
//
// So for stats-shaped questions the SERVER runs the stats tools itself, before
// the model is asked anything, and drops the live numbers into the system
// prompt as a measurement block. The model's job shrinks from "operate tools
// correctly" to "read numbers out loud" — a task even the weakest model gets
// right, because the numbers are already in front of it. The tool registry
// stays available for follow-ups this prefetch does not cover (a named author,
// a longer window, a repo the console is not scoped to).

// statsIntentMarkers are the tokens that make a question a code-stats question.
// Kept narrow on purpose: a false positive costs one extra git sweep, but a
// false negative is exactly the failure this file exists to prevent, so the
// Chinese markers cover the phrasings a dashboard user actually types.
var statsIntentMarkers = []string{
	// 提交（量/数/记录）与代码量家族。
	"提交量", "提交数", "提交记录", "代码提交", "提交活跃", "代码量", "代码行",
	"改动量", "代码统计", "活跃度", "写了多少代码", "多少行",
	// 口语化说法：动词+疑问词的组合（昨天提交了多少 / 今天提交了几个）。
	"提交了多少", "多少次提交", "多少个提交", "提交次数", "提交情况",
	// English equivalents.
	"commit count", "commit volume", "commits", "commit activity",
	"code stats", "code changes", "lines added", "lines deleted",
	"how many commits", "daily stats",
}

// statsIntentNegatives retire the markers' false positives: questions ABOUT
// commits (how to write one, what convention to follow) are not questions
// ABOUT commit numbers, and prefetching numbers for them would just dilute
// the prompt.
var statsIntentNegatives = []string{
	"怎么写", "如何写", "怎么提交", "如何提交", "规范", "格式",
	"commit message", "commit msg", "commit convention", "commit lint",
	"how to write", "how do i write",
}

// looksLikeStatsQuestion reports whether a question asks for code-activity
// numbers. Purely textual by design: it runs on every ask, so it must not
// touch the DB, the network, or the model.
func looksLikeStatsQuestion(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return false
	}
	matched := false
	for _, m := range statsIntentMarkers {
		if strings.Contains(q, m) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	for _, n := range statsIntentNegatives {
		if strings.Contains(q, n) {
			return false
		}
	}
	return true
}

// Prefetch scope caps. An unscoped ask fans out over every known project, and
// each repo is one git log; without a cap a hundred-project knowledge base
// would turn every "昨日提交量" into a minute of git. Twelve projects is
// larger than any dashboard this app has shipped, and the block says plainly
// when the sweep was truncated so the model can qualify its answer.
const (
	prefetchMaxProjects   = 12
	prefetchMaxRepos      = 10
	prefetchMaxReposTotal = 30
)

// prefetchStatsBlock runs the live git stats the question is about and renders
// them as a system-prompt block. It covers yesterday and today (the two dates
// "提交量" questions actually mean), the console's scoped project when one is
// set, or every known project when not. Errors degrade per repo — a repo that
// fails to query reports "unavailable" instead of failing the whole ask,
// because a half answer that names the failure still beats no answer.
//
// An empty string means "not a stats question" (or nothing to measure): the
// caller then sends the prompt exactly as before.
func (s *Service) prefetchStatsBlock(ctx context.Context, projectID int64, question string) string {
	if !looksLikeStatsQuestion(question) {
		return ""
	}

	// Dates come from the git provider dependency, not the stats package
	// directly: the provider is what tests fake, and what every other tool
	// consults, so the prefetch can never disagree with the tools' dates.
	today := s.git.GetTodayDate()
	yesterday := s.git.GetYesterdayDate()

	type repoLine struct {
		project string
		path    string
		yest    *stats.DailyEntry
		tod     *stats.DailyEntry
		errText string
	}

	collect := func(projectName string, repoPaths []string) []repoLine {
		if len(repoPaths) > prefetchMaxRepos {
			repoPaths = repoPaths[:prefetchMaxRepos]
		}
		lines := make([]repoLine, 0, len(repoPaths))
		for _, p := range repoPaths {
			line := repoLine{project: projectName, path: p}
			entries, err := s.git.QueryStatsRange(p, yesterday, today, "")
			if err != nil {
				line.errText = err.Error()
			} else {
				for i := range entries {
					switch entries[i].Date {
					case yesterday:
						e := entries[i]
						line.yest = &e
					case today:
						e := entries[i]
						line.tod = &e
					}
				}
			}
			lines = append(lines, line)
		}
		return lines
	}

	var lines []repoLine
	truncated := false
	switch {
	case projectID > 0:
		repos, err := db.GetRepositoriesByProjectID(s.db, projectID)
		if err != nil {
			return ""
		}
		paths := make([]string, 0, len(repos))
		for _, r := range repos {
			paths = append(paths, r.Path)
		}
		if project, perr := db.GetProjectByID(s.db, projectID); perr == nil && project != nil {
			lines = collect(project.Name, paths)
		} else {
			lines = collect("", paths)
		}
	default:
		projects, err := db.GetAllProjects(s.db)
		if err != nil || len(projects) == 0 {
			return ""
		}
		sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
		if len(projects) > prefetchMaxProjects {
			projects = projects[:prefetchMaxProjects]
			truncated = true
		}
		for _, p := range projects {
			repos, err := db.GetRepositoriesByProjectID(s.db, p.ID)
			if err != nil {
				continue
			}
			paths := make([]string, 0, len(repos))
			for _, r := range repos {
				paths = append(paths, r.Path)
			}
			for _, l := range collect(p.Name, paths) {
				lines = append(lines, l)
			}
			if len(lines) >= prefetchMaxReposTotal {
				lines = lines[:prefetchMaxReposTotal]
				truncated = true
				break
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	// When the sweep has no project name to show (unlinked repos), the repo
	// path is the label; both branches below go through this helper.
	label := func(l repoLine) string {
		if l.project != "" {
			return l.project
		}
		return l.path
	}
	b.WriteString("\n\nLive measurements (queried from git just now; authoritative for ")
	b.WriteString(yesterday + " and " + today + "). ")
	b.WriteString("Report THESE numbers for commit-volume questions — do not recompute, do not ")
	b.WriteString("substitute the memory snapshots below, and never invent a date:\n")
	var ty, td stats.DailyEntry
	shown := 0
	for _, l := range lines {
		if l.errText != "" {
			fmt.Fprintf(&b, "- %s (%s): query failed (%s)\n", label(l), l.path, l.errText)
			continue
		}
		fmt.Fprintf(&b, "- %s (%s): yesterday %s: %s; today %s: %s\n",
			label(l), l.path, yesterday, entryTextFor(l.yest), today, entryTextFor(l.tod))
		if l.yest != nil {
			ty.Commits += l.yest.Commits
			ty.LinesAdded += l.yest.LinesAdded
			ty.LinesDeleted += l.yest.LinesDeleted
		}
		if l.tod != nil {
			td.Commits += l.tod.Commits
			td.LinesAdded += l.tod.LinesAdded
			td.LinesDeleted += l.tod.LinesDeleted
		}
		shown++
	}
	if shown > 1 {
		fmt.Fprintf(&b, "TOTAL across %d repos: yesterday %s: %s; today %s: %s\n",
			shown, yesterday, entryTextFor(&ty), today, entryTextFor(&td))
	}
	if truncated {
		b.WriteString("(the sweep covered only the first projects/repos of the knowledge base — say so if you total the numbers)\n")
	}
	return b.String()
}

func entryTextFor(e *stats.DailyEntry) string {
	if e == nil {
		return "0 commits, 0 lines added, 0 lines deleted"
	}
	return fmt.Sprintf("%d commits, %d lines added, %d lines deleted", e.Commits, e.LinesAdded, e.LinesDeleted)
}
