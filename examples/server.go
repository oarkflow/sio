package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oarkflow/sio/chat"
	"github.com/oarkflow/sio/websocket"
)

func main() {
	var (
		port        = flag.Int("port", 8080, "Server port")
		host        = flag.String("host", "localhost", "Server host")
		dbURL       = flag.String("db", "", "Database URL (empty for in-memory)")
		tlsCert     = flag.String("tls-cert", "", "TLS certificate file")
		tlsKey      = flag.String("tls-key", "", "TLS key file")
		maxMsgLen   = flag.Int("max-msg-len", 4096000, "Maximum message length")
		rateLimit   = flag.Int("rate-limit", 100, "Rate limit per minute per client")
		corsOrigins = flag.String("cors-origins", "*", "Allowed CORS origins (comma-separated)")
	)
	flag.Parse()

	// Create database
	var db chat.Database
	if *dbURL != "" {
		// Use PostgreSQL (would need to implement)
		log.Printf("PostgreSQL support not implemented yet, using in-memory database")
		db = chat.NewInMemoryDB()
	} else {
		db = chat.NewInMemoryDB()
	}

	// Create WebSocket configuration
	wsConfig := websocket.DefaultConfig()
	wsConfig.MaxMessageSize = int64(*maxMsgLen)
	wsConfig.MaxFrameSize = int64(*maxMsgLen) // Ensure frame size matches message size
	wsConfig.CheckOrigin = func(r *http.Request) bool {
		if *corsOrigins == "*" {
			return true
		}
		// Implement origin checking based on corsOrigins
		return true
	}

	// Create security configuration
	security := &chat.SecurityConfig{
		MaxMessageLength: *maxMsgLen,
		MaxRoomsPerUser:  50,
		AllowedOrigins:   []string{*corsOrigins},
		RequireAuth:      false, // Set to true for production
		RateLimitPerSocket: &chat.RateLimit{
			Requests:   *rateLimit,
			WindowSize: time.Minute,
		},
		RateLimitPerRoom: &chat.RateLimit{
			Requests:   *rateLimit * 10,
			WindowSize: time.Minute,
		},
	}

	// Create chat server configuration
	config := &chat.Config{
		Port:        *port,
		Host:        *host,
		Database:    db,
		Security:    security,
		TLSCertFile: *tlsCert,
		TLSKeyFile:  *tlsKey,
		WSConfig:    wsConfig,
	}

	// Create chat server
	chatServer := chat.NewServer(config)

	// Create HTTP handler for REST API
	httpHandler := chat.NewHTTPHandler(chatServer)

	// Set up HTTP routes
	mux := http.NewServeMux()

	// REST API endpoints (this includes the /ws endpoint)
	httpHandler.SetupRoutes(mux)

	// Add a simple health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"chat-server"}`))
	})

	// Add a simple static file server for the demo client
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	// Serve demo page as default
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, "./static/demo.html")
		} else {
			http.NotFound(w, r)
		}
	})

	// Apply CORS middleware
	handler := httpHandler.CORS(mux)

	// Create HTTP server for REST API and static files
	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", *host, *port+1),
		Handler: handler,
	}

	// Start chat WebSocket server
	go func() {
		log.Printf("Starting chat WebSocket server on %s:%d", *host, *port)
		if err := chatServer.Start(); err != nil {
			log.Fatalf("Failed to start chat server: %v", err)
		}
	}()

	// Start HTTP server for REST API and static files
	go func() {
		log.Printf("Starting HTTP API server on %s:%d", *host, *port+1)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down servers...")

	// Stop chat server
	if err := chatServer.Stop(); err != nil {
		log.Printf("Error stopping chat server: %v", err)
	}

	// Stop HTTP server
	if err := httpServer.Close(); err != nil {
		log.Printf("Error stopping HTTP server: %v", err)
	}

	log.Println("Servers stopped")
}
