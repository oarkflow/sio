package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/oarkflow/sio/websocket"
)

func main() {
	// Create server with custom configuration
	config := websocket.DefaultConfig()
	config.MaxMessageSize = 1024 * 1024 // 1MB
	config.PingInterval = 30 * time.Second
	config.EnablePingPong = true

	// Allow all origins for demo purposes (be more restrictive in production)
	config.CheckOrigin = func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		fmt.Printf("Origin check: %s (allowed)\n", origin)
		return true // Allow all origins for demo
	}

	// Enable subprotocols
	config.Subprotocols = []string{"chat", "echo", "binary"}

	wsServer := websocket.NewServer(config)

	// Set up connection handler
	wsServer.OnConnection(func(conn *websocket.Conn) {
		fmt.Printf("New WebSocket connection established\n")

		// Set up event handlers for this connection
		conn.OnOpen(func(c *websocket.Conn) {
			fmt.Printf("Connection opened\n")
			c.WriteText("Welcome to WebSocket server!")
		})

		conn.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
			message := string(data)
			fmt.Printf("Received message type %d: %s\n", messageType, message)

			// Handle special ping message from JavaScript client
			if message == "__PING__" {
				c.WriteText("__PONG__")
				return
			}

			switch messageType {
			case websocket.TextMessage:
				// Echo text message
				response := fmt.Sprintf("Echo: %s", message)
				c.WriteText(response)

			case websocket.BinaryMessage:
				// Echo binary message
				fmt.Printf("Received binary data: %v\n", data)
				c.WriteBinary(data)
			}
		})

		conn.OnClose(func(c *websocket.Conn, code websocket.CloseCode, reason string) {
			fmt.Printf("Connection closed with code %d: %s\n", code, reason)
		})

		conn.OnError(func(c *websocket.Conn, err error) {
			fmt.Printf("Connection error: %v\n", err)
		})

		conn.OnPing(func(c *websocket.Conn, data []byte) {
			fmt.Printf("Received ping: %s\n", string(data))
			// Default handler will respond with pong
		})

		conn.OnPong(func(c *websocket.Conn, data []byte) {
			fmt.Printf("Received pong: %s\n", string(data))
		})

		// Custom event handlers for structured messages
		conn.On("chat", func(c *websocket.Conn, data interface{}) {
			fmt.Printf("Chat event: %v\n", data)
		})

		conn.On("broadcast", func(c *websocket.Conn, data interface{}) {
			// Broadcast to all connected clients (simplified example)
			fmt.Printf("Broadcasting: %v\n", data)
		})
	})

	// Set up error handler
	wsServer.OnError(func(err error) {
		log.Printf("WebSocket server error: %v\n", err)
	})

	// Create HTTP server to serve the chat HTML and handle WebSocket upgrades
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a WebSocket upgrade request
		if websocket.IsWebSocketUpgrade(r) {
			fmt.Printf("WebSocket upgrade request from %s\n", r.RemoteAddr)
			// Handle WebSocket upgrade here
			// Note: We need to integrate our WebSocket server with HTTP
			handleWebSocketUpgrade(wsServer, w, r)
			return
		}

		// Serve the chat HTML file
		if r.URL.Path == "/" || r.URL.Path == "/chat" {
			serveHTML(w, r)
			return
		}

		// Serve static files or 404
		http.NotFound(w, r)
	})

	fmt.Println("Starting HTTP server with WebSocket support on :8080")
	fmt.Println("Visit http://localhost:8080 to access the chat client")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}

// serveHTML serves the chat HTML file
func serveHTML(w http.ResponseWriter, r *http.Request) {
	// Get the current directory
	currentDir, err := os.Getwd()
	if err != nil {
		http.Error(w, "Unable to get current directory", http.StatusInternalServerError)
		return
	}

	// Build path to chat.html
	htmlPath := filepath.Join(currentDir, "chat.html")

	// Check if file exists
	if _, err := os.Stat(htmlPath); os.IsNotExist(err) {
		// If chat.html doesn't exist in current directory, serve embedded HTML
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(getEmbeddedHTML()))
		return
	}

	// Serve the file
	http.ServeFile(w, r, htmlPath)
}

// handleWebSocketUpgrade handles the WebSocket upgrade process
func handleWebSocketUpgrade(wsServer *websocket.Server, w http.ResponseWriter, r *http.Request) {
	// Check if this is a proper WebSocket request
	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "Not a WebSocket request", http.StatusBadRequest)
		return
	}

	// Get the underlying connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket upgrade not supported", http.StatusInternalServerError)
		return
	}

	conn, bufReader, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Failed to hijack connection", http.StatusInternalServerError)
		return
	}

	// Use the buffered reader if it has data
	_ = bufReader

	// Handle the connection using our WebSocket server's internal method
	go func() {
		defer conn.Close()
		fmt.Printf("Handling WebSocket upgrade from %s\n", conn.RemoteAddr())
		handleWebSocketConnectionProperly(wsServer, conn, r)
	}()
}

