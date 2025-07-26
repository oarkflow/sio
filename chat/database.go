package chat

import (
	"context"
	"database/sql"
	"fmt"
)

// Database interface for chat operations
type Database interface {
	// Message operations
	SaveMessage(ctx context.Context, message *DBMessage) error
	GetMessages(ctx context.Context, roomID string, limit, offset int) ([]*DBMessage, error)
	GetThreadMessages(ctx context.Context, parentMessageID string) ([]*DBMessage, error)
	GetMessage(ctx context.Context, messageID string) (*DBMessage, error)

	// Room operations
	CreateRoom(ctx context.Context, room *DBRoom) error
	GetRoom(ctx context.Context, roomID string) (*DBRoom, error)
	GetUserRooms(ctx context.Context, userID string) ([]*DBRoom, error)
	DeleteRoom(ctx context.Context, roomID string) error

	// Room membership operations
	AddRoomMember(ctx context.Context, member *DBRoomMember) error
	RemoveRoomMember(ctx context.Context, roomID, userID string) error
	GetRoomMembers(ctx context.Context, roomID string) ([]*DBRoomMember, error)
	IsUserInRoom(ctx context.Context, roomID, userID string) (bool, error)

	// Read receipt operations
	SaveReadReceipt(ctx context.Context, receipt *DBReadReceipt) error
	GetReadReceipts(ctx context.Context, messageID string) ([]*DBReadReceipt, error)
	GetUserReadReceipts(ctx context.Context, userID, roomID string) ([]*DBReadReceipt, error)

	// Utility operations
	Close() error
}

// PostgreSQLDB implements the Database interface using PostgreSQL
type PostgreSQLDB struct {
	db *sql.DB
}

// NewPostgreSQLDB creates a new PostgreSQL database connection
func NewPostgreSQLDB(connectionString string) (*PostgreSQLDB, error) {
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	pgDB := &PostgreSQLDB{db: db}

	// Initialize database schema
	if err := pgDB.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return pgDB, nil
}

