package websocket

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Constants from RFC 6455
const (
	// WebSocket magic string used in handshake
	websocketMagicString = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	// Frame opcodes
	OpcodeContinuation = 0x0
	OpcodeText         = 0x1
	OpcodeBinary       = 0x2
	OpcodeClose        = 0x8
	OpcodePing         = 0x9
	OpcodePong         = 0xA

	// Frame flags
	FlagFIN  = 0x80
	FlagRSV1 = 0x40
	FlagRSV2 = 0x20
	FlagRSV3 = 0x10
	FlagMask = 0x80

	// Max payload lengths
	MaxPayload7Bit  = 125
	MaxPayload16Bit = 65535

	// Default configuration values
	DefaultReadBufferSize   = 4096
	DefaultWriteBufferSize  = 4096
	DefaultHandshakeTimeout = 45 * time.Second
	DefaultPingInterval     = 30 * time.Second
	DefaultPongTimeout      = 10 * time.Second
	DefaultMaxMessageSize   = 1024 * 1024 // 1MB
	DefaultMaxFrameSize     = 1024 * 1024 // 1MB
)

var (
	ErrConnectionClosed       = errors.New("websocket: connection closed")
	ErrInvalidFrame           = errors.New("websocket: invalid frame")
	ErrMessageTooLarge        = errors.New("websocket: message too large")
	ErrInvalidOpcode          = errors.New("websocket: invalid opcode")
	ErrInvalidHandshake       = errors.New("websocket: invalid handshake")
	ErrOriginNotAllowed       = errors.New("websocket: origin not allowed")
	ErrSubprotocolMismatch    = errors.New("websocket: subprotocol mismatch")
	ErrCompressionUnsupported = errors.New("websocket: compression unsupported")
)

// MessageType represents the type of WebSocket message
type MessageType int

const (
	TextMessage   MessageType = 1
	BinaryMessage MessageType = 2
	CloseMessage  MessageType = 8
	PingMessage   MessageType = 9
	PongMessage   MessageType = 10
)

// CloseCode represents WebSocket close codes
type CloseCode int

const (
	CloseNormalClosure           CloseCode = 1000
	CloseGoingAway               CloseCode = 1001
	CloseProtocolError           CloseCode = 1002
	CloseUnsupportedData         CloseCode = 1003
	CloseNoStatusReceived        CloseCode = 1005
	CloseAbnormalClosure         CloseCode = 1006
	CloseInvalidFramePayloadData CloseCode = 1007
	ClosePolicyViolation         CloseCode = 1008
	CloseMessageTooBig           CloseCode = 1009
	CloseMandatoryExtension      CloseCode = 1010
	CloseInternalServerErr       CloseCode = 1011
	CloseServiceRestart          CloseCode = 1012
	CloseTryAgainLater           CloseCode = 1013
	CloseTLSHandshake            CloseCode = 1015
)

// Frame represents a WebSocket frame
type Frame struct {
	FIN     bool
	RSV1    bool
	RSV2    bool
	RSV3    bool
	Opcode  byte
	Masked  bool
	Payload []byte
}

// Config represents WebSocket connection configuration
type Config struct {
	// Server configuration
	ReadBufferSize   int
	WriteBufferSize  int
	HandshakeTimeout time.Duration
	MaxMessageSize   int64
	MaxFrameSize     int64

	// Security
	CheckOrigin    func(r *http.Request) bool
	AllowedOrigins []string
	Subprotocols   []string

	// Authentication (NEW)
	CheckAuth func(r *http.Request) bool // Return true if authenticated, false otherwise

	// Keep-alive
	PingInterval   time.Duration
	PongTimeout    time.Duration
	EnablePingPong bool

	// Compression (future extension support)
	EnableCompression bool
	CompressionLevel  int

	// Per-message compression (RFC 7692)
	PerMessageDeflate          bool
	PMDServerNoContextTakeover bool
	PMDClientNoContextTakeover bool
	PMDMaxWindowBits           int
	PMDClientMaxWindowBits     int
	PMDServerMaxWindowBits     int
	// Application-level keep-alive/idle
	IdleTimeout time.Duration

	// Backpressure
	WriteQueueSize int
	WriteRateLimit int // messages/sec

	// Room/Topic management
	EnableRooms bool

	// TLS
	TLSConfig *tls.Config
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		ReadBufferSize:    DefaultReadBufferSize,
		WriteBufferSize:   DefaultWriteBufferSize,
		HandshakeTimeout:  DefaultHandshakeTimeout,
		MaxMessageSize:    DefaultMaxMessageSize,
		MaxFrameSize:      DefaultMaxFrameSize,
		PingInterval:      DefaultPingInterval,
		PongTimeout:       DefaultPongTimeout,
		EnablePingPong:    true,
		EnableCompression: false,
		CompressionLevel:  6,
		CheckOrigin: func(r *http.Request) bool {
			return true // Default: allow all origins (change for production)
		},
		CheckAuth: nil, // By default, no authentication check
	}
}