// handleWebSocketConnectionProperly properly handles the WebSocket connection
func handleWebSocketConnectionProperly(wsServer *websocket.Server, conn net.Conn, r *http.Request) {
	// Set handshake timeout (use default timeout since we can't access config)
	conn.SetDeadline(time.Now().Add(45 * time.Second))

	// Perform WebSocket upgrade handshake
	reader := bufio.NewReader(conn)

	// Validate WebSocket handshake
	if err := validateHandshake(r); err != nil {
		writeHandshakeError(conn, err)
		return
	}

	// Generate accept key
	acceptKey := generateAcceptKey(r.Header.Get("Sec-WebSocket-Key"))

	// Write handshake response
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey + "\r\n\r\n"

	if _, err := conn.Write([]byte(response)); err != nil {
		fmt.Printf("Failed to write handshake response: %v\n", err)
		return
	}

	// Clear handshake timeout
	conn.SetDeadline(time.Time{})

	fmt.Printf("WebSocket handshake completed for %s\n", conn.RemoteAddr())

	// Handle the WebSocket connection
	handleBasicWebSocket(conn, reader)
}

// validateHandshake validates the WebSocket handshake request
func validateHandshake(req *http.Request) error {
	if req.Method != "GET" {
		return fmt.Errorf("invalid method")
	}
	if req.Header.Get("Upgrade") != "websocket" {
		return fmt.Errorf("invalid upgrade header")
	}
	if req.Header.Get("Sec-WebSocket-Version") != "13" {
		return fmt.Errorf("invalid websocket version")
	}
	if req.Header.Get("Sec-WebSocket-Key") == "" {
		return fmt.Errorf("missing websocket key")
	}
	return nil
}

// generateAcceptKey generates the Sec-WebSocket-Accept header value
func generateAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// writeHandshakeError writes an error response for failed handshake
func writeHandshakeError(conn net.Conn, err error) {
	response := "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"
	conn.Write([]byte(response))
}

// handleBasicWebSocket handles basic WebSocket communication
func handleBasicWebSocket(conn net.Conn, reader *bufio.Reader) {
	fmt.Printf("Starting WebSocket message loop for %s\n", conn.RemoteAddr())

	for {
		// Read WebSocket frame
		frame, err := readWebSocketFrame(reader)
		if err != nil {
			fmt.Printf("Error reading frame: %v\n", err)
			break
		}

		fmt.Printf("Received WebSocket frame: opcode=%d, payload=%s\n", frame.Opcode, string(frame.Payload))

		// Handle different frame types
		switch frame.Opcode {
		case 0x1: // Text frame
			// Echo the message back
			response := fmt.Sprintf("Echo: %s", string(frame.Payload))
			if err := writeWebSocketFrame(conn, 0x1, []byte(response)); err != nil {
				fmt.Printf("Error writing response: %v\n", err)
				return
			}
		case 0x8: // Close frame
			fmt.Printf("Received close frame\n")
			return
		case 0x9: // Ping frame
			// Respond with pong
			if err := writeWebSocketFrame(conn, 0xA, frame.Payload); err != nil {
				fmt.Printf("Error writing pong: %v\n", err)
				return
			}
		}
	}
}

// WebSocketFrame represents a simple WebSocket frame
type WebSocketFrame struct {
	FIN     bool
	Opcode  byte
	Masked  bool
	Payload []byte
}

// readWebSocketFrame reads a WebSocket frame
func readWebSocketFrame(reader *bufio.Reader) (*WebSocketFrame, error) {
	// Read first two bytes
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}

	frame := &WebSocketFrame{
		FIN:    header[0]&0x80 != 0,
		Opcode: header[0] & 0x0F,
		Masked: header[1]&0x80 != 0,
	}

	// Read payload length
	payloadLen := int64(header[1] & 0x7F)

	if payloadLen == 126 {
		lengthBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, lengthBytes); err != nil {
			return nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(lengthBytes))
	} else if payloadLen == 127 {
		lengthBytes := make([]byte, 8)
		if _, err := io.ReadFull(reader, lengthBytes); err != nil {
			return nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint64(lengthBytes))
	}

	// Read masking key if present
	var maskKey []byte
	if frame.Masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(reader, maskKey); err != nil {
			return nil, err
		}
	}

	// Read payload
	if payloadLen > 0 {
		frame.Payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(reader, frame.Payload); err != nil {
			return nil, err
		}

		// Unmask payload if masked
		if frame.Masked {
			for i := range frame.Payload {
				frame.Payload[i] ^= maskKey[i%4]
			}
		}
	}

	return frame, nil
}

