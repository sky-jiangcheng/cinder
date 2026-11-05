package service

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
)

// OMP-compatible memory export (ADR-0013 outlook). OMP — the Open Memory
// Protocol — is an emerging, vendor-neutral standard for portable AI memory
// (Memory Object: id/content/type/source/tags/created_at/updated_at). Emitting
// this shape gives Cinder a portable export/import seam that could replace
// per-tool importer guessing once OMP matures.
//
// PROVISIONAL: OMP is v0.4 (pre-1.0). We export a superset-compatible object and
// treat the type mapping below as our best-effort projection, not a settled
// contract; the importer side is intentionally not written yet.
type OMPMemory struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Type      string    `json:"type"`
	Source    OMPSource `json:"source"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt string    `json:"created_at,omitempty"`
	UpdatedAt string    `json:"updated_at,omitempty"`
	ExpiresAt string    `json:"expires_at,omitempty"`
}

// OMPSource is the OMP memory provenance block.
type OMPSource struct {
	Tool      string `json:"tool"`
	SessionID string `json:"session_id,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// ompType maps a Cinder note to an OMP memory type. Cinder kinds are
// knowledge|log|idea|other; a note tagged "handoff" is a session exit record.
// Projection: handoff/log -> episodic (what happened); idea -> procedural
// (intended how-to); knowledge/other -> semantic (facts).
func ompType(kind string, tags string) string {
	if strings.Contains(strings.ToLower(tags), handoffTag) || kind == "log" {
		return "episodic"
	}
	switch kind {
	case "idea":
		return "procedural"
	default:
		return "semantic"
	}
}

func ompTags(kind, tags string) []string {
	out := []string{}
	for _, t := range strings.Split(tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if kind != "" {
		out = append(out, "kind:"+kind)
	}
	return out
}

// ExportMemoryJSON serialises the knowledge base as an array of OMP-style
// memory objects (portable export seam). limit<=0 lists all notes.
func (s *Service) ExportMemoryJSON(limit int) string {
	notes := s.ListAllNotesLimited(limit)
	out := make([]OMPMemory, 0, len(notes))
	for _, n := range notes {
		content := n.Content
		if strings.TrimSpace(n.Title) != "" {
			content = n.Title + "\n\n" + n.Content
		}
		out = append(out, OMPMemory{
			ID:        "urn:repotory:note:" + strconv.FormatInt(n.ID, 10),
			Content:   content,
			Type:      ompType(n.Kind, n.Tags),
			Source:    OMPSource{Tool: "repotory", SessionID: n.Source, Timestamp: n.CreatedAt},
			Tags:      ompTags(n.Kind, n.Tags),
			CreatedAt: n.CreatedAt,
			UpdatedAt: n.UpdatedAt,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Printf("omp export marshal error: %v", err)
		return "[]"
	}
	return string(b)
}