// EventHandler represents event handler functions
type EventHandler interface{}

// Event handlers
type (
	OpenHandler    func(conn *Conn)
	MessageHandler func(conn *Conn, messageType MessageType, data []byte)
	CloseHandler   func(conn *Conn, code CloseCode, reason string)
	ErrorHandler   func(conn *Conn, err error)
	PingHandler    func(conn *Conn, data []byte)
	PongHandler    func(conn *Conn, data []byte)
)

// Conn represents a WebSocket connection
type Conn struct {
	conn     net.Conn
	config   *Config
	isServer bool
	isClient bool

	ctx    context.Context
	cancel context.CancelFunc

	// Connection state
	mu          sync.RWMutex
	closed      bool
	closeReason CloseCode
	closeText   string

	// I/O
	reader        *bufio.Reader
	writer        *bufio.Writer
	readDeadline  time.Time
	writeDeadline time.Time

	// Message assembly
	messageBuffer []byte
	messageType   MessageType

	// Keep-alive
	pingTicker      *time.Ticker
	pongTimer       *time.Timer
	pongCh          chan bool
	keepAliveStopCh chan struct{}

	// Event handlers - default handlers
	onOpen    OpenHandler
	onMessage MessageHandler
	onClose   CloseHandler
	onError   ErrorHandler
	onPing    PingHandler
	onPong    PongHandler

	// Custom event handlers
	customHandlers map[string]EventHandler
	eventMu        sync.RWMutex

	// Per-message compression
	compressionEnabled   bool
	decompressor         interface{}
	compressor           interface{}
	negotiatedExtensions map[string]string
	// ...existing fields...
	// Fragmentation
	fragmentedWriteMu         sync.Mutex
	fragmentedWriteInProgress bool

	// Backpressure
	writeQueue       chan *Frame
	writeRateLimiter *time.Ticker

	// Application-level keep-alive
	idleTimer     *time.Timer
	onIdleTimeout func(conn *Conn)

	// Negotiated subprotocol
	negotiatedSubprotocol string
}

// setupCompression initializes zlib compressor/decompressor for permessage-deflate
func (c *Conn) setupCompression() error {
	if !c.compressionEnabled {
		return nil
	}
	// Window bits and context takeover negotiation (stub)
	// TODO: Parse c.negotiatedExtensions["permessage-deflate"] for params
	// For now, use default flate settings
	comp, err := flate.NewWriter(nil, flate.DefaultCompression)
	if err != nil {
		return err
	}
	c.compressor = comp
	// Decompressor will be created per-frame as needed
	return nil
}

// Server represents a WebSocket server
type Server struct {
	config    *Config
	listener  net.Listener
	tlsConfig *tls.Config

	// Event handlers
	onConnection func(conn *Conn)
	onError      func(err error)

	// Server state
	mu       sync.RWMutex
	running  bool
	stopCh   chan struct{}
	connPool sync.Pool

	// Room management
	roomsMu sync.RWMutex
	rooms   map[string]map[*Conn]struct{}

	// Extension negotiation
	supportedExtensions map[string]bool
}

// ExtensionInfo holds negotiated extension parameters
type ExtensionInfo struct {
	Name   string
	Params map[string]string
}

// Room management API
func (s *Server) JoinRoom(room string, c *Conn) {
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if s.rooms == nil {
		s.rooms = make(map[string]map[*Conn]struct{})
	}
	if s.rooms[room] == nil {
		s.rooms[room] = make(map[*Conn]struct{})
	}
	s.rooms[room][c] = struct{}{}
}

func (s *Server) LeaveRoom(room string, c *Conn) {
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if s.rooms != nil && s.rooms[room] != nil {
		delete(s.rooms[room], c)
		if len(s.rooms[room]) == 0 {
			delete(s.rooms, room)
		}
	}
}

func (s *Server) BroadcastToRoom(room string, messageType MessageType, data []byte) {
	s.roomsMu.RLock()
	conns := s.rooms[room]
	s.roomsMu.RUnlock()
	for c := range conns {
		c.WriteMessage(messageType, data)
	}
}

