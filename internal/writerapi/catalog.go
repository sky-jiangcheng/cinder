// Package writerapi is the write catalogue of ADR-0058's single-writer
// contract: the fixed list of operations an agent surface is allowed to ask
// the elected writer to perform, plus the Manager that decides whether this
// process runs them itself or forwards them.
//
// Two packages, split deliberately: internal/writer knows only election and
// transport (no Service, no domain), so it stays testable in isolation. This
// one binds the catalogue to internal/service.
//
// WHY A CATALOGUE AND NOT REFLECTION. The HTTP RPC bridge reaches every App
// method by name through reflection; doing that here would mean the write
// contract is "whatever exists", which is exactly the property ADR-0058 is
// trying to establish. So the list below is explicit, one case per operation,
// and writerapi_test.go asserts it covers every write an MCP tool can trigger.
// A new MCP write tool with no catalogue entry fails that test instead of
// quietly writing from a non-writer process.
package writerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/errcode"
	"repotory/internal/service"
)

// Catalogue method names. These are internal to the machine (writer ↔ client),
// NOT the MCP tool names and NOT the frozen `repotory_*` identifier layer —
// renaming one of these is a code change in one file, not a contract break.
const (
	MethodNotesCreate   = "notes.create"
	MethodNotesUpdate   = "notes.update"
	MethodHandoffCreate = "handoff.create"
	MethodChatCreate    = "chat.create"
	MethodChatAppend    = "chat.append"
	MethodChatRename    = "chat.rename"
	MethodScanRun       = "scan.run"
	MethodReviewRun     = "review.run"
	MethodCaptureClaude = "capture.claude_session"
	MethodAuditRecord   = "audit.record"
)

// ErrUnknownMethod is returned when a request names something outside the
// catalogue. It classifies as not-found: the caller sent a method that does not
// exist, and retrying cannot help.
var ErrUnknownMethod = errors.New("unknown write method")

// Args structs, one per method. They exist so the wire shape is declared in
// one place per operation instead of being inferred from a service signature,
// and so a client cannot smuggle an argument the writer never agreed to read.

// NotesCreateArgs mirrors svc.CreateNoteWithMeta.
type NotesCreateArgs struct {
	ProjectID int64  `json:"project_id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Tags      string `json:"tags"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
}

// NotesUpdateArgs mirrors svc.UpdateNoteFull. ExpectedVersion is the ADR-0057
// D2 optimistic lock: the agent surface passes 0 (no check), the desktop
// editor passes the version it read. It travels here so a merged partial
// update decided in the client cannot silently overwrite a newer row.
type NotesUpdateArgs struct {
	NoteID          int64  `json:"note_id"`
	Content         string `json:"content"`
	Title           string `json:"title"`
	Tags            string `json:"tags"`
	Kind            string `json:"kind"`
	Pinned          bool   `json:"pinned"`
	ExpectedVersion int    `json:"expected_version"`
}

// HandoffCreateArgs mirrors service.HandoffInput.
type HandoffCreateArgs struct {
	ProjectID     int64    `json:"project_id"`
	Agent         string   `json:"agent"`
	Summary       string   `json:"summary"`
	Changes       []string `json:"changes"`
	Decisions     []string `json:"decisions"`
	Gotchas       []string `json:"gotchas"`
	NextSteps     []string `json:"next_steps"`
	Tags          string   `json:"tags"`
	Source        string   `json:"source"`
	WorkspacePath string   `json:"workspace_path"`
	Milestone     bool     `json:"milestone"`
	SessionID     string   `json:"session_id"`
}

// ChatCreateArgs mirrors svc.CreateChatThread.
type ChatCreateArgs struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// ChatAppendArgs mirrors service.ChatAppendInput.
type ChatAppendArgs struct {
	ThreadID string  `json:"thread_id"`
	ID       string  `json:"id"`
	Role     string  `json:"role"`
	Content  string  `json:"content"`
	Source   string  `json:"source"`
	ReplyTo  *string `json:"reply_to"`
}

// ChatRenameArgs mirrors svc.RenameChatThread.
type ChatRenameArgs struct {
	ThreadID string `json:"thread_id"`
	Title    string `json:"title"`
}

// ReviewRunArgs mirrors reviewrun.Options. Date is carried as RFC3339 because
// time.Time's JSON form is what round-trips; the zero value means "yesterday"
// exactly as in-process.
type ReviewRunArgs struct {
	Date   time.Time `json:"date"`
	Scope  string    `json:"scope"`
	DryRun bool      `json:"dry_run"`
	Force  bool      `json:"force"`
	NoAI   bool      `json:"no_ai"`
}

// CaptureClaudeArgs wraps the SessionEnd hook's single argument.
type CaptureClaudeArgs struct {
	Cwd string `json:"cwd"`
}

// AuditRecordArgs carries a fully assembled audit row. The row's Surface is
// whatever the client recorded, NOT the writer's own surface: an agent call
// audited by a desktop process still has to read as `mcp` in the trail, or the
// trail stops answering "which surface sent this".
type AuditRecordArgs struct {
	Row db.AICallLog `json:"row"`
}

