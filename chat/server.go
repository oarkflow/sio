package chat

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oarkflow/sio/websocket"
)

// Server represents the chat server
type Server struct {
	config   *Config
	db       Database
	wsServer *websocket.Server

	// Client management
	clients   map[string]*Client // clientID -> Client
	rooms     map[string]*Room   // roomID -> Room
	clientsMu sync.RWMutex
	roomsMu   sync.RWMutex

	// Rate limiting
	rateLimiters map[string]*RateLimit // clientID -> RateLimit
	rateMu       sync.RWMutex

	// Security
	security *SecurityConfig

	// Typing indicators
	typingClients map[string]map[string]time.Time // roomID -> clientID -> lastTyping
	typingMu      sync.RWMutex

	// Background tasks
	ctx    context.Context
	cancel context.CancelFunc
}

// Config represents server configuration
type Config struct {
	Port        int
	Host        string
	Database    Database
	Security    *SecurityConfig
	TLSCertFile string
	TLSKeyFile  string

	// WebSocket configuration
	WSConfig *websocket.Config
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Port:     8080,
		Host:     "localhost",
		Database: NewInMemoryDB(),
		Security: &SecurityConfig{
			MaxMessageLength:   4096,
			MaxRoomsPerUser:    50,
			AllowedOrigins:     []string{"*"},
			RequireAuth:        false,
			RateLimitPerSocket: &RateLimit{Requests: 100, WindowSize: time.Minute},
			RateLimitPerRoom:   &RateLimit{Requests: 1000, WindowSize: time.Minute},
		},
		WSConfig: websocket.DefaultConfig(),
	}
}

// NewServer creates a new chat server
func NewServer(config *Config) *Server {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Configure WebSocket security and user extraction
	if config.WSConfig == nil {
		config.WSConfig = websocket.DefaultConfig()
	}

	config.WSConfig.CheckOrigin = func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if len(config.Security.AllowedOrigins) == 0 {
			return true
		}
		for _, allowed := range config.Security.AllowedOrigins {
			if allowed == "*" || allowed == origin {
				return true
			}
		}
		return false
	}

	server := &Server{
		config:        config,
		db:            config.Database,
		clients:       make(map[string]*Client),
		rooms:         make(map[string]*Room),
		rateLimiters:  make(map[string]*RateLimit),
		typingClients: make(map[string]map[string]time.Time),
		security:      config.Security,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Create custom WebSocket server with user info extraction
	server.wsServer = server.createWebSocketServer(config.WSConfig)

	// Start background tasks
	go server.cleanupTypingIndicators()

	return server
}

// createWebSocketServer creates a WebSocket server with custom connection handling
func (s *Server) createWebSocketServer(config *websocket.Config) *websocket.Server {
	wsServer := websocket.NewServer(config)

	// Store original onConnection handler
	originalHandler := s.handleNewConnection

	// Create a custom handler that extracts user info
	wsServer.OnConnection(func(conn *websocket.Conn) {
		// Try to extract user info from the connection
		// This is a simplified approach - in practice, you'd need to modify
		// the websocket package to pass the original HTTP request
		originalHandler(conn)
	})

	wsServer.OnError(func(err error) {
		log.Printf("WebSocket server error: %v", err)
	})

	return wsServer
}

// Start starts the chat server
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	log.Printf("Starting chat server on %s", addr)

	// Initialize default rooms
	if err := s.initializeDefaultRooms(); err != nil {
		return fmt.Errorf("failed to initialize default rooms: %w", err)
	}

	// Configure TLS if certificates are provided
	if s.config.TLSCertFile != "" && s.config.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificates: %w", err)
		}

		// Create new WebSocket server with TLS config
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		s.config.WSConfig.TLSConfig = tlsConfig
		s.wsServer = websocket.NewServer(s.config.WSConfig)
		s.wsServer.OnConnection(s.handleNewConnection)
		s.wsServer.OnError(func(err error) {
			log.Printf("WebSocket server error: %v", err)
		})
	}

	// Start WebSocket server
	return s.wsServer.Listen(addr)
}