// Conn API for negotiated subprotocol and extensions
func (c *Conn) NegotiatedSubprotocol() string {
	return c.negotiatedSubprotocol
}

func (c *Conn) NegotiatedExtensions() map[string]string {
	return c.negotiatedExtensions
}

// Fragmented message write API
func (c *Conn) WriteFragmented(messageType MessageType, data []byte, fragmentSize int) error {
	c.fragmentedWriteMu.Lock()
	defer c.fragmentedWriteMu.Unlock()
	if c.fragmentedWriteInProgress {
		return errors.New("fragmented write already in progress")
	}
	c.fragmentedWriteInProgress = true
	defer func() { c.fragmentedWriteInProgress = false }()
	total := len(data)
	for i := 0; i < total; i += fragmentSize {
		end := i + fragmentSize
		if end > total {
			end = total
		}
		fin := end == total
		opcode := byte(messageType)
		if i > 0 {
			opcode = OpcodeContinuation
		}
		frame := &Frame{
			FIN:     fin,
			Opcode:  opcode,
			Payload: data[i:end],
			Masked:  c.isClient,
		}
		if err := c.writeFrame(frame); err != nil {
			return err
		}
	}
	return nil
}

// Application-level keep-alive/idle timeout hook
func (c *Conn) OnIdleTimeout(f func(conn *Conn)) {
	c.onIdleTimeout = f
}

// Client represents a WebSocket client
type Client struct {
	config *Config

	// Client state
	mu     sync.RWMutex
	conn   *Conn
	stopCh chan struct{}
}

// NewServer creates a new WebSocket server
func NewServer(config *Config) *Server {
	if config == nil {
		config = DefaultConfig()
	}

	return &Server{
		config: config,
		stopCh: make(chan struct{}),
		connPool: sync.Pool{
			New: func() interface{} {
				ctx, cancel := context.WithCancel(context.Background())
				return &Conn{ctx: ctx, cancel: cancel}
			},
		},
	}
}

// NewClient creates a new WebSocket client
func NewClient(config *Config) *Client {
	if config == nil {
		config = DefaultConfig()
	}

	return &Client{
		config: config,
		stopCh: make(chan struct{}),
	}
}

// Listen starts the server on the given address
func (s *Server) Listen(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return errors.New("server already running")
	}

	var err error
	if s.tlsConfig != nil {
		s.listener, err = tls.Listen("tcp", addr, s.tlsConfig)
	} else {
		s.listener, err = net.Listen("tcp", addr)
	}

	if err != nil {
		return err
	}

	s.running = true
	go s.acceptConnections()

	return nil
}

// acceptConnections handles incoming connections
func (s *Server) acceptConnections() {
	for {
		select {
		case <-s.stopCh:
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				if s.onError != nil {
					s.onError(err)
				}
				continue
			}

			go s.handleConnection(conn)
		}
	}
}

// handleConnection processes a new connection
func (s *Server) handleConnection(netConn net.Conn) {
	defer netConn.Close()

	// Set handshake timeout
	if s.config.HandshakeTimeout > 0 {
		netConn.SetDeadline(time.Now().Add(s.config.HandshakeTimeout))
	}

	conn, err := s.upgrade(netConn)
	if err != nil {
		if s.onError != nil {
			s.onError(err)
		}
		return
	}

	// Clear handshake timeout
	netConn.SetDeadline(time.Time{})

	if s.onConnection != nil {
		s.onConnection(conn)
	}

	// Start connection handling
	conn.handleConnection()
}

