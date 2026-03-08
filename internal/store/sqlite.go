package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
  chat_type TEXT NOT NULL DEFAULT '',
  message_type TEXT NOT NULL DEFAULT '',
  sender_open_id TEXT NOT NULL,
  text_content TEXT NOT NULL,
  raw_content_json TEXT NOT NULL DEFAULT '',
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
  content_type TEXT NOT NULL DEFAULT 'text',
  file_path TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS authorized_users (
  open_id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pairing_requests (
  open_id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  requested_at TEXT NOT NULL,
  handled_at TEXT
);

CREATE TABLE IF NOT EXISTS authorized_groups (
  chat_id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
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
	return s.migrateSchema()
}

func (s *SQLiteStore) migrateSchema() error {
	type columnDef struct {
		table        string
		name         string
		definition   string
		defaultValue string
	}

	columns := []columnDef{
		{table: "processed_messages", name: "chat_type", definition: "TEXT NOT NULL DEFAULT ''", defaultValue: ""},
		{table: "processed_messages", name: "message_type", definition: "TEXT NOT NULL DEFAULT ''", defaultValue: ""},
		{table: "processed_messages", name: "raw_content_json", definition: "TEXT NOT NULL DEFAULT ''", defaultValue: ""},
		{table: "conversation_entries", name: "content_type", definition: "TEXT NOT NULL DEFAULT 'text'", defaultValue: "text"},
		{table: "conversation_entries", name: "file_path", definition: "TEXT NOT NULL DEFAULT ''", defaultValue: ""},
	}

	for _, column := range columns {
		exists, err := s.hasColumn(column.table, column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.name, column.definition)); err != nil {
			return fmt.Errorf("add column %s.%s: %w", column.table, column.name, err)
		}
		if _, err := s.db.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s IS NULL", column.table, column.name, column.name), column.defaultValue); err != nil {
			return fmt.Errorf("backfill column %s.%s: %w", column.table, column.name, err)
		}
	}

	return nil
}

func (s *SQLiteStore) hasColumn(table string, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate table_info(%s): %w", table, err)
	}
	return false, nil
}

func (s *SQLiteStore) TryCreateInbound(ctx context.Context, record MessageRecord) (bool, error) {
	now := record.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO processed_messages (
  message_id, chat_id, chat_type, message_type, sender_open_id, text_content, raw_content_json, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, record.MessageID, record.ChatID, record.ChatType, record.MessageType, record.SenderOpenID, record.TextContent, record.RawContentJSON, record.Status, now, now)
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
INSERT INTO conversation_entries (source, open_id, message_id, content, content_type, file_path, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
`, entry.Source, entry.OpenID, entry.MessageID, entry.Content, normalizeContentType(entry.ContentType), entry.FilePath, entry.CreatedAt.UTC().Format(time.RFC3339Nano))
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
SELECT id, source, open_id, message_id, content, content_type, file_path, created_at
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
		if err := rows.Scan(&entry.ID, &entry.Source, &entry.OpenID, &entry.MessageID, &entry.Content, &entry.ContentType, &entry.FilePath, &createdAt); err != nil {
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

func (s *SQLiteStore) EnsureAuthorizedUser(ctx context.Context, openID string) error {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO authorized_users (open_id, created_at)
VALUES (?, ?)
ON CONFLICT(open_id) DO NOTHING
`, openID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("ensure authorized user: %w", err)
	}
	return nil
}

func (s *SQLiteStore) IsAuthorizedUser(ctx context.Context, openID string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT 1
FROM authorized_users
WHERE open_id = ?
LIMIT 1
`, openID)

	var flag int
	if err := row.Scan(&flag); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check authorized user: %w", err)
	}
	return true, nil
}

func (s *SQLiteStore) CreateOrRefreshPairingRequest(ctx context.Context, openID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO pairing_requests (open_id, status, requested_at, handled_at)
VALUES (?, 'pending', ?, NULL)
ON CONFLICT(open_id) DO UPDATE SET
  status = 'pending',
  requested_at = excluded.requested_at,
  handled_at = NULL
`, openID, now)
	if err != nil {
		return fmt.Errorf("create pairing request: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListPendingPairingRequests(ctx context.Context) ([]PairingRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT open_id, status, requested_at, handled_at
FROM pairing_requests
WHERE status = 'pending'
ORDER BY requested_at ASC
`)
	if err != nil {
		return nil, fmt.Errorf("list pairing requests: %w", err)
	}
	defer rows.Close()

	var items []PairingRequest
	for rows.Next() {
		var item PairingRequest
		var requestedAt string
		var handledAt sql.NullString
		if err := rows.Scan(&item.OpenID, &item.Status, &requestedAt, &handledAt); err != nil {
			return nil, fmt.Errorf("scan pairing request: %w", err)
		}
		item.RequestedAt = parseTimestamp(requestedAt)
		if handledAt.Valid {
			ts := parseTimestamp(handledAt.String)
			item.HandledAt = &ts
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pairing requests: %w", err)
	}
	return items, nil
}

func (s *SQLiteStore) SetPairingRequestStatus(ctx context.Context, openID string, status string) error {
	if !slices.Contains([]string{"approved", "rejected"}, status) {
		return fmt.Errorf("unsupported pairing status: %s", status)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
UPDATE pairing_requests
SET status = ?, handled_at = ?
WHERE open_id = ?
`, status, now, openID)
	if err != nil {
		return fmt.Errorf("update pairing request status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("pairing request rows affected: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) EnsureAuthorizedGroup(ctx context.Context, chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO authorized_groups (chat_id, created_at)
VALUES (?, ?)
ON CONFLICT(chat_id) DO NOTHING
`, chatID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("ensure authorized group: %w", err)
	}
	return nil
}

func (s *SQLiteStore) IsAuthorizedGroup(ctx context.Context, chatID string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT 1
FROM authorized_groups
WHERE chat_id = ?
LIMIT 1
`, chatID)

	var flag int
	if err := row.Scan(&flag); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check authorized group: %w", err)
	}
	return true, nil
}

func (s *SQLiteStore) RemoveAuthorizedGroup(ctx context.Context, chatID string) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM authorized_groups
WHERE chat_id = ?
`, chatID)
	if err != nil {
		return fmt.Errorf("remove authorized group: %w", err)
	}
	return nil
}

func normalizeContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return "text"
	}
	return contentType
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