// Stop stops the chat server
func (s *Server) Stop() error {
	s.cancel()
	return s.wsServer.Stop()
}

// initializeDefaultRooms creates default public rooms
func (s *Server) initializeDefaultRooms() error {
	defaultRooms := []struct {
		id   string
		name string
	}{
		{"public:lobby", "General Lobby"},
		{"public:random", "Random Chat"},
		{"public:help", "Help & Support"},
	}

	for _, room := range defaultRooms {
		dbRoom := &DBRoom{
			ID:       room.id,
			Type:     string(PublicRoom),
			Name:     room.name,
			Metadata: "{}",
			Created:  time.Now(),
		}

		// Create room in database (ignore if exists)
		s.db.CreateRoom(s.ctx, dbRoom)

		// Create room in memory
		s.roomsMu.Lock()
		s.rooms[room.id] = &Room{
			ID:       room.id,
			Type:     PublicRoom,
			Name:     room.name,
			Clients:  make(map[string]*Client),
			Metadata: make(map[string]interface{}),
			Created:  time.Now(),
		}
		s.roomsMu.Unlock()
	}

	return nil
}

// handleNewConnection handles a new WebSocket connection
func (s *Server) handleNewConnection(conn *websocket.Conn) {
	// Extract user info from the connection URL or create default
	userID := fmt.Sprintf("user_%d", time.Now().Unix())
	username := fmt.Sprintf("User_%d", time.Now().Unix()%1000)

	s.handleNewConnectionWithUser(conn, userID, username)
}

// handleNewConnectionWithUser handles a new WebSocket connection with user info
func (s *Server) handleNewConnectionWithUser(conn *websocket.Conn, userID, username string) {
	clientID := s.generateClientID()

	client := &Client{
		ID:       clientID,
		UserID:   userID,
		Username: username,
		Conn:     conn,
		Rooms:    make(map[string]bool),
		LastSeen: time.Now(),
		IsTyping: make(map[string]bool),
	}

	// Store client
	s.clientsMu.Lock()
	s.clients[clientID] = client
	s.clientsMu.Unlock()

	// Set up connection handlers
	conn.OnMessage(func(conn *websocket.Conn, messageType websocket.MessageType, data []byte) {
		s.handleMessage(client, data)
	})

	conn.OnClose(func(conn *websocket.Conn, code websocket.CloseCode, reason string) {
		s.handleClientDisconnect(client)
	})

	conn.OnError(func(conn *websocket.Conn, err error) {
		log.Printf("Client %s error: %v", clientID, err)
		s.handleClientDisconnect(client)
	})

	log.Printf("New client connected: %s (User: %s)", clientID, username)
}

// handleMessage processes incoming messages from clients
func (s *Server) handleMessage(client *Client, data []byte) {
	// Update last seen
	client.LastSeen = time.Now()

	// Check rate limit
	if !s.checkRateLimit(client.ID) {
		s.sendError(client, 429, "Rate limit exceeded", "")
		return
	}

	// Parse message
	var msg ChatMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		s.sendError(client, 400, "Invalid message format", err.Error())
		return
	}

	// Validate message
	if err := s.validateMessage(&msg); err != nil {
		s.sendError(client, 400, "Message validation failed", err.Error())
		return
	}

	// Set timestamp
	msg.Timestamp = time.Now()

	// Route message based on type
	switch msg.Type {
	case JoinRoom:
		s.handleJoinRoom(client, &msg)
	case LeaveRoom:
		s.handleLeaveRoom(client, &msg)
	case Message:
		s.handleChatMessage(client, &msg)
	case ThreadReply:
		s.handleThreadReply(client, &msg)
	case Typing:
		s.handleTyping(client, &msg)
	case ReadReceipt:
		s.handleReadReceipt(client, &msg)
	default:
		s.sendError(client, 400, "Unknown message type", string(msg.Type))
	}
}

