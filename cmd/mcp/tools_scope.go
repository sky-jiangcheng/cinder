package main

import (
	"strings"

	"repotory/internal/bounds"
	"repotory/internal/service"
)

// resolveProjectID reads the project scope out of a tool's arguments. The
// second return value is a validation message ("" means OK): an oversized
// path used to degrade silently to a cross-project answer, which is a
// different result from the one the caller asked for.
//
// An explicit project_id always wins; a path is the fallback, resolved
// server-side by the same matcher repotory_context uses — asking an agent for
// a numeric id is the friction that made the context tool unusable out of the
// box, and a client-side matcher would be the second implementation of the
// same rule (the disagreement ADR-0027 warns about).
func resolveProjectID(svc *service.Service, args map[string]any) (int64, string) {
	if id, ok := args["project_id"].(float64); ok && int64(id) > 0 {
		return int64(id), ""
	}
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return 0, ""
	}
	if msg := bounds.Check("path", path, bounds.MaxPathLen); msg != "" {
		return 0, msg
	}
	res := svc.ResolveWorkspace("", path)
	if res != nil && res.Project != nil {
		return res.Project.ID, ""
	}
	return 0, ""
}
