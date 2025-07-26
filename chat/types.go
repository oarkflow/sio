package chat

import (
	"time"

	"github.com/oarkflow/sio/websocket"
)

// MessageType represents different types of chat messages
type MessageType string

const (
	JoinRoom       MessageType = "join_room"
	LeaveRoom      MessageType = "leave_room"
	Message        MessageType = "message"
	ThreadReply    MessageType = "thread_reply"
	Typing         MessageType = "typing"
	ReadReceipt    MessageType = "read_receipt"
	UserJoined     MessageType = "user_joined"
	UserLeft       MessageType = "user_left"
	TypingStart    MessageType = "typing_start"
	TypingStop     MessageType = "typing_stop"
	Acknowledgment MessageType = "ack"
	Error          MessageType = "error"
)

// RoomType represents different types of chat rooms
type RoomType string

const (
	PublicRoom RoomType = "public"
	GroupRoom  RoomType = "group"
	DMRoom     RoomType = "dm"
)

// MessageStatus represents the status of a message
type MessageStatus string

const (
	StatusSent      MessageStatus = "sent"
	StatusDelivered MessageStatus = "delivered"
	StatusRead      MessageStatus = "read"
)

// ChatMessage represents the main message envelope
type ChatMessage struct {
	Type      MessageType `json:"type"`
	RoomID    string      `json:"roomId"`
	MessageID string      `json:"messageId,omitempty"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}

// JoinRoomPayload for joining a room
type JoinRoomPayload struct {
	Password string `json:"password,omitempty"`
}

// LeaveRoomPayload for leaving a room
type LeaveRoomPayload struct {
	Reason string `json:"reason,omitempty"`
}

// MessagePayload for regular messages
type MessagePayload struct {
	Content   string `json:"content"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	MessageID string `json:"messageId"`
}

// ThreadReplyPayload for threaded replies
type ThreadReplyPayload struct {
	Content         string `json:"content"`
	UserID          string `json:"userId"`
	Username        string `json:"username"`
	MessageID       string `json:"messageId"`
	ParentMessageID string `json:"parentMessageId"`
}

// TypingPayload for typing indicators
type TypingPayload struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	IsTyping bool   `json:"isTyping"`
}

// ReadReceiptPayload for read receipts
type ReadReceiptPayload struct {
	UserID    string    `json:"userId"`
	MessageID string    `json:"messageId"`
	ReadAt    time.Time `json:"readAt"`
}

// UserPresencePayload for user presence updates
type UserPresencePayload struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Status   string `json:"status"` // online, offline, away
}

// AckPayload for acknowledgments
type AckPayload struct {
	MessageID string        `json:"messageId"`
	Status    MessageStatus `json:"status"`
	Error     string        `json:"error,omitempty"`
}

// ErrorPayload for error messages
type ErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Client represents a connected chat client
type Client struct {
	ID       string
	UserID   string
	Username string
	Conn     *websocket.Conn
	Rooms    map[string]bool
	LastSeen time.Time
	IsTyping map[string]bool // roomID -> isTyping
}

// Room represents a chat room
type Room struct {
	ID       string
	Type     RoomType
	Name     string
	Clients  map[string]*Client
	Metadata map[string]interface{}
	Created  time.Time
}

// DBMessage represents a message in the database
type DBMessage struct {
	ID              string    `json:"id" db:"id"`
	RoomID          string    `json:"roomId" db:"room_id"`
	SenderID        string    `json:"senderId" db:"sender_id"`
	Content         string    `json:"content" db:"content"`
	Timestamp       time.Time `json:"timestamp" db:"timestamp"`
	ParentMessageID *string   `json:"parentMessageId,omitempty" db:"parent_message_id"`
	MessageType     string    `json:"messageType" db:"message_type"`
}

// DBRoom represents a room in the database
type DBRoom struct {
	ID       string    `json:"id" db:"id"`
	Type     string    `json:"type" db:"type"`
	Name     string    `json:"name" db:"name"`
	Metadata string    `json:"metadata" db:"metadata"` // JSON string
	Created  time.Time `json:"created" db:"created_at"`
}

// DBRoomMember represents room membership
type DBRoomMember struct {
	RoomID   string    `json:"roomId" db:"room_id"`
	UserID   string    `json:"userId" db:"user_id"`
	Role     string    `json:"role" db:"role"` // member, admin, owner
	JoinedAt time.Time `json:"joinedAt" db:"joined_at"`
}

// DBReadReceipt represents read receipts
type DBReadReceipt struct {
	MessageID string    `json:"messageId" db:"message_id"`
	UserID    string    `json:"userId" db:"user_id"`
	ReadAt    time.Time `json:"readAt" db:"read_at"`
}

// Rate limiting types
type RateLimit struct {
	Requests    int
	WindowSize  time.Duration
	LastReset   time.Time
	CurrentUsed int
}

// Security configuration
type SecurityConfig struct {
	MaxMessageLength   int
	MaxRoomsPerUser    int
	AllowedOrigins     []string
	RequireAuth        bool
	RateLimitPerSocket *RateLimit
	RateLimitPerRoom   *RateLimit
}