// bindingCatalogueMethods maps the App bindings (internal/app's
// bindingWrites registry) onto the catalogue method that executes their write.
// Several bindings share one catalogue method (the tool face is narrower than
// the UI face by design); a binding absent from this map is a human-gate write
// with no agent twin, and DecideWrite refuses it from a client process.
var bindingCatalogueMethods = map[string]string{
	"TriggerScan":        MethodScanRun,
	"CreateNote":         MethodNotesCreate, // the binding takes content only; the executor's Kind defaults inside CreateNoteWithMeta
	"CreateNoteWithMeta": MethodNotesCreate,
	"UpdateNote":         MethodNotesUpdate,
	"UpdateNoteFull":     MethodNotesUpdate,
	"UpdateNoteMeta":     MethodNotesUpdate,
	"CreateHandoff":      MethodHandoffCreate,
	"RunReviewNow":       MethodReviewRun,
	// CaptureClaudeHandoff and the import triggers are NOT here: they read
	// other tools' files off disk (ADR-0011 privacy gate) — the desktop is
	// the surface where that consent lives, so a client process refuses them
	// rather than forwarding.
}

// CatalogueMethodForBinding returns the catalogue method that runs this
// binding's write, or "" when the binding has none (human-gate write).
func CatalogueMethodForBinding(binding string) string {
	return bindingCatalogueMethods[binding]
}

// Methods is the catalogue's membership list, for the coverage test and for
// the log line at startup ("serving N write methods"). Order is fixed so two
// processes print the same thing.
func Methods() []string {
	return []string{
		MethodNotesCreate, MethodNotesUpdate, MethodHandoffCreate,
		MethodChatCreate, MethodChatAppend, MethodChatRename,
		MethodScanRun, MethodReviewRun, MethodCaptureClaude, MethodAuditRecord,
	}
}

// IsMethod reports whether name is in the catalogue.
func IsMethod(name string) bool {
	for _, m := range Methods() {
		if m == name {
			return true
		}
	}
	return false
}

// Executor runs catalogue methods against this process's Service. It is the
// server half; the Manager owns one and calls it directly when this process is
// the writer, so the elected agent has exactly one write path in both modes.
type Executor struct {
	svc *service.Service
	// scanMu is not needed: ScanNow serialises itself.
}

// NewExecutor binds the catalogue to a live Service.
func NewExecutor(svc *service.Service) *Executor { return &Executor{svc: svc} }

// Execute decodes args for method and runs it, returning the result value to
// serialise. The request's Surface is deliberately NOT read here: provenance
// travels in the args (HandoffCreateArgs.Source is what the caller intends to
// store), so the writer never invents or overwrites who wrote a record — it
// only logs the surface that asked (see Manager.handle).
func (e *Executor) Execute(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case MethodNotesCreate:
		var a NotesCreateArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return e.svc.CreateNoteWithMeta(a.ProjectID, a.Title, a.Content, a.Tags, a.Kind, a.Source)
	case MethodNotesUpdate:
		var a NotesUpdateArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := e.svc.UpdateNoteFull(a.NoteID, a.Content, a.Title, a.Tags, a.Kind, a.Pinned, a.ExpectedVersion); err != nil {
			return nil, err
		}
		// Echo the stored row: the tool returns the note after the write, and
		// re-reading here is the writer's read, which cannot be stale.
		return e.svc.GetNote(a.NoteID)
	case MethodHandoffCreate:
		var a HandoffCreateArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return e.svc.CreateHandoffNote(service.HandoffInput{
			ProjectID: a.ProjectID, Agent: a.Agent, Summary: a.Summary,
			Changes: a.Changes, Decisions: a.Decisions, Gotchas: a.Gotchas,
			NextSteps: a.NextSteps, Tags: a.Tags, Source: a.Source,
			WorkspacePath: a.WorkspacePath, Milestone: a.Milestone, SessionID: a.SessionID,
		})
	case MethodChatCreate:
		var a ChatCreateArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return e.svc.CreateChatThread(ctx, a.ID, a.Title, a.Path)
	case MethodChatAppend:
		var a ChatAppendArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return e.svc.AppendChatMessage(ctx, service.ChatAppendInput{
			ThreadID: a.ThreadID, ID: a.ID, Role: a.Role,
			Content: a.Content, Source: a.Source, ReplyTo: a.ReplyTo,
		})
	case MethodChatRename:
		var a ChatRenameArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return e.svc.RenameChatThread(ctx, a.ThreadID, a.Title)
	case MethodScanRun:
		// Seeding and scanning are one operation from the agent's point of
		// view, and both write; doing them together keeps a non-writer from
		// needing a second catalogue entry for a config row.
		e.svc.EnsureDefaultScanRoots()
		return e.svc.ScanNow(ctx)
	case MethodReviewRun:
		var a ReviewRunArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return e.svc.RunReview(ctx, service.ReviewOptions{
			Date: a.Date, Scope: a.Scope, DryRun: a.DryRun, Force: a.Force, NoAI: a.NoAI,
		})
	case MethodCaptureClaude:
		var a CaptureClaudeArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Cwd) == "" {
			return nil, fmt.Errorf("cwd is required")
		}
		return e.svc.CaptureClaudeSessionByCwd(a.Cwd)
	case MethodAuditRecord:
		var a AuditRecordArgs
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := e.svc.RecordAuditRow(a.Row); err != nil {
			return nil, err
		}
		return map[string]bool{"recorded": true}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, method)
	}
}

func decode(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		// invalid_argument, not internal: a malformed payload is the caller's
		// bug, and the code is what tells an agent to fix its request.
		return fmt.Errorf("bad %s arguments: %w", typeName(target), err)
	}
	return nil
}

func typeName(v any) string {
	t := fmt.Sprintf("%T", v)
	return strings.TrimPrefix(t, "*")
}

// remoteErr re-wraps an error that crossed the process boundary so
// errcode.Classify reproduces the writer's verdict instead of guessing from
// the message text.
func remoteErr(code, message string, retryable bool) error {
	if message == "" {
		message = "write failed"
	}
	return &errcode.Remote{Code: code, Message: message, Retryable: retryable}
}
