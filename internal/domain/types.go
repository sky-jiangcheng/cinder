// Package domain defines the plain data types shared across Cinder layers:
// persistence (internal/db), business logic (internal/service) and the
// bindings exposed to the frontend, CLI and MCP server. The types carry no
// behaviour and no storage semantics so every layer can depend on them
// without cycles.
package domain

// Project represents a row in the projects table.
type Project struct {
	ID            int64  `db:"id" json:"id"`
	Name          string `db:"name" json:"name"`
	RootPath      string `db:"root_path" json:"root_path"`
	LevelOverride int    `db:"level_override" json:"level_override"`
	IsAutoGrouped bool   `db:"is_auto_grouped" json:"is_auto_grouped"`
	IsStarred     bool   `db:"is_starred" json:"is_starred"`
	Collected     bool   `db:"collected" json:"collected"`
	CollectedAt   string `db:"collected_at" json:"collected_at"`
	CreatedAt     string `db:"created_at" json:"created_at"`
	// ProjectKind classifies how the row came to exist — see the ProjectKind*
	// constants below (ADR-0023). Wired into projectColumns by migration v24.
	ProjectKind string `db:"project_kind" json:"project_kind"`
}

// ProjectKind values (ADR-0023). The set is validated in Go, not by a SQL CHECK,
// because adding a table-level CHECK to the existing projects table would force a
// full rebuild (see ADR-0021 implementation corrections).
const (
	// ProjectKindScanned is a project discovered by the repository scanner.
	// It is the default for every row that predates v24.
	ProjectKindScanned = "scanned"
	// ProjectKindImportPlaceholder is a landing project created during an
	// external-knowledge import when the note matched no existing project. Its
	// root_path is the source repo path, so a later scan of that repo promotes
	// it to ProjectKindScanned.
	ProjectKindImportPlaceholder = "import_placeholder"
	// ProjectKindDefault is the seeded "Uncategorized" fallback project that
	// receives imports with no derivable repo path, and the organize target of
	// last resort. It must never be deleted.
	ProjectKindDefault = "default"
)

// Seed values for the single ProjectKindDefault row (ADR-0023 decision 2).
// The root_path is a synthetic sentinel no real filesystem path can produce, so
// it satisfies projects.root_path NOT NULL UNIQUE without ever colliding with a
// scanned repository. The display name is localized by the frontend from the
// kind; this fixed value is only the stored/internal name.
const (
	DefaultProjectName     = "未分类"
	DefaultProjectRootPath = "repotory://memory/default"
)

// MergeSibling is one project that a "merge up" (grouping level +) would swallow:
// the merge moves its repositories, notes and todos into the surviving project and
// then deletes the row. It exists as a type because the operation is destructive:
// the same listing feeds the confirmation preview, so what the user is asked to
// approve is computed by the same code that performs the merge (see
// db.MergeAbsorbableSiblings) and cannot quietly understate the damage.
type MergeSibling struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
	Repos    int    `json:"repos"`
	Notes    int    `json:"notes"`
	Todos    int    `json:"todos"`
}

// OrganizeGroup is one row in the knowledge-base "organize" surface (ADR-0023
// decision 5): a landing project — an import placeholder or the default
// ("Uncategorized") bucket — that holds notes the user has not yet assigned to a
// real, scanner-backed project. RepoBound reports whether a scanned repository
// currently points at the project; an unbound placeholder whose repo has vanished
// from disk is the "source repo gone" case where deleting it may also delete its
// notes (a manual, user-confirmed action — the default project is never offered).
type OrganizeGroup struct {
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
	Kind      string `json:"kind"`
	NoteCount int    `json:"note_count"`
	RepoBound bool   `json:"repo_bound"`
}

// Repository represents a row in the repositories table.
type Repository struct {
	ID          int64   `json:"id"`
	Path        string  `json:"path"`
	ProjectID   *int64  `json:"project_id"`
	LastScanned *string `json:"last_scanned_at"`
}