// handleJoinRoom processes room join requests
func (s *Server) handleJoinRoom(client *Client, msg *ChatMessage) {
	roomID := msg.RoomID

	// Check if room exists or create it
	room, err := s.getOrCreateRoom(roomID)
	if err != nil {
		s.sendError(client, 404, "Room not found", err.Error())
		return
	}

	// Check permissions for private rooms
	if room.Type != PublicRoom {
		allowed, err := s.checkRoomPermission(client, roomID)
		if err != nil || !allowed {
			s.sendError(client, 403, "Access denied", "Not authorized to join this room")
			return
		}
	}

	// Add client to room
	s.roomsMu.Lock()
	room.Clients[client.ID] = client
	s.roomsMu.Unlock()

	// Add room to client
	client.Rooms[roomID] = true

	// Add to database if not public room
	if room.Type != PublicRoom {
		member := &DBRoomMember{
			RoomID:   roomID,
			UserID:   client.UserID,
			Role:     "member",
			JoinedAt: time.Now(),
		}
		s.db.AddRoomMember(s.ctx, member)
	}

	// Broadcast user joined
	s.broadcastToRoom(roomID, ChatMessage{
		Type:      UserJoined,
		RoomID:    roomID,
		Timestamp: time.Now(),
		Payload: UserPresencePayload{
			UserID:   client.UserID,
			Username: client.Username,
			Status:   "online",
		},
	}, client.ID)

	// Send acknowledgment
	s.sendMessage(client, ChatMessage{
		Type:      Acknowledgment,
		RoomID:    roomID,
		Timestamp: time.Now(),
		Payload: AckPayload{
			Status: StatusDelivered,
		},
	})

	log.Printf("Client %s joined room %s", client.ID, roomID)
}

// handleLeaveRoom processes room leave requests
func (s *Server) handleLeaveRoom(client *Client, msg *ChatMessage) {
	roomID := msg.RoomID

	// Check if client is in room
	if !client.Rooms[roomID] {
		s.sendError(client, 400, "Not in room", "You are not in this room")
		return
	}

	// Remove client from room
	s.roomsMu.Lock()
	if room, exists := s.rooms[roomID]; exists {
		delete(room.Clients, client.ID)

		// Clean up empty non-public rooms
		if len(room.Clients) == 0 && room.Type != PublicRoom {
			delete(s.rooms, roomID)
		}
	}
	s.roomsMu.Unlock()

	// Remove room from client
	delete(client.Rooms, roomID)

	// Remove from database if not public room
	if !strings.HasPrefix(roomID, "public:") {
		s.db.RemoveRoomMember(s.ctx, roomID, client.UserID)
	}

	// Broadcast user left
	s.broadcastToRoom(roomID, ChatMessage{
		Type:      UserLeft,
		RoomID:    roomID,
		Timestamp: time.Now(),
		Payload: UserPresencePayload{
			UserID:   client.UserID,
			Username: client.Username,
			Status:   "offline",
		},
	}, client.ID)

	log.Printf("Client %s left room %s", client.ID, roomID)
}

// handleChatMessage processes regular chat messages
func (s *Server) handleChatMessage(client *Client, msg *ChatMessage) {
	roomID := msg.RoomID

	// Check if client is in room
	if !client.Rooms[roomID] {
		s.sendError(client, 403, "Access denied", "You are not in this room")
		return
	}

	// Parse payload
	var payload MessagePayload
	if err := s.parsePayload(msg.Payload, &payload); err != nil {
		s.sendError(client, 400, "Invalid payload", err.Error())
		return
	}

	// Generate message ID if not provided
	if payload.MessageID == "" {
		payload.MessageID = s.generateMessageID()
	}

	// Set sender info
	payload.UserID = client.UserID
	payload.Username = client.Username

	// Save to database
	dbMsg := &DBMessage{
		ID:          payload.MessageID,
		RoomID:      roomID,
		SenderID:    client.UserID,
		Content:     payload.Content,
		Timestamp:   msg.Timestamp,
		MessageType: string(msg.Type),
	}

	if err := s.db.SaveMessage(s.ctx, dbMsg); err != nil {
		s.sendError(client, 500, "Failed to save message", err.Error())
		return
	}

	// Update message with payload
	msg.Payload = payload
	msg.MessageID = payload.MessageID

	// Broadcast to room
	s.broadcastToRoom(roomID, *msg, "")

	// Send acknowledgment to sender
	s.sendMessage(client, ChatMessage{
		Type:      Acknowledgment,
		RoomID:    roomID,
		MessageID: payload.MessageID,
		Timestamp: time.Now(),
		Payload: AckPayload{
			MessageID: payload.MessageID,
			Status:    StatusSent,
		},
	})
}