// writeWebSocketFrame writes a WebSocket frame
func writeWebSocketFrame(conn net.Conn, opcode byte, payload []byte) error {
	payloadLen := len(payload)

	// Calculate header size
	headerSize := 2
	if payloadLen > 65535 {
		headerSize += 8
	} else if payloadLen > 125 {
		headerSize += 2
	}

	// Create frame
	frame := make([]byte, headerSize+payloadLen)

	// First byte: FIN + opcode
	frame[0] = 0x80 | opcode // FIN = 1

	// Payload length
	offset := 2
	if payloadLen > 65535 {
		frame[1] = 127
		binary.BigEndian.PutUint64(frame[2:10], uint64(payloadLen))
		offset = 10
	} else if payloadLen > 125 {
		frame[1] = 126
		binary.BigEndian.PutUint16(frame[2:4], uint16(payloadLen))
		offset = 4
	} else {
		frame[1] = byte(payloadLen)
	}

	// Copy payload
	copy(frame[offset:], payload)

	// Write frame
	_, err := conn.Write(frame)
	return err
}

// getEmbeddedHTML returns embedded HTML if the file doesn't exist
func getEmbeddedHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>WebSocket Chat Client</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            margin: 20px;
            background: #f0f0f0;
        }
        .container {
            max-width: 600px;
            margin: 0 auto;
            background: white;
            padding: 20px;
            border-radius: 10px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
        }
        .messages {
            height: 300px;
            border: 1px solid #ddd;
            padding: 10px;
            overflow-y: scroll;
            margin-bottom: 10px;
            background: #fafafa;
        }
        .input-container {
            display: flex;
            gap: 10px;
        }
        input {
            flex: 1;
            padding: 10px;
            border: 1px solid #ddd;
            border-radius: 5px;
        }
        button {
            padding: 10px 20px;
            background: #007bff;
            color: white;
            border: none;
            border-radius: 5px;
            cursor: pointer;
        }
        button:hover {
            background: #0056b3;
        }
        button:disabled {
            background: #ccc;
            cursor: not-allowed;
        }
        .status {
            padding: 10px;
            margin-bottom: 10px;
            border-radius: 5px;
            text-align: center;
        }
        .connected { background: #d4edda; color: #155724; }
        .disconnected { background: #f8d7da; color: #721c24; }
        .connecting { background: #fff3cd; color: #856404; }
    </style>
</head>
<body>
    <div class="container">
        <h1>WebSocket Chat Client</h1>
        <div class="status disconnected" id="status">Disconnected</div>
        <div class="messages" id="messages"></div>
        <div class="input-container">
            <input type="text" id="messageInput" placeholder="Type a message..." disabled>
            <button id="sendButton" disabled>Send</button>
            <button id="connectButton">Connect</button>
        </div>
    </div>

    <script>
        let ws = null;
        let connected = false;

        const status = document.getElementById('status');
        const messages = document.getElementById('messages');
        const messageInput = document.getElementById('messageInput');
        const sendButton = document.getElementById('sendButton');
        const connectButton = document.getElementById('connectButton');

        function addMessage(text, type = 'info') {
            const div = document.createElement('div');
            div.textContent = new Date().toLocaleTimeString() + ' - ' + text;
            div.style.marginBottom = '5px';
            div.style.color = type === 'error' ? 'red' : type === 'sent' ? 'blue' : 'black';
            messages.appendChild(div);
            messages.scrollTop = messages.scrollHeight;
        }

        function updateStatus(text, className) {
            status.textContent = text;
            status.className = 'status ' + className;
        }

        function connect() {
            if (connected) return;

            updateStatus('Connecting...', 'connecting');
            ws = new WebSocket('ws://localhost:8080');

            ws.onopen = function() {
                connected = true;
                updateStatus('Connected', 'connected');
                addMessage('Connected to server');
                messageInput.disabled = false;
                sendButton.disabled = false;
                connectButton.textContent = 'Disconnect';
            };

            ws.onmessage = function(event) {
                addMessage('Received: ' + event.data, 'received');
            };

            ws.onclose = function() {
                connected = false;
                updateStatus('Disconnected', 'disconnected');
                addMessage('Disconnected from server', 'error');
                messageInput.disabled = true;
                sendButton.disabled = true;
                connectButton.textContent = 'Connect';
            };

            ws.onerror = function(error) {
                addMessage('Error: ' + error, 'error');
            };
        }

        function disconnect() {
            if (ws && connected) {
                ws.close();
            }
        }

        function sendMessage() {
            const message = messageInput.value.trim();
            if (message && connected) {
                ws.send(message);
                addMessage('Sent: ' + message, 'sent');
                messageInput.value = '';
            }
        }

        connectButton.addEventListener('click', function() {
            if (connected) {
                disconnect();
            } else {
                connect();
            }
        });

        sendButton.addEventListener('click', sendMessage);

        messageInput.addEventListener('keypress', function(e) {
            if (e.key === 'Enter') {
                sendMessage();
            }
        });

        // Auto-connect
        connect();
    </script>
</body>
</html>`
}