// upgrade performs the WebSocket handshake
func (s *Server) upgrade(netConn net.Conn) (*Conn, error) {
	reader := bufio.NewReader(netConn)

	// Read HTTP request
	req, err := http.ReadRequest(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP request: %w", err)
	}

	// Validate WebSocket handshake
	if err := s.validateHandshake(req); err != nil {
		s.writeHandshakeError(netConn, err)
		return nil, err
	}

	// Generate accept key
	acceptKey := s.generateAcceptKey(req.Header.Get("Sec-WebSocket-Key"))

	// Negotiate subprotocol
	subprotocol := s.negotiateSubprotocol(req)

	// Negotiate extensions (permessage-deflate)
	extHeader := req.Header.Get("Sec-WebSocket-Extensions")
	negotiatedExtensions := make(map[string]string)
	compressionEnabled := false
	if s.config.EnableCompression && extHeader != "" {
		exts := strings.Split(extHeader, ",")
		for _, ext := range exts {
			ext = strings.TrimSpace(ext)
			if strings.HasPrefix(ext, "permessage-deflate") {
				compressionEnabled = true
				negotiatedExtensions["permessage-deflate"] = ext
				break
			}
		}
	}

	// Write handshake response with negotiated extensions
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey + "\r\n"
	if subprotocol != "" {
		response += "Sec-WebSocket-Protocol: " + subprotocol + "\r\n"
	}
	if compressionEnabled {
		response += "Sec-WebSocket-Extensions: permessage-deflate\r\n"
	}
	response += "\r\n"
	if _, err := netConn.Write([]byte(response)); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	conn := &Conn{
		conn:                  netConn,
		config:                s.config,
		isServer:              true,
		reader:                reader,
		writer:                bufio.NewWriter(netConn),
		messageBuffer:         make([]byte, 0, s.config.ReadBufferSize),
		pongCh:                make(chan bool, 1),
		customHandlers:        make(map[string]EventHandler),
		negotiatedSubprotocol: subprotocol,
		negotiatedExtensions:  negotiatedExtensions,
		compressionEnabled:    compressionEnabled,
		ctx:                   ctx,
		cancel:                cancel,
	}

	// Set default event handlers
	conn.setDefaultHandlers()

	return conn, nil
}

// validateHandshake validates the WebSocket handshake request
func (s *Server) validateHandshake(req *http.Request) error {
	// Check method
	if req.Method != "GET" {
		return ErrInvalidHandshake
	}

	// Check required headers
	if req.Header.Get("Upgrade") != "websocket" {
		return ErrInvalidHandshake
	}

	connection := req.Header.Get("Connection")
	if !strings.Contains(strings.ToLower(connection), "upgrade") {
		return ErrInvalidHandshake
	}

	if req.Header.Get("Sec-WebSocket-Version") != "13" {
		return ErrInvalidHandshake
	}

	if req.Header.Get("Sec-WebSocket-Key") == "" {
		return ErrInvalidHandshake
	}

	// Check origin if configured
	if s.config.CheckOrigin != nil && !s.config.CheckOrigin(req) {
		return ErrOriginNotAllowed
	}

	// Check authentication if configured (NEW)
	if s.config.CheckAuth != nil && !s.config.CheckAuth(req) {
		return ErrInvalidHandshake
	}

	return nil
}

// generateAcceptKey generates the Sec-WebSocket-Accept header value
func (s *Server) generateAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + websocketMagicString))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// negotiateSubprotocol negotiates the subprotocol
func (s *Server) negotiateSubprotocol(req *http.Request) string {
	if len(s.config.Subprotocols) == 0 {
		return ""
	}

	clientProtocols := req.Header["Sec-Websocket-Protocol"]
	for _, clientProto := range clientProtocols {
		for _, proto := range strings.Split(clientProto, ",") {
			proto = strings.TrimSpace(proto)
			for _, serverProto := range s.config.Subprotocols {
				if proto == serverProto {
					return proto
				}
			}
		}
	}

	return ""
}

// writeHandshakeResponse writes the WebSocket handshake response
func (s *Server) writeHandshakeResponse(conn net.Conn, acceptKey, subprotocol string) error {
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey + "\r\n"

	if subprotocol != "" {
		response += "Sec-WebSocket-Protocol: " + subprotocol + "\r\n"
	}

	response += "\r\n"

	_, err := conn.Write([]byte(response))
	return err
}

// writeHandshakeError writes an error response for failed handshake
func (s *Server) writeHandshakeError(conn net.Conn, err error) {
	var status string
	switch err {
	case ErrOriginNotAllowed:
		status = "403 Forbidden"
	case ErrSubprotocolMismatch:
		status = "400 Bad Request"
	default:
		status = "400 Bad Request"
	}

	response := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Length: 0\r\n\r\n", status)
	conn.Write([]byte(response))
}