// handleThreadReply processes thread reply messages
func (s *Server) handleThreadReply(client *Client, msg *ChatMessage) {
	roomID := msg.RoomID

	// Check if client is in room
	if !client.Rooms[roomID] {
		s.sendError(client, 403, "Access denied", "You are not in this room")
		return
	}

	// Parse payload
	var payload ThreadReplyPayload
	if err := s.parsePayload(msg.Payload, &payload); err != nil {
		s.sendError(client, 400, "Invalid payload", err.Error())
		return
	}

	// Validate parent message exists
	if _, err := s.db.GetMessage(s.ctx, payload.ParentMessageID); err != nil {
		s.sendError(client, 404, "Parent message not found", err.Error())
		return
	}

	// Generate message ID if not provided
	if payload.MessageID == "" {
		payload.MessageID = s.generateMessageID()
	}

	// Set sender info
	payload.UserID = client.UserID
	payload.Username = client.Username

	// Save to database
	dbMsg := &DBMessage{
		ID:              payload.MessageID,
		RoomID:          roomID,
		SenderID:        client.UserID,
		Content:         payload.Content,
		Timestamp:       msg.Timestamp,
		ParentMessageID: &payload.ParentMessageID,
		MessageType:     string(msg.Type),
	}

	if err := s.db.SaveMessage(s.ctx, dbMsg); err != nil {
		s.sendError(client, 500, "Failed to save message", err.Error())
		return
	}

	// Update message with payload
	msg.Payload = payload
	msg.MessageID = payload.MessageID

	// Broadcast to room
	s.broadcastToRoom(roomID, *msg, "")

	// Send acknowledgment to sender
	s.sendMessage(client, ChatMessage{
		Type:      Acknowledgment,
		RoomID:    roomID,
		MessageID: payload.MessageID,
		Timestamp: time.Now(),
		Payload: AckPayload{
			MessageID: payload.MessageID,
			Status:    StatusSent,
		},
	})
}

// handleTyping processes typing indicators
func (s *Server) handleTyping(client *Client, msg *ChatMessage) {
	roomID := msg.RoomID

	// Check if client is in room
	if !client.Rooms[roomID] {
		return // Silently ignore
	}

	// Parse payload
	var payload TypingPayload
	if err := s.parsePayload(msg.Payload, &payload); err != nil {
		return // Silently ignore typing errors
	}

	// Set sender info
	payload.UserID = client.UserID
	payload.Username = client.Username

	// Update typing state
	s.typingMu.Lock()
	if _, exists := s.typingClients[roomID]; !exists {
		s.typingClients[roomID] = make(map[string]time.Time)
	}

	if payload.IsTyping {
		s.typingClients[roomID][client.ID] = time.Now()
		client.IsTyping[roomID] = true
		msg.Type = TypingStart
	} else {
		delete(s.typingClients[roomID], client.ID)
		client.IsTyping[roomID] = false
		msg.Type = TypingStop
	}
	s.typingMu.Unlock()

	// Update message payload
	msg.Payload = payload

	// Broadcast typing indicator (exclude sender)
	s.broadcastToRoom(roomID, *msg, client.ID)
}

