package wikigen

import "fmt"

// WikiFinding is one lint result. It moved here in ADR-0060 step three because
// the model-driven kernels produce or consume it: merge-suggest emits findings
// into lint's report, and compile/community file revision todos whose titles
// are rendered through LintTodoTitle. The structural lint half — the heaviest
// producer — still lives in internal/service until the lint step of the split,
// and reaches this type through the service-side alias.
//
// ProjectID may be 0 for a global page, in which case the finding is reported
// but cannot become a todo (todos require a project).
type WikiFinding struct {
	Check     string `json:"check"`
	Severity  string `json:"severity"` // info | warn
	ProjectID int64  `json:"project_id"`
	PageID    int64  `json:"page_id"`
	Detail    string `json:"detail"`
}

// LintTodoPrefix marks a todo as machine-generated, for the dedupe query and
// for a human scanning the list.
const LintTodoPrefix = "[lint]"

// LintTodoTitle renders the one line a reviewer sees in their todo list.
// Exported because the lint half files findings through it from the service
// package, and because compile/community's revision todos must render with the
// same prefix and the same clip — three writers, one format, or the todo list
// starts looking like three different tools wrote it.
func LintTodoTitle(f WikiFinding) string {
	return clipRunes(fmt.Sprintf("%s %s: %s", LintTodoPrefix, f.Check, f.Detail), 180)
}
