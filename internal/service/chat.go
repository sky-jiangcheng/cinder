package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxChatTitleLen   = 160 // Unicode code points, not UTF-8 bytes
	MaxChatContentLen = 1 << 20
	MaxChatPathLen    = 4096
	chatThreadLimit   = 100
	chatMessageLimit  = 500
)

var (
	ErrChatThreadNotFound = errors.New("chat thread not found")
	ErrChatConflict       = errors.New("chat id already exists with a different payload")
	chatUUID              = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// ChatThread and ChatMessage are transport-neutral, persisted conversation
// records. They are never converted to notes, search evidence or review items.
type ChatThread struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Path      string `json:"path"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type ChatMessage struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	Content   string  `json:"content"`
	Source    string  `json:"source"`
	ReplyTo   *string `json:"replyTo"`
	CreatedAt string  `json:"createdAt"`
}

type ChatReadResult struct {
	Thread   ChatThread    `json:"thread"`
	Messages []ChatMessage `json:"messages"`
}

type ChatAppendInput struct {
	ThreadID string
	ID       string
	Role     string
	Content  string
	Source   string
	ReplyTo  *string
}

func validateChatID(name, id string) error {
	if !chatUUID.MatchString(id) {
		return fmt.Errorf("%s must be a UUID", name)
	}
	return nil
}

// Only message IDs accept the deterministic suffix used by clients for replies.
// Thread IDs and reply_to references keep the plain UUID contract.
func validateChatMessageID(id string) error {
	if !chatUUID.MatchString(strings.TrimSuffix(id, "-reply")) {
		return fmt.Errorf("id must be a UUID or UUID-reply")
	}
	return nil
}

func validateChatTitle(title string) error {
	if !utf8.ValidString(title) || strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > MaxChatTitleLen || strings.ContainsFunc(title, unicode.IsControl) {
		return fmt.Errorf("title must be nonblank UTF-8 text without control characters, at most %d Unicode characters", MaxChatTitleLen)
	}
	return nil
}

// Empty means an unscoped conversation (or an unfiltered list/metrics request).
// Paths are labels, not permission to read disk; no Stat/EvalSymlinks is needed.
func validateChatPath(path string) error {
	if !utf8.ValidString(path) || len(path) > MaxChatPathLen || strings.ContainsFunc(path, unicode.IsControl) || (path != "" && (!filepath.IsAbs(path) || strings.TrimSpace(path) != path)) {
		return fmt.Errorf("path must be empty or an absolute UTF-8 path without control characters, at most %d bytes", MaxChatPathLen)
	}
	return nil
}

func chatTimestamp() string {
	// Fixed precision keeps SQLite's lexical timestamp ordering chronological.
	return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
}

const chatThreadColumns = "id, title, path, created_at, updated_at"
const chatMessageColumns = "id, role, content, source, reply_to, created_at"

type chatScanner interface{ Scan(...any) error }

func scanChatThread(row chatScanner) (ChatThread, error) {
	var t ChatThread
	err := row.Scan(&t.ID, &t.Title, &t.Path, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrChatThreadNotFound
	}
	return t, err
}

func scanChatMessage(row chatScanner) (ChatMessage, error) {
	var m ChatMessage
	err := row.Scan(&m.ID, &m.Role, &m.Content, &m.Source, &m.ReplyTo, &m.CreatedAt)
	return m, err
}

func (s *Service) ListChatThreads(ctx context.Context, path string) ([]ChatThread, error) {
	if err := validateChatPath(path); err != nil {
		return nil, err
	}
	query := "SELECT " + chatThreadColumns + " FROM chat_threads"
	args := []any{}
	if path != "" {
		query += " WHERE path = ?"
		args = append(args, path)
	}
	query += " ORDER BY updated_at DESC, id ASC LIMIT ?"
	args = append(args, chatThreadLimit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatThread{}
	for rows.Next() {
		t, err := scanChatThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) CreateChatThread(ctx context.Context, id, title, path string) (*ChatThread, error) {
	if err := validateChatID("id", id); err != nil {
		return nil, err
	}
	if err := validateChatTitle(title); err != nil {
		return nil, err
	}
	if err := validateChatPath(path); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stamp := chatTimestamp()
	// Write FIRST: SQLite acquires the writer lock before the idempotency read.
	// A read-then-write deferred transaction races across MCP processes in WAL
	// mode (SQLITE_BUSY_SNAPSHOT), even when each process has a one-connection pool.
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_threads(id, title, creation_title, path, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, id, title, title, path, stamp, stamp)
	if err != nil {
		return nil, err
	}
	t, err := scanChatThread(tx.QueryRowContext(ctx, "SELECT "+chatThreadColumns+" FROM chat_threads WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	// Remember the original create payload so retrying create after rename is
	// still idempotent, without reverting the human's new title.
	var originalTitle string
	if err := tx.QueryRowContext(ctx, "SELECT creation_title FROM chat_threads WHERE id = ?", id).Scan(&originalTitle); err != nil {
		return nil, err
	}
	if originalTitle != title || t.Path != path {
		return nil, ErrChatConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) ReadChatThread(ctx context.Context, id string) (*ChatReadResult, error) {
	if err := validateChatID("thread_id", id); err != nil {
		return nil, err
	}
	// A read transaction gives the thread and its messages the same snapshot.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t, err := scanChatThread(tx.QueryRowContext(ctx, "SELECT "+chatThreadColumns+" FROM chat_threads WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+chatMessageColumns+" FROM (SELECT * FROM chat_messages WHERE thread_id = ? ORDER BY seq DESC LIMIT ?) ORDER BY seq ASC", id, chatMessageLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &ChatReadResult{Thread: t, Messages: []ChatMessage{}}
	for rows.Next() {
		m, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) AppendChatMessage(ctx context.Context, in ChatAppendInput) (*ChatMessage, error) {
	if err := validateChatID("thread_id", in.ThreadID); err != nil {
		return nil, err
	}
	if err := validateChatMessageID(in.ID); err != nil {
		return nil, err
	}
	if in.Role != "user" && in.Role != "assistant" {
		return nil, fmt.Errorf("role must be user or assistant")
	}
	if in.Source != "warden" && in.Source != "vscode" && in.Source != "jetbrains" {
		return nil, fmt.Errorf("source must be warden, vscode or jetbrains")
	}
	if !utf8.ValidString(in.Content) || strings.TrimSpace(in.Content) == "" || len(in.Content) > MaxChatContentLen || strings.ContainsRune(in.Content, '\x00') {
		return nil, fmt.Errorf("content must be nonblank UTF-8 text without NUL, at most %d bytes", MaxChatContentLen)
	}
	if in.ReplyTo != nil {
		if err := validateChatID("reply_to", *in.ReplyTo); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Acquire SQLite's cross-process writer lock before ANY reads. This no-op
	// deliberately does not advance updated_at on retries or rejected appends.
	result, err := tx.ExecContext(ctx, "UPDATE chat_threads SET updated_at = updated_at WHERE id = ?", in.ThreadID)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrChatThreadNotFound
	}
	var owner string
	err = tx.QueryRowContext(ctx, "SELECT thread_id FROM chat_messages WHERE id = ?", in.ID).Scan(&owner)
	if err == nil {
		m, err := scanChatMessage(tx.QueryRowContext(ctx, "SELECT "+chatMessageColumns+" FROM chat_messages WHERE id = ?", in.ID))
		if err != nil {
			return nil, err
		}
		sameReply := m.ReplyTo == nil && in.ReplyTo == nil || m.ReplyTo != nil && in.ReplyTo != nil && *m.ReplyTo == *in.ReplyTo
		if owner != in.ThreadID || m.Role != in.Role || m.Content != in.Content || m.Source != in.Source || !sameReply {
			return nil, ErrChatConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &m, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if in.ReplyTo != nil {
		var found int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM chat_messages WHERE id = ? AND thread_id = ? AND role = 'user'", *in.ReplyTo, in.ThreadID).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("reply_to must reference a user message in the same thread")
		}
		if err != nil {
			return nil, err
		}
	}
	stamp := chatTimestamp()
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_messages(id, thread_id, role, content, source, reply_to, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, in.ID, in.ThreadID, in.Role, in.Content, in.Source, in.ReplyTo, stamp)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE chat_threads SET updated_at = MAX(updated_at, ?) WHERE id = ?", stamp, in.ThreadID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ChatMessage{ID: in.ID, Role: in.Role, Content: in.Content, Source: in.Source, ReplyTo: in.ReplyTo, CreatedAt: stamp}, nil
}

func (s *Service) RenameChatThread(ctx context.Context, id, title string) (*ChatThread, error) {
	if err := validateChatID("thread_id", id); err != nil {
		return nil, err
	}
	if err := validateChatTitle(title); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE chat_threads SET title = ?, updated_at = MAX(updated_at, ?) WHERE id = ?", title, chatTimestamp(), id); err != nil {
		return nil, err
	}
	t, err := scanChatThread(tx.QueryRowContext(ctx, "SELECT "+chatThreadColumns+" FROM chat_threads WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &t, nil
}