// Connect connects the client to a WebSocket server
func (c *Client) Connect(urlStr string, headers http.Header) (*Conn, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	var netConn net.Conn
	addr := u.Host
	if u.Port() == "" {
		if u.Scheme == "wss" {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}

	// Connect to server
	if u.Scheme == "wss" {
		netConn, err = tls.Dial("tcp", addr, c.config.TLSConfig)
	} else {
		netConn, err = net.Dial("tcp", addr)
	}

	if err != nil {
		return nil, err
	}

	// Perform handshake
	conn, err := c.handshake(netConn, u, headers)
	if err != nil {
		netConn.Close()
		return nil, err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// Start connection handling
	go conn.handleConnection()

	return conn, nil
}

// handshake performs client-side WebSocket handshake
func (c *Client) handshake(netConn net.Conn, u *url.URL, headers http.Header) (*Conn, error) {
	// Generate WebSocket key
	key := c.generateKey()

	// Build handshake request
	req := fmt.Sprintf("GET %s HTTP/1.1\r\n", u.RequestURI())
	req += fmt.Sprintf("Host: %s\r\n", u.Host)
	req += "Upgrade: websocket\r\n"
	req += "Connection: Upgrade\r\n"
	req += fmt.Sprintf("Sec-WebSocket-Key: %s\r\n", key)
	req += "Sec-WebSocket-Version: 13\r\n"

	// Add subprotocols
	if len(c.config.Subprotocols) > 0 {
		req += fmt.Sprintf("Sec-WebSocket-Protocol: %s\r\n", strings.Join(c.config.Subprotocols, ", "))
	}

	// Add custom headers
	for name, values := range headers {
		for _, value := range values {
			req += fmt.Sprintf("%s: %s\r\n", name, value)
		}
	}

	req += "\r\n"

	// Send handshake request
	if _, err := netConn.Write([]byte(req)); err != nil {
		return nil, err
	}

	// Read handshake response
	reader := bufio.NewReader(netConn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Validate response
	if err := c.validateResponse(resp, key); err != nil {
		return nil, err
	}

	// Create connection
	conn := &Conn{
		conn:           netConn,
		config:         c.config,
		isClient:       true,
		reader:         reader,
		writer:         bufio.NewWriter(netConn),
		messageBuffer:  make([]byte, 0, c.config.ReadBufferSize),
		pongCh:         make(chan bool, 1),
		customHandlers: make(map[string]EventHandler),
	}

	// Set default handlers
	conn.setDefaultHandlers()

	return conn, nil
}

// generateKey generates a random WebSocket key
func (c *Client) generateKey() string {
	key := make([]byte, 16)
	rand.Read(key)
	return base64.StdEncoding.EncodeToString(key)
}

// validateResponse validates the handshake response
func (c *Client) validateResponse(resp *http.Response, key string) error {
	if resp.StatusCode != 101 {
		return ErrInvalidHandshake
	}

	if resp.Header.Get("Upgrade") != "websocket" {
		return ErrInvalidHandshake
	}

	connection := resp.Header.Get("Connection")
	if !strings.Contains(strings.ToLower(connection), "upgrade") {
		return ErrInvalidHandshake
	}

	// Validate accept key
	expectedAccept := c.generateAcceptKey(key)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		return ErrInvalidHandshake
	}

	return nil
}

// generateAcceptKey generates the expected accept key for validation
func (c *Client) generateAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + websocketMagicString))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Connection methods

// setDefaultHandlers sets up default event handlers
func (c *Conn) setDefaultHandlers() {
	// Default ping handler - respond with pong
	c.onPing = func(conn *Conn, data []byte) {
		conn.WritePong(data)
	}

	// Default pong handler - signal keep-alive
	c.onPong = func(conn *Conn, data []byte) {
		select {
		case conn.pongCh <- true:
		default:
		}
	}

	// Default close handler
	c.onClose = func(conn *Conn, code CloseCode, reason string) {
		conn.close(code, reason)
	}

	// Default error handler
	c.onError = func(conn *Conn, err error) {
		fmt.Fprintf(os.Stderr, "[websocket] error: %v\n", err)
	}
}

// handleConnection manages the connection lifecycle
func (c *Conn) handleConnection() {
	defer c.cleanup()

	// Start keep-alive if enabled
	if c.config.EnablePingPong && c.isServer {
		c.startKeepAlive()
	}

	// Call onOpen handler
	if c.onOpen != nil {
		c.onOpen(c)
	}

	// Main message loop
	for {
		if c.isClosed() {
			break
		}

		messageType, data, err := c.ReadMessage()
		if err != nil {
			if c.isClosed() {
				break
			}
			if c.onError != nil {
				c.onError(c, err)
			}
			break
		}

		// Handle control frames internally
		switch messageType {
		case PingMessage:
			if c.onPing != nil {
				c.onPing(c, data)
			}
		case PongMessage:
			if c.onPong != nil {
				c.onPong(c, data)
			}
		case CloseMessage:
			// Parse close frame
			code := CloseNormalClosure
			reason := ""
			if len(data) >= 2 {
				code = CloseCode(binary.BigEndian.Uint16(data[:2]))
				if len(data) > 2 {
					reason = string(data[2:])
				}
			}
			if c.onClose != nil {
				c.onClose(c, code, reason)
			}
			return
		default:
			// Data frames
			if c.onMessage != nil {
				c.onMessage(c, messageType, data)
			}
		}
	}
}

