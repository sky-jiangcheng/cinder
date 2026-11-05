package db

import "database/sql"

// chatSchema is deliberately disconnected from notes, wiki pages, FTS and
// embedding queues. A conversation is not knowledge and has no review state.
var chatSchema = []string{
	`CREATE TABLE IF NOT EXISTS chat_threads (
		id TEXT PRIMARY KEY NOT NULL,
		title TEXT NOT NULL,
		creation_title TEXT NOT NULL,
		path TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_chat_threads_updated ON chat_threads(updated_at DESC, id)`,
	`CREATE INDEX IF NOT EXISTS idx_chat_threads_path_updated ON chat_threads(path, updated_at DESC, id)`,
	`CREATE TABLE IF NOT EXISTS chat_messages (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL UNIQUE,
		thread_id TEXT NOT NULL REFERENCES chat_threads(id),
		role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
		content TEXT NOT NULL,
		source TEXT NOT NULL CHECK (source IN ('warden', 'vscode', 'jetbrains')),
		reply_to TEXT REFERENCES chat_messages(id),
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_chat_messages_thread_seq ON chat_messages(thread_id, seq DESC)`,
	`CREATE TRIGGER IF NOT EXISTS chat_messages_reply_guard BEFORE INSERT ON chat_messages
	 WHEN new.reply_to IS NOT NULL AND NOT EXISTS (
		SELECT 1 FROM chat_messages WHERE id = new.reply_to AND thread_id = new.thread_id AND role = 'user'
	 ) BEGIN SELECT RAISE(ABORT, 'reply_to must reference a user message in the same thread'); END`,
	`CREATE TRIGGER IF NOT EXISTS chat_messages_no_update BEFORE UPDATE ON chat_messages
	 BEGIN SELECT RAISE(ABORT, 'chat messages are immutable'); END`,
}

// EnsureChatSchema is migration v29. It is additive and transactional; unlike
// derived knowledge, persisted conversations are never rebuilt or backfilled.
func EnsureChatSchema(database *sql.DB) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range chatSchema {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