// initSchema creates the necessary tables
func (p *PostgreSQLDB) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS rooms (
			id VARCHAR(255) PRIMARY KEY,
			type VARCHAR(50) NOT NULL,
			name VARCHAR(255) NOT NULL,
			metadata JSONB,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id VARCHAR(255) PRIMARY KEY,
			room_id VARCHAR(255) NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			sender_id VARCHAR(255) NOT NULL,
			content TEXT NOT NULL,
			timestamp TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			parent_message_id VARCHAR(255) REFERENCES messages(id) ON DELETE CASCADE,
			message_type VARCHAR(50) DEFAULT 'message'
		)`,
		`CREATE TABLE IF NOT EXISTS room_members (
			room_id VARCHAR(255) NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			user_id VARCHAR(255) NOT NULL,
			role VARCHAR(50) DEFAULT 'member',
			joined_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			PRIMARY KEY (room_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS read_receipts (
			message_id VARCHAR(255) NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			user_id VARCHAR(255) NOT NULL,
			read_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			PRIMARY KEY (message_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_room_id ON messages(room_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_parent_id ON messages(parent_message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_room_members_user_id ON room_members(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_read_receipts_user_id ON read_receipts(user_id)`,
	}

	for _, query := range queries {
		if _, err := p.db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %s, error: %w", query, err)
		}
	}

	return nil
}

// SaveMessage saves a message to the database
func (p *PostgreSQLDB) SaveMessage(ctx context.Context, message *DBMessage) error {
	query := `
		INSERT INTO messages (id, room_id, sender_id, content, timestamp, parent_message_id, message_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := p.db.ExecContext(ctx, query,
		message.ID, message.RoomID, message.SenderID, message.Content,
		message.Timestamp, message.ParentMessageID, message.MessageType)
	return err
}

// GetMessages retrieves messages from a room with pagination
func (p *PostgreSQLDB) GetMessages(ctx context.Context, roomID string, limit, offset int) ([]*DBMessage, error) {
	query := `
		SELECT id, room_id, sender_id, content, timestamp, parent_message_id, message_type
		FROM messages
		WHERE room_id = $1
		ORDER BY timestamp DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := p.db.QueryContext(ctx, query, roomID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*DBMessage
	for rows.Next() {
		message := &DBMessage{}
		err := rows.Scan(&message.ID, &message.RoomID, &message.SenderID,
			&message.Content, &message.Timestamp, &message.ParentMessageID, &message.MessageType)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}

	return messages, rows.Err()
}

// GetThreadMessages retrieves all replies to a parent message
func (p *PostgreSQLDB) GetThreadMessages(ctx context.Context, parentMessageID string) ([]*DBMessage, error) {
	query := `
		SELECT id, room_id, sender_id, content, timestamp, parent_message_id, message_type
		FROM messages
		WHERE parent_message_id = $1
		ORDER BY timestamp ASC
	`
	rows, err := p.db.QueryContext(ctx, query, parentMessageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*DBMessage
	for rows.Next() {
		message := &DBMessage{}
		err := rows.Scan(&message.ID, &message.RoomID, &message.SenderID,
			&message.Content, &message.Timestamp, &message.ParentMessageID, &message.MessageType)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}

	return messages, rows.Err()
}

// GetMessage retrieves a specific message
func (p *PostgreSQLDB) GetMessage(ctx context.Context, messageID string) (*DBMessage, error) {
	query := `
		SELECT id, room_id, sender_id, content, timestamp, parent_message_id, message_type
		FROM messages
		WHERE id = $1
	`
	message := &DBMessage{}
	err := p.db.QueryRowContext(ctx, query, messageID).Scan(
		&message.ID, &message.RoomID, &message.SenderID,
		&message.Content, &message.Timestamp, &message.ParentMessageID, &message.MessageType)

	if err != nil {
		return nil, err
	}
	return message, nil
}

// CreateRoom creates a new room
func (p *PostgreSQLDB) CreateRoom(ctx context.Context, room *DBRoom) error {
	query := `
		INSERT INTO rooms (id, type, name, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := p.db.ExecContext(ctx, query,
		room.ID, room.Type, room.Name, room.Metadata, room.Created)
	return err
}

// GetRoom retrieves a room by ID
func (p *PostgreSQLDB) GetRoom(ctx context.Context, roomID string) (*DBRoom, error) {
	query := `
		SELECT id, type, name, metadata, created_at
		FROM rooms
		WHERE id = $1
	`
	room := &DBRoom{}
	err := p.db.QueryRowContext(ctx, query, roomID).Scan(
		&room.ID, &room.Type, &room.Name, &room.Metadata, &room.Created)

	if err != nil {
		return nil, err
	}
	return room, nil
}

// GetUserRooms retrieves all rooms a user is a member of
func (p *PostgreSQLDB) GetUserRooms(ctx context.Context, userID string) ([]*DBRoom, error) {
	query := `
		SELECT r.id, r.type, r.name, r.metadata, r.created_at
		FROM rooms r
		JOIN room_members rm ON r.id = rm.room_id
		WHERE rm.user_id = $1
		ORDER BY r.created_at DESC
	`
	rows, err := p.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []*DBRoom
	for rows.Next() {
		room := &DBRoom{}
		err := rows.Scan(&room.ID, &room.Type, &room.Name, &room.Metadata, &room.Created)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}

	return rooms, rows.Err()
}

// DeleteRoom deletes a room
func (p *PostgreSQLDB) DeleteRoom(ctx context.Context, roomID string) error {
	query := `DELETE FROM rooms WHERE id = $1`
	_, err := p.db.ExecContext(ctx, query, roomID)
	return err
}

// AddRoomMember adds a user to a room
func (p *PostgreSQLDB) AddRoomMember(ctx context.Context, member *DBRoomMember) error {
	query := `
		INSERT INTO room_members (room_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (room_id, user_id) DO UPDATE SET
		role = EXCLUDED.role,
		joined_at = EXCLUDED.joined_at
	`
	_, err := p.db.ExecContext(ctx, query,
		member.RoomID, member.UserID, member.Role, member.JoinedAt)
	return err
}

// RemoveRoomMember removes a user from a room
func (p *PostgreSQLDB) RemoveRoomMember(ctx context.Context, roomID, userID string) error {
	query := `DELETE FROM room_members WHERE room_id = $1 AND user_id = $2`
	_, err := p.db.ExecContext(ctx, query, roomID, userID)
	return err
}

// GetRoomMembers retrieves all members of a room
func (p *PostgreSQLDB) GetRoomMembers(ctx context.Context, roomID string) ([]*DBRoomMember, error) {
	query := `
		SELECT room_id, user_id, role, joined_at
		FROM room_members
		WHERE room_id = $1
		ORDER BY joined_at ASC
	`
	rows, err := p.db.QueryContext(ctx, query, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*DBRoomMember
	for rows.Next() {
		member := &DBRoomMember{}
		err := rows.Scan(&member.RoomID, &member.UserID, &member.Role, &member.JoinedAt)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}

	return members, rows.Err()
}

// IsUserInRoom checks if a user is a member of a room
func (p *PostgreSQLDB) IsUserInRoom(ctx context.Context, roomID, userID string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM room_members WHERE room_id = $1 AND user_id = $2)`
	var exists bool
	err := p.db.QueryRowContext(ctx, query, roomID, userID).Scan(&exists)
	return exists, err
}

// SaveReadReceipt saves a read receipt
func (p *PostgreSQLDB) SaveReadReceipt(ctx context.Context, receipt *DBReadReceipt) error {
	query := `
		INSERT INTO read_receipts (message_id, user_id, read_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (message_id, user_id) DO UPDATE SET
		read_at = EXCLUDED.read_at
	`
	_, err := p.db.ExecContext(ctx, query,
		receipt.MessageID, receipt.UserID, receipt.ReadAt)
	return err
}

// GetReadReceipts retrieves all read receipts for a message
func (p *PostgreSQLDB) GetReadReceipts(ctx context.Context, messageID string) ([]*DBReadReceipt, error) {
	query := `
		SELECT message_id, user_id, read_at
		FROM read_receipts
		WHERE message_id = $1
		ORDER BY read_at ASC
	`
	rows, err := p.db.QueryContext(ctx, query, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var receipts []*DBReadReceipt
	for rows.Next() {
		receipt := &DBReadReceipt{}
		err := rows.Scan(&receipt.MessageID, &receipt.UserID, &receipt.ReadAt)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}

	return receipts, rows.Err()
}

// GetUserReadReceipts retrieves read receipts for a user in a room
func (p *PostgreSQLDB) GetUserReadReceipts(ctx context.Context, userID, roomID string) ([]*DBReadReceipt, error) {
	query := `
		SELECT rr.message_id, rr.user_id, rr.read_at
		FROM read_receipts rr
		JOIN messages m ON rr.message_id = m.id
		WHERE rr.user_id = $1 AND m.room_id = $2
		ORDER BY rr.read_at DESC
	`
	rows, err := p.db.QueryContext(ctx, query, userID, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var receipts []*DBReadReceipt
	for rows.Next() {
		receipt := &DBReadReceipt{}
		err := rows.Scan(&receipt.MessageID, &receipt.UserID, &receipt.ReadAt)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}

	return receipts, rows.Err()
}

// Close closes the database connection
func (p *PostgreSQLDB) Close() error {
	return p.db.Close()
}

// InMemoryDB implements the Database interface using in-memory storage
type InMemoryDB struct {
	messages     map[string]*DBMessage
	rooms        map[string]*DBRoom
	roomMembers  map[string]map[string]*DBRoomMember  // roomID -> userID -> member
	readReceipts map[string]map[string]*DBReadReceipt // messageID -> userID -> receipt
	userRooms    map[string]map[string]bool           // userID -> roomID -> exists
}

// NewInMemoryDB creates a new in-memory database
func NewInMemoryDB() *InMemoryDB {
	return &InMemoryDB{
		messages:     make(map[string]*DBMessage),
		rooms:        make(map[string]*DBRoom),
		roomMembers:  make(map[string]map[string]*DBRoomMember),
		readReceipts: make(map[string]map[string]*DBReadReceipt),
		userRooms:    make(map[string]map[string]bool),
	}
}

// Implementation of Database interface methods for InMemoryDB
// (This is a simplified implementation for development/testing)

func (m *InMemoryDB) SaveMessage(ctx context.Context, message *DBMessage) error {
	m.messages[message.ID] = message
	return nil
}

func (m *InMemoryDB) GetMessages(ctx context.Context, roomID string, limit, offset int) ([]*DBMessage, error) {
	var messages []*DBMessage
	for _, msg := range m.messages {
		if msg.RoomID == roomID {
			messages = append(messages, msg)
		}
	}

	// Simple pagination (in real implementation, you'd sort by timestamp)
	start := offset
	end := offset + limit
	if start > len(messages) {
		return []*DBMessage{}, nil
	}
	if end > len(messages) {
		end = len(messages)
	}

	return messages[start:end], nil
}

func (m *InMemoryDB) GetThreadMessages(ctx context.Context, parentMessageID string) ([]*DBMessage, error) {
	var messages []*DBMessage
	for _, msg := range m.messages {
		if msg.ParentMessageID != nil && *msg.ParentMessageID == parentMessageID {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

func (m *InMemoryDB) GetMessage(ctx context.Context, messageID string) (*DBMessage, error) {
	if msg, exists := m.messages[messageID]; exists {
		return msg, nil
	}
	return nil, fmt.Errorf("message not found")
}

func (m *InMemoryDB) CreateRoom(ctx context.Context, room *DBRoom) error {
	m.rooms[room.ID] = room
	m.roomMembers[room.ID] = make(map[string]*DBRoomMember)
	return nil
}

func (m *InMemoryDB) GetRoom(ctx context.Context, roomID string) (*DBRoom, error) {
	if room, exists := m.rooms[roomID]; exists {
		return room, nil
	}
	return nil, fmt.Errorf("room not found")
}

func (m *InMemoryDB) GetUserRooms(ctx context.Context, userID string) ([]*DBRoom, error) {
	var rooms []*DBRoom
	if userRooms, exists := m.userRooms[userID]; exists {
		for roomID := range userRooms {
			if room, exists := m.rooms[roomID]; exists {
				rooms = append(rooms, room)
			}
		}
	}
	return rooms, nil
}

func (m *InMemoryDB) DeleteRoom(ctx context.Context, roomID string) error {
	delete(m.rooms, roomID)
	delete(m.roomMembers, roomID)
	return nil
}

func (m *InMemoryDB) AddRoomMember(ctx context.Context, member *DBRoomMember) error {
	if _, exists := m.roomMembers[member.RoomID]; !exists {
		m.roomMembers[member.RoomID] = make(map[string]*DBRoomMember)
	}
	m.roomMembers[member.RoomID][member.UserID] = member

	if _, exists := m.userRooms[member.UserID]; !exists {
		m.userRooms[member.UserID] = make(map[string]bool)
	}
	m.userRooms[member.UserID][member.RoomID] = true

	return nil
}

func (m *InMemoryDB) RemoveRoomMember(ctx context.Context, roomID, userID string) error {
	if roomMembers, exists := m.roomMembers[roomID]; exists {
		delete(roomMembers, userID)
	}
	if userRooms, exists := m.userRooms[userID]; exists {
		delete(userRooms, roomID)
	}
	return nil
}

func (m *InMemoryDB) GetRoomMembers(ctx context.Context, roomID string) ([]*DBRoomMember, error) {
	var members []*DBRoomMember
	if roomMembers, exists := m.roomMembers[roomID]; exists {
		for _, member := range roomMembers {
			members = append(members, member)
		}
	}
	return members, nil
}

func (m *InMemoryDB) IsUserInRoom(ctx context.Context, roomID, userID string) (bool, error) {
	if roomMembers, exists := m.roomMembers[roomID]; exists {
		_, isMember := roomMembers[userID]
		return isMember, nil
	}
	return false, nil
}

func (m *InMemoryDB) SaveReadReceipt(ctx context.Context, receipt *DBReadReceipt) error {
	if _, exists := m.readReceipts[receipt.MessageID]; !exists {
		m.readReceipts[receipt.MessageID] = make(map[string]*DBReadReceipt)
	}
	m.readReceipts[receipt.MessageID][receipt.UserID] = receipt
	return nil
}

func (m *InMemoryDB) GetReadReceipts(ctx context.Context, messageID string) ([]*DBReadReceipt, error) {
	var receipts []*DBReadReceipt
	if messageReceipts, exists := m.readReceipts[messageID]; exists {
		for _, receipt := range messageReceipts {
			receipts = append(receipts, receipt)
		}
	}
	return receipts, nil
}

func (m *InMemoryDB) GetUserReadReceipts(ctx context.Context, userID, roomID string) ([]*DBReadReceipt, error) {
	var receipts []*DBReadReceipt
	for messageID, messageReceipts := range m.readReceipts {
		if receipt, exists := messageReceipts[userID]; exists {
			// Check if message belongs to the room
			if msg, exists := m.messages[messageID]; exists && msg.RoomID == roomID {
				receipts = append(receipts, receipt)
			}
		}
	}
	return receipts, nil
}

func (m *InMemoryDB) Close() error {
	return nil
}