// ReadMessage reads a complete message from the connection
func (c *Conn) ReadMessage() (MessageType, []byte, error) {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return 0, nil, ErrConnectionClosed
	}

	// Reset message buffer
	c.messageBuffer = c.messageBuffer[:0]
	var messageType MessageType

	for {
		frame, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}

		// Handle control frames immediately
		if frame.Opcode >= 0x8 {
			return MessageType(frame.Opcode), frame.Payload, nil
		}

		// Handle data frames
		if frame.Opcode == OpcodeContinuation {
			// Continuation frame
			if messageType == 0 {
				return 0, nil, ErrInvalidFrame
			}
		} else {
			// First frame of message
			messageType = MessageType(frame.Opcode)
		}

		// Append payload to message buffer
		c.messageBuffer = append(c.messageBuffer, frame.Payload...)

		// Check message size limit
		if int64(len(c.messageBuffer)) > c.config.MaxMessageSize {
			return 0, nil, ErrMessageTooLarge
		}

		// If FIN is set, message is complete
		if frame.FIN {
			// Make a copy of the message buffer
			message := make([]byte, len(c.messageBuffer))
			copy(message, c.messageBuffer)
			return messageType, message, nil
		}
	}
}

// readFrame reads a single WebSocket frame
func (c *Conn) readFrame() (*Frame, error) {
	// Set read deadline if configured
	if c.config != nil && c.config.HandshakeTimeout > 0 {
		c.conn.SetReadDeadline(time.Now().Add(c.config.HandshakeTimeout))
	}
	// Read first two bytes (header)
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return nil, err
	}

	frame := &Frame{
		FIN:    header[0]&FlagFIN != 0,
		RSV1:   header[0]&FlagRSV1 != 0,
		RSV2:   header[0]&FlagRSV2 != 0,
		RSV3:   header[0]&FlagRSV3 != 0,
		Opcode: header[0] & 0x0F,
		Masked: header[1]&FlagMask != 0,
	}

	// Validate opcode
	if !c.isValidOpcode(frame.Opcode) {
		return nil, ErrInvalidOpcode
	}

	// Validate masking (clients must mask, servers must not)
	if c.isServer {
		if !frame.Masked {
			return nil, ErrInvalidFrame
		}
	} else if c.isClient {
		if frame.Masked {
			return nil, ErrInvalidFrame
		}
	}

	// Read payload length
	payloadLen := int64(header[1] & 0x7F)

	if payloadLen == 126 {
		// 16-bit length
		lengthBytes := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, lengthBytes); err != nil {
			return nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(lengthBytes))
	} else if payloadLen == 127 {
		// 64-bit length
		lengthBytes := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, lengthBytes); err != nil {
			return nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint64(lengthBytes))
	}

	// Check frame size limit
	if payloadLen > c.config.MaxFrameSize {
		return nil, ErrMessageTooLarge
	}

	// Read masking key if present
	var maskKey []byte
	if frame.Masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(c.reader, maskKey); err != nil {
			return nil, err
		}
	}

	// Read payload
	if payloadLen > 0 {
		frame.Payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(c.reader, frame.Payload); err != nil {
			return nil, err
		}

		// Unmask payload if masked
		if frame.Masked {
			c.maskBytes(frame.Payload, maskKey)
		}
	}

	return frame, nil
}

// WriteMessage writes a complete message to the connection
func (c *Conn) WriteMessage(messageType MessageType, data []byte) error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return ErrConnectionClosed
	}

	frame := &Frame{
		FIN:     true,
		Opcode:  byte(messageType),
		Payload: data,
		Masked:  c.isClient, // Clients must mask frames
	}

	return c.writeFrame(frame)
}

// WriteText writes a text message
func (c *Conn) WriteText(text string) error {
	return c.WriteMessage(TextMessage, []byte(text))
}

// WriteBinary writes a binary message
func (c *Conn) WriteBinary(data []byte) error {
	return c.WriteMessage(BinaryMessage, data)
}

// WritePing writes a ping frame
func (c *Conn) WritePing(data []byte) error {
	frame := &Frame{
		FIN:     true,
		Opcode:  OpcodePing,
		Payload: data,
		Masked:  c.isClient,
	}
	return c.writeFrame(frame)
}