// handleReadReceipt processes read receipts
func (s *Server) handleReadReceipt(client *Client, msg *ChatMessage) {
	roomID := msg.RoomID

	// Check if client is in room
	if !client.Rooms[roomID] {
		s.sendError(client, 403, "Access denied", "You are not in this room")
		return
	}

	// Parse payload
	var payload ReadReceiptPayload
	if err := s.parsePayload(msg.Payload, &payload); err != nil {
		s.sendError(client, 400, "Invalid payload", err.Error())
		return
	}

	// Set user info
	payload.UserID = client.UserID
	payload.ReadAt = time.Now()

	// Save to database
	receipt := &DBReadReceipt{
		MessageID: payload.MessageID,
		UserID:    client.UserID,
		ReadAt:    payload.ReadAt,
	}

	if err := s.db.SaveReadReceipt(s.ctx, receipt); err != nil {
		s.sendError(client, 500, "Failed to save read receipt", err.Error())
		return
	}

	// Update message payload
	msg.Payload = payload

	// Broadcast read receipt (exclude sender)
	s.broadcastToRoom(roomID, *msg, client.ID)
}

// Helper methods

// getOrCreateRoom gets an existing room or creates a new one
func (s *Server) getOrCreateRoom(roomID string) (*Room, error) {
	s.roomsMu.RLock()
	room, exists := s.rooms[roomID]
	s.roomsMu.RUnlock()

	if exists {
		return room, nil
	}

	// Parse room type from ID
	var roomType RoomType
	if strings.HasPrefix(roomID, "public:") {
		roomType = PublicRoom
	} else if strings.HasPrefix(roomID, "group:") {
		roomType = GroupRoom
	} else if strings.HasPrefix(roomID, "dm:") {
		roomType = DMRoom
	} else {
		return nil, fmt.Errorf("invalid room ID format")
	}

	// For public rooms, create immediately
	if roomType == PublicRoom {
		s.roomsMu.Lock()
		room = &Room{
			ID:       roomID,
			Type:     roomType,
			Name:     roomID,
			Clients:  make(map[string]*Client),
			Metadata: make(map[string]interface{}),
			Created:  time.Now(),
		}
		s.rooms[roomID] = room
		s.roomsMu.Unlock()
		return room, nil
	}

	// For private rooms, check database first
	dbRoom, err := s.db.GetRoom(s.ctx, roomID)
	if err != nil {
		// Create DM room if it doesn't exist
		if roomType == DMRoom {
			dbRoom = &DBRoom{
				ID:       roomID,
				Type:     string(roomType),
				Name:     roomID,
				Metadata: "{}",
				Created:  time.Now(),
			}
			if err := s.db.CreateRoom(s.ctx, dbRoom); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// Create room in memory
	s.roomsMu.Lock()
	room = &Room{
		ID:       dbRoom.ID,
		Type:     RoomType(dbRoom.Type),
		Name:     dbRoom.Name,
		Clients:  make(map[string]*Client),
		Metadata: make(map[string]interface{}),
		Created:  dbRoom.Created,
	}
	s.rooms[roomID] = room
	s.roomsMu.Unlock()

	return room, nil
}

// checkRoomPermission checks if a client can access a room
func (s *Server) checkRoomPermission(client *Client, roomID string) (bool, error) {
	// Public rooms are always accessible
	if strings.HasPrefix(roomID, "public:") {
		return true, nil
	}

	// For DM rooms, check if user is part of the conversation
	if strings.HasPrefix(roomID, "dm:") {
		// DM room format: dm:user1_user2 (sorted)
		users := strings.TrimPrefix(roomID, "dm:")
		userList := strings.Split(users, "_")
		for _, user := range userList {
			if user == client.UserID {
				return true, nil
			}
		}
		return false, nil
	}

	// For group rooms, check database membership
	return s.db.IsUserInRoom(s.ctx, roomID, client.UserID)
}

// broadcastToRoom sends a message to all clients in a room
func (s *Server) broadcastToRoom(roomID string, msg ChatMessage, excludeClientID string) {
	s.roomsMu.RLock()
	room, exists := s.rooms[roomID]
	s.roomsMu.RUnlock()

	if !exists {
		return
	}

	// Marshal message once
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal broadcast message: %v", err)
		return
	}

	// Send to all clients in room
	for clientID, client := range room.Clients {
		if clientID != excludeClientID {
			if err := client.Conn.WriteText(string(data)); err != nil {
				log.Printf("Failed to send message to client %s: %v", clientID, err)
				// Handle client disconnect
				go s.handleClientDisconnect(client)
			}
		}
	}
}

// sendMessage sends a message to a specific client
func (s *Server) sendMessage(client *Client, msg ChatMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal message: %v", err)
		return
	}

	if err := client.Conn.WriteText(string(data)); err != nil {
		log.Printf("Failed to send message to client %s: %v", client.ID, err)
		go s.handleClientDisconnect(client)
	}
}

