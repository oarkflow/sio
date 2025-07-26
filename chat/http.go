package chat

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oarkflow/sio/websocket"
)

// HTTPHandler provides HTTP endpoints for the chat server
type HTTPHandler struct {
	server *Server
}

// NewHTTPHandler creates a new HTTP handler for the chat server
func NewHTTPHandler(server *Server) *HTTPHandler {
	return &HTTPHandler{
		server: server,
	}
}

// WebSocketHandler handles WebSocket upgrade requests
func (h *HTTPHandler) WebSocketHandler(w http.ResponseWriter, r *http.Request) {
	// Extract user info from query parameters
	userID := r.URL.Query().Get("user_id")
	username := r.URL.Query().Get("username")

	// Validate required parameters
	if userID == "" {
		userID = fmt.Sprintf("user_%d", time.Now().Unix())
	}
	if username == "" {
		username = fmt.Sprintf("User_%d", time.Now().Unix()%1000)
	}

	log.Printf("WebSocket upgrade request - UserID: %s, Username: %s", userID, username)

	// Create a custom upgrader - simplified approach
	// In production, you'd use a proper websocket library like gorilla/websocket
	http.Error(w, "WebSocket upgrade not fully implemented - connect directly to WebSocket server", http.StatusNotImplemented)

	// TODO: Implement proper WebSocket upgrade with user info extraction
	// For now, the client should connect directly to the WebSocket server
} // upgradeConnection manually upgrades the HTTP connection to WebSocket
func (h *HTTPHandler) upgradeConnection(w http.ResponseWriter, r *http.Request, userID, username string) (*websocket.Conn, error) {
	// This is a simplified version - in practice, you'd use the websocket server's upgrade logic
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("webserver doesn't support hijacking")
	}

	conn, _, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	// For now, we'll return an error and suggest using the WebSocket server directly
	// In a real implementation, you'd need to implement the WebSocket handshake manually
	// or expose the upgrade functionality from the websocket package
	conn.Close()
	return nil, fmt.Errorf("manual upgrade not implemented - use WebSocket server directly")
}

// API endpoints for room and message management

// GetRoomsHandler returns rooms for a user
func (h *HTTPHandler) GetRoomsHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = r.Header.Get("X-User-ID")
	}

	if userID == "" {
		http.Error(w, "User ID required", http.StatusBadRequest)
		return
	}

	rooms, err := h.server.db.GetUserRooms(r.Context(), userID)
	if err != nil {
		http.Error(w, "Failed to get rooms", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rooms)
}

