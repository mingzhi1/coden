package protocol

// MessageListParams selects messages for a session.
type MessageListParams struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type Message struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at_unix_nano"`
}