// sendError sends an error message to a client
func (s *Server) sendError(client *Client, code int, message, details string) {
	errorMsg := ChatMessage{
		Type:      Error,
		Timestamp: time.Now(),
		Payload: ErrorPayload{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
	s.sendMessage(client, errorMsg)
}

// handleClientDisconnect handles client disconnection
func (s *Server) handleClientDisconnect(client *Client) {
	// Remove from all rooms
	for roomID := range client.Rooms {
		// Broadcast user left
		s.broadcastToRoom(roomID, ChatMessage{
			Type:      UserLeft,
			RoomID:    roomID,
			Timestamp: time.Now(),
			Payload: UserPresencePayload{
				UserID:   client.UserID,
				Username: client.Username,
				Status:   "offline",
			},
		}, client.ID)

		// Remove from room
		s.roomsMu.Lock()
		if room, exists := s.rooms[roomID]; exists {
			delete(room.Clients, client.ID)

			// Clean up empty non-public rooms
			if len(room.Clients) == 0 && room.Type != PublicRoom {
				delete(s.rooms, roomID)
			}
		}
		s.roomsMu.Unlock()
	}

	// Remove from typing indicators
	s.typingMu.Lock()
	for roomID := range s.typingClients {
		delete(s.typingClients[roomID], client.ID)
	}
	s.typingMu.Unlock()

	// Remove client
	s.clientsMu.Lock()
	delete(s.clients, client.ID)
	s.clientsMu.Unlock()

	// Remove rate limiter
	s.rateMu.Lock()
	delete(s.rateLimiters, client.ID)
	s.rateMu.Unlock()

	log.Printf("Client %s disconnected", client.ID)
}

// Rate limiting and validation

// checkRateLimit checks if a client has exceeded rate limits
func (s *Server) checkRateLimit(clientID string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	limiter, exists := s.rateLimiters[clientID]
	if !exists {
		limiter = &RateLimit{
			Requests:    s.security.RateLimitPerSocket.Requests,
			WindowSize:  s.security.RateLimitPerSocket.WindowSize,
			LastReset:   time.Now(),
			CurrentUsed: 0,
		}
		s.rateLimiters[clientID] = limiter
	}

	now := time.Now()
	if now.Sub(limiter.LastReset) >= limiter.WindowSize {
		limiter.LastReset = now
		limiter.CurrentUsed = 0
	}

	if limiter.CurrentUsed >= limiter.Requests {
		return false
	}

	limiter.CurrentUsed++
	return true
}

// validateMessage validates an incoming message
func (s *Server) validateMessage(msg *ChatMessage) error {
	if msg.Type == "" {
		return fmt.Errorf("message type is required")
	}

	if msg.RoomID == "" {
		return fmt.Errorf("room ID is required")
	}

	// Validate message content length for text messages
	if msg.Type == Message || msg.Type == ThreadReply {
		var content string
		if payload, ok := msg.Payload.(map[string]interface{}); ok {
			if c, exists := payload["content"]; exists {
				content = fmt.Sprintf("%v", c)
			}
		}

		if len(content) > s.security.MaxMessageLength {
			return fmt.Errorf("message content too long")
		}
	}

	return nil
}

// parsePayload parses message payload into the specified type
func (s *Server) parsePayload(payload interface{}, target interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// Utility functions

// generateClientID generates a unique client ID
func (s *Server) generateClientID() string {
	return fmt.Sprintf("client_%d_%s", time.Now().Unix(), s.randomString(8))
}

// generateMessageID generates a unique message ID
func (s *Server) generateMessageID() string {
	return fmt.Sprintf("msg_%d_%s", time.Now().Unix(), s.randomString(12))
}

// randomString generates a random string of specified length
func (s *Server) randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	rand.Read(b)
	for i := range b {
		b[i] = charset[b[i]%byte(len(charset))]
	}
	return string(b)
}

// Background tasks

// cleanupTypingIndicators removes stale typing indicators
func (s *Server) cleanupTypingIndicators() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.typingMu.Lock()
			now := time.Now()

			for roomID, clients := range s.typingClients {
				for clientID, lastTyping := range clients {
					// Remove typing indicator after 10 seconds of inactivity
					if now.Sub(lastTyping) > 10*time.Second {
						delete(clients, clientID)

						// Find client and update state
						s.clientsMu.RLock()
						if client, exists := s.clients[clientID]; exists {
							client.IsTyping[roomID] = false

							// Broadcast typing stop
							s.broadcastToRoom(roomID, ChatMessage{
								Type:      TypingStop,
								RoomID:    roomID,
								Timestamp: now,
								Payload: TypingPayload{
									UserID:   client.UserID,
									Username: client.Username,
									IsTyping: false,
								},
							}, clientID)
						}
						s.clientsMu.RUnlock()
					}
				}

				// Clean up empty room entries
				if len(clients) == 0 {
					delete(s.typingClients, roomID)
				}
			}
			s.typingMu.Unlock()
		}
	}
}