// GetMessagesHandler returns messages for a room
func (h *HTTPHandler) GetMessagesHandler(w http.ResponseWriter, r *http.Request) {
	// Extract room ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/rooms/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "messages" {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	roomID := parts[0]

	// Parse pagination parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 50 // default
	offset := 0 // default

	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// Get messages
	messages, err := h.server.db.GetMessages(r.Context(), roomID, limit, offset)
	if err != nil {
		http.Error(w, "Failed to get messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// GetThreadHandler returns thread messages
func (h *HTTPHandler) GetThreadHandler(w http.ResponseWriter, r *http.Request) {
	parentMessageID := r.URL.Query().Get("parent")
	if parentMessageID == "" {
		http.Error(w, "Parent message ID required", http.StatusBadRequest)
		return
	}

	messages, err := h.server.db.GetThreadMessages(r.Context(), parentMessageID)
	if err != nil {
		http.Error(w, "Failed to get thread messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// GetRoomMembersHandler returns members of a room
func (h *HTTPHandler) GetRoomMembersHandler(w http.ResponseWriter, r *http.Request) {
	// Extract room ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/rooms/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "members" {
		http.Error(w, "Invalid URL format", http.StatusBadRequest)
		return
	}
	roomID := parts[0]

	// Get online users from server
	members := h.server.GetRoomMembers(roomID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"roomId":  roomID,
		"members": members,
		"count":   len(members),
	})
}

// CreateRoomHandler creates a new room
func (h *HTTPHandler) CreateRoomHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		UserID  string `json:"userId"`
		UserID2 string `json:"userId2,omitempty"` // For DM rooms
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Type == "" || req.UserID == "" {
		http.Error(w, "Name, type, and userId are required", http.StatusBadRequest)
		return
	}

	var roomID string
	var err error

	switch req.Type {
	case "group":
		roomID, err = h.server.CreateGroupRoom(r.Context(), req.Name, req.UserID)
	case "dm":
		if req.UserID2 == "" {
			http.Error(w, "userId2 required for DM rooms", http.StatusBadRequest)
			return
		}
		roomID, err = h.server.CreateDMRoom(r.Context(), req.UserID, req.UserID2)
	default:
		http.Error(w, "Invalid room type", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "Failed to create room", http.StatusInternalServerError)
		return
	}

	response := map[string]string{
		"roomId": roomID,
		"status": "created",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// FileUploadHandler handles file uploads via HTTP
func (h *HTTPHandler) FileUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Enable CORS for this endpoint
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "X-User-ID, X-Username, Content-Type")

	// Handle preflight OPTIONS request
	if r.Method == http.MethodOptions {
		return
	}

	// Parse multipart form data (limit to 50MB)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	// Get user info from headers
	userID := r.Header.Get("X-User-ID")
	username := r.Header.Get("X-Username")
	roomID := r.FormValue("roomId")

	if userID == "" || username == "" || roomID == "" {
		http.Error(w, "Missing required parameters", http.StatusBadRequest)
		return
	}

	// Get uploaded files
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	var uploadedFiles []map[string]interface{}

	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			log.Printf("Error opening file %s: %v", fileHeader.Filename, err)
			continue
		}
		defer file.Close()

		// Validate file size (limit to 10MB)
		if fileHeader.Size > 10*1024*1024 {
			http.Error(w, fmt.Sprintf("File %s is too large (max 10MB)", fileHeader.Filename), http.StatusRequestEntityTooLarge)
			return
		}

		// Read file data
		fileData := make([]byte, fileHeader.Size)
		_, err = file.Read(fileData)
		if err != nil {
			log.Printf("Error reading file %s: %v", fileHeader.Filename, err)
			continue
		}

		// Generate a unique file ID
		fileID := h.generateFileID()

		// In a real implementation, you would save to disk/cloud storage
		// For now, we'll encode to base64 for storage in memory/database
		fileDataB64 := base64.StdEncoding.EncodeToString(fileData)

		// Create file message and send through WebSocket
		messageID := h.generateMessageID()

		// Create file share message
		fileShareMsg := &ChatMessage{
			Type:      FileShare,
			RoomID:    roomID,
			MessageID: messageID,
			Payload: FileSharePayload{
				FileName:  fileHeader.Filename,
				FileSize:  fileHeader.Size,
				FileType:  fileHeader.Header.Get("Content-Type"),
				FileData:  fileDataB64,
				UserID:    userID,
				Username:  username,
				MessageID: messageID,
			},
			Timestamp: time.Now(),
		}

		// Send file message through chat server
		h.server.handleFileShareFromHTTP(userID, fileShareMsg)

		// Create file info for response (without the actual data)
		fileInfo := map[string]interface{}{
			"fileId":    fileID,
			"fileName":  fileHeader.Filename,
			"fileSize":  fileHeader.Size,
			"fileType":  fileHeader.Header.Get("Content-Type"),
			"messageId": messageID,
			"url":       fmt.Sprintf("/api/files/%s", fileID), // Would be actual file URL
		}

		uploadedFiles = append(uploadedFiles, fileInfo)
	}

	response := map[string]interface{}{
		"status": "success",
		"files":  uploadedFiles,
		"count":  len(uploadedFiles),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// SetupRoutes sets up HTTP routes for the chat API
func (h *HTTPHandler) SetupRoutes(mux *http.ServeMux) {
	// WebSocket endpoint
	mux.HandleFunc("/ws", h.WebSocketHandler)

	// REST API endpoints
	mux.HandleFunc("/api/rooms", h.GetRoomsHandler)
	mux.HandleFunc("/api/rooms/create", h.CreateRoomHandler)

	// File upload endpoint
	mux.HandleFunc("/api/upload", h.FileUploadHandler)

	// Messages endpoints (using pattern matching for room ID)
	mux.HandleFunc("/api/rooms/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			h.GetMessagesHandler(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/members") {
			h.GetRoomMembersHandler(w, r)
		} else if strings.Contains(r.URL.Path, "/threads") {
			h.GetThreadHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	// Serve enhanced chat client
	mux.HandleFunc("/enhanced", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/enhanced-chat.html")
	})
}

// CORS middleware
func (h *HTTPHandler) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-ID, X-Username")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// StartHTTPServer starts an HTTP server with the chat endpoints
func (h *HTTPHandler) StartHTTPServer(addr string) error {
	mux := http.NewServeMux()
	h.SetupRoutes(mux)

	// Apply CORS middleware
	handler := h.CORS(mux)

	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	return server.ListenAndServe()
}

// Helper methods

// generateFileID generates a unique file ID
func (h *HTTPHandler) generateFileID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// generateMessageID generates a unique message ID
func (h *HTTPHandler) generateMessageID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