// WritePong writes a pong frame
func (c *Conn) WritePong(data []byte) error {
	frame := &Frame{
		FIN:     true,
		Opcode:  OpcodePong,
		Payload: data,
		Masked:  c.isClient,
	}
	return c.writeFrame(frame)
}

// WriteClose writes a close frame
func (c *Conn) WriteClose(code CloseCode, reason string) error {
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload[:2], uint16(code))
	copy(payload[2:], reason)

	frame := &Frame{
		FIN:     true,
		Opcode:  OpcodeClose,
		Payload: payload,
		Masked:  c.isClient,
	}

	return c.writeFrame(frame)
}

// writeFrame writes a single frame to the connection
func (c *Conn) writeFrame(frame *Frame) error {
	// Set write deadline if configured
	if c.config != nil && c.config.HandshakeTimeout > 0 {
		c.conn.SetWriteDeadline(time.Now().Add(c.config.HandshakeTimeout))
	}
	// Compress payload if permessage-deflate is enabled and not a control frame
	if c.compressionEnabled && (frame.Opcode == OpcodeText || frame.Opcode == OpcodeBinary) {
		var b bytes.Buffer
		comp, _ := flate.NewWriter(&b, flate.DefaultCompression)
		comp.Write(frame.Payload)
		comp.Close()
		frame.Payload = b.Bytes()
		frame.RSV1 = true
	}
	// Calculate frame size
	headerSize := 2
	if len(frame.Payload) > MaxPayload16Bit {
		headerSize += 8
	} else if len(frame.Payload) > MaxPayload7Bit {
		headerSize += 2
	}

	if frame.Masked {
		headerSize += 4
	}

	// Create frame buffer
	frameBytes := make([]byte, headerSize+len(frame.Payload))

	// First byte: FIN + RSV + Opcode
	frameBytes[0] = frame.Opcode
	if frame.FIN {
		frameBytes[0] |= FlagFIN
	}
	if frame.RSV1 {
		frameBytes[0] |= FlagRSV1
	}
	if frame.RSV2 {
		frameBytes[0] |= FlagRSV2
	}
	if frame.RSV3 {
		frameBytes[0] |= FlagRSV3
	}

	// Second byte: MASK + Payload length
	payloadLen := len(frame.Payload)
	offset := 2

	if payloadLen > MaxPayload16Bit {
		frameBytes[1] = 127
		binary.BigEndian.PutUint64(frameBytes[2:10], uint64(payloadLen))
		offset = 10
	} else if payloadLen > MaxPayload7Bit {
		frameBytes[1] = 126
		binary.BigEndian.PutUint16(frameBytes[2:4], uint16(payloadLen))
		offset = 4
	} else {
		frameBytes[1] = byte(payloadLen)
	}

	if frame.Masked {
		frameBytes[1] |= FlagMask

		// Generate masking key
		maskKey := make([]byte, 4)
		rand.Read(maskKey)
		copy(frameBytes[offset:offset+4], maskKey)
		offset += 4

		// Copy and mask payload
		copy(frameBytes[offset:], frame.Payload)
		c.maskBytes(frameBytes[offset:], maskKey)
	} else {
		// Copy payload without masking
		copy(frameBytes[offset:], frame.Payload)
	}

	// Write frame
	_, err := c.writer.Write(frameBytes)
	if err != nil {
		return err
	}

	return c.writer.Flush()
}

// Helper methods

// isValidOpcode checks if the opcode is valid
func (c *Conn) isValidOpcode(opcode byte) bool {
	switch opcode {
	case OpcodeContinuation, OpcodeText, OpcodeBinary,
		OpcodeClose, OpcodePing, OpcodePong:
		return true
	default:
		return false
	}
}

// maskBytes applies WebSocket masking to data
func (c *Conn) maskBytes(data, key []byte) {
	for i := range data {
		data[i] ^= key[i%4]
	}
}