// Todo represents a row in the project_todos table.
type Todo struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
	Priority  int    `json:"priority"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Note represents a row in the project_notes table.
type Note struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Tags      string `json:"tags"`
	Kind      string `json:"kind"`
	Pinned    bool   `json:"pinned"`
	Source    string `json:"source"`
	// SourceType is the DERIVED display bucket of Source (ADR-0022 决策 4):
	// agent-import / user-conversation / project-extract / manual. It is not
	// a database column — the classification is presentation semantics over
	// the versioned source string, so the server fills it on the way out and
	// the client never has to carry a second copy of the mapping.
	SourceType string `json:"source_type,omitempty"`
	// TopicID is the topic this note is filed under, inside its project
	// (schema v27: project -> topic -> note). Zero means the note has no
	// topic — a legitimate state, since every note written before v27 has
	// none and the user may never file one.
	TopicID   int64  `json:"topic_id"`
	TopicName string `json:"topic_name,omitempty"`
	SortOrder int    `json:"sort_order"`
	// Version is the optimistic-lock counter (schema v31, ADR-0057). It starts
	// at 1 and every write that changes title/content/tags/kind increments it.
	// A caller that edits a note passes back the version it read, so a second
	// writer — the desktop app and cinder-mcp are separate processes — gets a
	// conflict instead of silently overwriting the first one's work.
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// NoteWithProject is a note joined with its parent project.
type NoteWithProject struct {
	Note
	ProjectName string `json:"project_name"`
}

// NoteTopic is a topic of one project — the layer between a project and the
// notes filed under it (schema v27).
type NoteTopic struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
	// NoteCount is how many notes are filed under this topic.
	NoteCount int    `json:"note_count"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// NoteVersion represents a historical snapshot of a note.
type NoteVersion struct {
	ID     int64 `json:"id"`
	NoteID int64 `json:"note_id"`
	// Version is the note version this row is the state of (schema v31,
	// ADR-0057). 0 means "recorded before v31", whose rows hold post-edit
	// content that cannot be mapped back to a version number.
	Version   int    `json:"version"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Tags      string `json:"tags"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
}

// NoteDiff represents a line-level diff between two versions.
type NoteDiff struct {
	VersionID int64  `json:"version_id"`
	CreatedAt string `json:"created_at"`
	Diff      string `json:"diff"`
}

// TodoCount holds incomplete and total todo counts for a project.
type TodoCount struct {
	ProjectID int64 `json:"project_id"`
	Count     int   `json:"count"`
	Total     int   `json:"total"`
}

// NoteCount holds the note count for a project.
type NoteCount struct {
	ProjectID int64 `json:"project_id"`
	Count     int   `json:"count"`
}

// DailyStat represents a row in the daily_stats table.
type DailyStat struct {
	ID           int64  `json:"id"`
	RepositoryID int64  `json:"repository_id"`
	StatDate     string `json:"stat_date"`
	Author       string `json:"author"`
	FilesChanged int    `json:"files_changed"`
	LinesAdded   int    `json:"lines_added"`
	LinesDeleted int    `json:"lines_deleted"`
}

// HeatmapDay holds aggregated stats for a single day.
type HeatmapDay struct {
	Date         string `json:"date"`
	LinesAdded   int    `json:"lines_added"`
	LinesDeleted int    `json:"lines_deleted"`
	Commits      int    `json:"commits"`
}

// SearchHit is a unified search result from notes and todos.
type SearchHit struct {
	Type      string  `json:"type"`
	ID        int64   `json:"id"`
	ProjectID int64   `json:"project_id"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	Rank      float64 `json:"rank,omitempty"` // bm25 score; lower = more relevant
}

// RepoMeta represents a row in the repo_meta table.
type RepoMeta struct {
	RepositoryID    int64  `json:"repository_id"`
	TechStack       string `json:"tech_stack"`
	ReadmeExcerpt   string `json:"readme_excerpt"`
	Languages       string `json:"languages"`
	Dependencies    string `json:"dependencies"`
	TopContributors string `json:"top_contributors"`
	Activity        string `json:"activity"`
	UpdatedAt       string `json:"updated_at"`
}