// API methods for room management

// CreateGroupRoom creates a new group room
func (s *Server) CreateGroupRoom(ctx context.Context, name string, creatorID string) (string, error) {
	roomID := fmt.Sprintf("group:%s", s.randomString(16))

	dbRoom := &DBRoom{
		ID:       roomID,
		Type:     string(GroupRoom),
		Name:     name,
		Metadata: "{}",
		Created:  time.Now(),
	}

	if err := s.db.CreateRoom(ctx, dbRoom); err != nil {
		return "", err
	}

	// Add creator as owner
	member := &DBRoomMember{
		RoomID:   roomID,
		UserID:   creatorID,
		Role:     "owner",
		JoinedAt: time.Now(),
	}

	if err := s.db.AddRoomMember(ctx, member); err != nil {
		return "", err
	}

	return roomID, nil
}

// CreateDMRoom creates a new DM room between two users
func (s *Server) CreateDMRoom(ctx context.Context, userID1, userID2 string) (string, error) {
	// Sort user IDs to ensure consistent room ID
	users := []string{userID1, userID2}
	sort.Strings(users)
	roomID := fmt.Sprintf("dm:%s_%s", users[0], users[1])

	// Check if room already exists
	if _, err := s.db.GetRoom(ctx, roomID); err == nil {
		return roomID, nil // Room already exists
	}

	dbRoom := &DBRoom{
		ID:       roomID,
		Type:     string(DMRoom),
		Name:     fmt.Sprintf("DM: %s, %s", users[0], users[1]),
		Metadata: "{}",
		Created:  time.Now(),
	}

	if err := s.db.CreateRoom(ctx, dbRoom); err != nil {
		return "", err
	}

	// Add both users as members
	for _, userID := range users {
		member := &DBRoomMember{
			RoomID:   roomID,
			UserID:   userID,
			Role:     "member",
			JoinedAt: time.Now(),
		}
		if err := s.db.AddRoomMember(ctx, member); err != nil {
			return "", err
		}
	}

	return roomID, nil
}

// GetRoomMessages retrieves messages from a room with pagination
func (s *Server) GetRoomMessages(ctx context.Context, roomID string, limit, offset int) ([]*DBMessage, error) {
	return s.db.GetMessages(ctx, roomID, limit, offset)
}

// GetThreadMessages retrieves thread messages
func (s *Server) GetThreadMessages(ctx context.Context, parentMessageID string) ([]*DBMessage, error) {
	return s.db.GetThreadMessages(ctx, parentMessageID)
}
