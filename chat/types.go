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
	FileShare      MessageType = "file_share"
	MediaShare     MessageType = "media_share"
	CallInvite     MessageType = "call_invite"
	CallAnswer     MessageType = "call_answer"
	CallEnd        MessageType = "call_end"
	WebRTCSignal   MessageType = "webrtc_signal"
	ScreenShare    MessageType = "screen_share"
	RecordingStart MessageType = "recording_start"
	RecordingStop  MessageType = "recording_stop"
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

// FileSharePayload for file sharing
type FileSharePayload struct {
	MessageID string      `json:"messageId"`
	UserID    string      `json:"userId"`
	Username  string      `json:"username"`
	FileName  string      `json:"fileName"`
	FileSize  int64       `json:"fileSize"`
	FileType  string      `json:"fileType"`
	FileData  interface{} `json:"fileData,omitempty"`
	FileURL   string      `json:"fileUrl,omitempty"`
	RoomID    string      `json:"roomId,omitempty"` // Add RoomID for upload context
}

// MediaSharePayload for media sharing (audio/video recordings)
type MediaSharePayload struct {
	MediaType string `json:"mediaType"`           // audio, video, screen
	MediaData string `json:"mediaData"`           // Base64 encoded
	Duration  int    `json:"duration"`            // Duration in seconds
	Thumbnail string `json:"thumbnail,omitempty"` // Base64 encoded thumbnail for videos
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	MessageID string `json:"messageId"`
}

// CallInvitePayload for video call invitations
type CallInvitePayload struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	CallType string `json:"callType"` // audio, video, screen
	RoomID   string `json:"roomId"`
}

// CallAnswerPayload for call responses
type CallAnswerPayload struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	CallID   string `json:"callId"`
	Accepted bool   `json:"accepted"`
}

// CallEndPayload for ending calls
type CallEndPayload struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	CallID   string `json:"callId"`
	Reason   string `json:"reason,omitempty"`
}

// WebRTCSignalPayload for WebRTC signaling
type WebRTCSignalPayload struct {
	UserID     string      `json:"userId"`
	Username   string      `json:"username"`
	Signal     interface{} `json:"signal"`     // SDP offer/answer or ICE candidate
	SignalType string      `json:"signalType"` // offer, answer, ice-candidate
}

// ScreenSharePayload for screen sharing
type ScreenSharePayload struct {
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	IsSharing bool   `json:"isSharing"`
	StreamID  string `json:"streamId,omitempty"`
}

// RecordingPayload for call recording
type RecordingPayload struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	IsRecording bool   `json:"isRecording"`
	RecordingID string `json:"recordingId,omitempty"`
}

// LocationPayload for location sharing
type LocationPayload struct {
	UserID    string  `json:"userId"`
	Username  string  `json:"username"`
	MessageID string  `json:"messageId"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Address   string  `json:"address,omitempty"`
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
	FileData        any       `json:"fileData,omitempty" db:"file_data"`
	FileName        *string   `json:"fileName,omitempty" db:"file_name"`
	FileSize        *int64    `json:"fileSize,omitempty" db:"file_size"`
	FileURL         *string   `json:"fileUrl,omitempty" db:"file_url"` // URL for file download
	FileType        *string   `json:"fileType,omitempty" db:"file_type"`
	MediaData       *string   `json:"mediaData,omitempty" db:"media_data"`
	MediaType       *string   `json:"mediaType,omitempty" db:"media_type"`
	Duration        *int      `json:"duration,omitempty" db:"duration"`
	Latitude        *float64  `json:"latitude,omitempty" db:"latitude"`
	Longitude       *float64  `json:"longitude,omitempty" db:"longitude"`
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
