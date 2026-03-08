package store

import "time"

type ConversationEntry struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	OpenID    string    `json:"open_id,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type ExecutionRecord struct {
	ID               string     `json:"id"`
	RequestMessageID string     `json:"request_message_id"`
	Prompt           string     `json:"-"`
	Output           string     `json:"output,omitempty"`
	Status           string     `json:"status"`
	ErrorText        string     `json:"error_text,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type MessageRecord struct {
	MessageID      string     `json:"message_id"`
	ChatID         string     `json:"chat_id"`
	SenderOpenID   string     `json:"sender_open_id"`
	TextContent    string     `json:"text_content"`
	Status         string     `json:"status"`
	AckMessageID   string     `json:"ack_message_id,omitempty"`
	FinalMessageID string     `json:"final_message_id,omitempty"`
	ExecutionID    string     `json:"execution_id,omitempty"`
	ErrorText      string     `json:"error_text,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}
