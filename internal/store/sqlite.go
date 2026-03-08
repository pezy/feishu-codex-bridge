package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) initSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS processed_messages (
  message_id TEXT PRIMARY KEY,
  chat_id TEXT NOT NULL,
  sender_open_id TEXT NOT NULL,
  text_content TEXT NOT NULL,
  status TEXT NOT NULL,
  ack_message_id TEXT,
  final_message_id TEXT,
  execution_id TEXT,
  error_text TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT
);

CREATE TABLE IF NOT EXISTS conversation_entries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source TEXT NOT NULL,
  open_id TEXT,
  message_id TEXT,
  content TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS executions (
  id TEXT PRIMARY KEY,
  request_message_id TEXT NOT NULL UNIQUE,
  prompt TEXT NOT NULL,
  output TEXT,
  status TEXT NOT NULL,
  error_text TEXT,
  started_at TEXT NOT NULL,
  completed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_conversation_entries_created_at
  ON conversation_entries(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_executions_started_at
  ON executions(started_at DESC);
`

	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("init sqlite schema: %w", err)
	}
	return nil
}

func (s *SQLiteStore) TryCreateInbound(ctx context.Context, record MessageRecord) (bool, error) {
	now := record.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO processed_messages (
  message_id, chat_id, sender_open_id, text_content, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
`, record.MessageID, record.ChatID, record.SenderOpenID, record.TextContent, record.Status, now, now)
	if err == nil {
		return true, nil
	}
	if isSQLitePrimaryKey(err) {
		return false, nil
	}
	return false, fmt.Errorf("insert inbound message: %w", err)
}

func (s *SQLiteStore) AppendConversation(ctx context.Context, entry ConversationEntry) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO conversation_entries (source, open_id, message_id, content, created_at)
VALUES (?, ?, ?, ?, ?)
`, entry.Source, entry.OpenID, entry.MessageID, entry.Content, entry.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("append conversation: %w", err)
	}
	return nil
}

func (s *SQLiteStore) CreateExecution(ctx context.Context, record ExecutionRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO executions (id, request_message_id, prompt, status, started_at)
VALUES (?, ?, ?, ?, ?)
`, record.ID, record.RequestMessageID, record.Prompt, record.Status, record.StartedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create execution: %w", err)
	}
	return nil
}

func (s *SQLiteStore) FinishExecution(ctx context.Context, record ExecutionRecord) error {
	var completedAt string
	if record.CompletedAt != nil {
		completedAt = record.CompletedAt.UTC().Format(time.RFC3339Nano)
	}

	_, err := s.db.ExecContext(ctx, `
UPDATE executions
SET output = ?, status = ?, error_text = ?, completed_at = ?
WHERE id = ?
`, record.Output, record.Status, record.ErrorText, completedAt, record.ID)
	if err != nil {
		return fmt.Errorf("finish execution: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateMessageState(
	ctx context.Context,
	messageID string,
	status string,
	ackMessageID string,
	finalMessageID string,
	executionID string,
	errorText string,
	completedAt *time.Time,
) error {
	var completed string
	if completedAt != nil {
		completed = completedAt.UTC().Format(time.RFC3339Nano)
	}

	_, err := s.db.ExecContext(ctx, `
UPDATE processed_messages
SET status = ?,
    ack_message_id = COALESCE(NULLIF(?, ''), ack_message_id),
    final_message_id = COALESCE(NULLIF(?, ''), final_message_id),
    execution_id = COALESCE(NULLIF(?, ''), execution_id),
    error_text = ?,
    updated_at = ?,
    completed_at = COALESCE(NULLIF(?, ''), completed_at)
WHERE message_id = ?
`, status, ackMessageID, finalMessageID, executionID, errorText, time.Now().UTC().Format(time.RFC3339Nano), completed, messageID)
	if err != nil {
		return fmt.Errorf("update message state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) RecentConversations(ctx context.Context, limit int) ([]ConversationEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, source, open_id, message_id, content, created_at
FROM conversation_entries
ORDER BY created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent conversations: %w", err)
	}
	defer rows.Close()

	var entries []ConversationEntry
	for rows.Next() {
		var entry ConversationEntry
		var createdAt string
		if err := rows.Scan(&entry.ID, &entry.Source, &entry.OpenID, &entry.MessageID, &entry.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		entry.CreatedAt = parseTimestamp(createdAt)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent conversations: %w", err)
	}

	reverse(entries)
	return entries, nil
}

func (s *SQLiteStore) LastExecution(ctx context.Context) (*ExecutionRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, request_message_id, output, status, error_text, started_at, completed_at
FROM executions
ORDER BY started_at DESC
LIMIT 1
`)

	var record ExecutionRecord
	var startedAt string
	var output sql.NullString
	var errorText sql.NullString
	var completedAt sql.NullString
	if err := row.Scan(
		&record.ID,
		&record.RequestMessageID,
		&output,
		&record.Status,
		&errorText,
		&startedAt,
		&completedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load last execution: %w", err)
	}

	record.StartedAt = parseTimestamp(startedAt)
	if output.Valid {
		record.Output = output.String
	}
	if errorText.Valid {
		record.ErrorText = errorText.String
	}
	if completedAt.Valid {
		ts := parseTimestamp(completedAt.String)
		record.CompletedAt = &ts
	}
	return &record, nil
}

func parseTimestamp(value string) time.Time {
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func reverse(entries []ConversationEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}

func isSQLitePrimaryKey(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "PRIMARY KEY"))
}