// startKeepAlive starts the ping/pong keep-alive mechanism
func (c *Conn) startKeepAlive() {
	if c.config.PingInterval <= 0 {
		return
	}

	c.pingTicker = time.NewTicker(c.config.PingInterval)
	c.keepAliveStopCh = make(chan struct{})

	go func() {
		defer c.pingTicker.Stop()
		for {
			select {
			case <-c.pingTicker.C:
				if c.isClosed() {
					return
				}
				// Send ping
				if err := c.WritePing(nil); err != nil {
					if c.onError != nil {
						c.onError(c, err)
					}
					return
				}
				// Start pong timeout
				if c.config.PongTimeout > 0 {
					c.pongTimer = time.NewTimer(c.config.PongTimeout)
					select {
					case <-c.pongCh:
						c.pongTimer.Stop()
					case <-c.pongTimer.C:
						c.close(CloseGoingAway, "pong timeout")
						return
					case <-c.ctx.Done():
						return
					}
				}
			case <-c.keepAliveStopCh:
				return
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

// Extension is a plugin interface for protocol extensions
type Extension interface {
	Name() string
	Negotiate(client, server map[string]string) (map[string]string, error)
	WrapConn(c *Conn) error
}

// Example: permessage-deflate extension plugin stub
type PerMessageDeflateExtension struct{}

func (e *PerMessageDeflateExtension) Name() string {
	return "permessage-deflate"
}

func (e *PerMessageDeflateExtension) Negotiate(client, server map[string]string) (map[string]string, error) {
	// TODO: implement negotiation logic
	return map[string]string{"enabled": "true"}, nil
}

func (e *PerMessageDeflateExtension) WrapConn(c *Conn) error {
	// TODO: wrap Conn for compression
	return nil
}

// stopKeepAlive returns a channel that signals keep-alive should stop
func (c *Conn) stopKeepAlive() <-chan struct{} {
	return c.keepAliveStopCh
}

// Close closes the connection gracefully
func (c *Conn) Close() error {
	return c.close(CloseNormalClosure, "")
}

// close closes the connection with a specific code and reason
func (c *Conn) close(code CloseCode, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	c.closeReason = code
	c.closeText = reason

	// Send close frame
	c.WriteClose(code, reason)

	// Unblock any reads by setting deadlines
	c.conn.SetReadDeadline(time.Now())
	c.conn.SetWriteDeadline(time.Now())

	// Close underlying connection
	return c.conn.Close()
}

// isClosed returns true if the connection is closed
func (c *Conn) isClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// cleanup performs connection cleanup
func (c *Conn) cleanup() {
	if c.pingTicker != nil {
		c.pingTicker.Stop()
	}
	if c.pongTimer != nil {
		c.pongTimer.Stop()
	}
	if c.keepAliveStopCh != nil {
		close(c.keepAliveStopCh)
	}
}

// Event handler methods

// OnOpen sets the connection open handler
func (c *Conn) OnOpen(handler OpenHandler) {
	c.onOpen = handler
}

// OnMessage sets the message handler
func (c *Conn) OnMessage(handler MessageHandler) {
	c.onMessage = handler
}

// OnClose sets the close handler
func (c *Conn) OnClose(handler CloseHandler) {
	c.onClose = handler
}

// OnError sets the error handler
func (c *Conn) OnError(handler ErrorHandler) {
	c.onError = handler
}

// OnPing sets the ping handler
func (c *Conn) OnPing(handler PingHandler) {
	c.onPing = handler
}

// OnPong sets the pong handler
func (c *Conn) OnPong(handler PongHandler) {
	c.onPong = handler
}

// Custom event handlers

// On registers a custom event handler
func (c *Conn) On(event string, handler EventHandler) {
	c.eventMu.Lock()
	c.customHandlers[event] = handler
	c.eventMu.Unlock()
}

// Emit triggers a custom event
func (c *Conn) Emit(event string, data interface{}) {
	c.eventMu.RLock()
	handler, exists := c.customHandlers[event]
	c.eventMu.RUnlock()

	if !exists {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			if c.onError != nil {
				c.onError(c, fmt.Errorf("panic in event handler for '%s': %v", event, r))
			}
		}
	}()

	// Use reflection or type assertion to call handler
	switch h := handler.(type) {
	case func(*Conn, interface{}):
		h(c, data)
	case func(interface{}):
		h(data)
	case func():
		h()
	}
}

// Server event handlers

// OnConnection sets the connection handler for the server
func (s *Server) OnConnection(handler func(conn *Conn)) {
	s.onConnection = handler
}

// OnError sets the error handler for the server
func (s *Server) OnError(handler func(err error)) {
	s.onError = handler
}

// Stop gracefully stops the server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	close(s.stopCh)
	s.running = false

	if s.listener != nil {
		return s.listener.Close()
	}

	return nil
}

// Utility functions

// IsWebSocketUpgrade checks if an HTTP request is a WebSocket upgrade request
func IsWebSocketUpgrade(req *http.Request) bool {
	return req.Header.Get("Upgrade") == "websocket" &&
		strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade") &&
		req.Header.Get("Sec-WebSocket-Version") == "13" &&
		req.Header.Get("Sec-WebSocket-Key") != ""
}
