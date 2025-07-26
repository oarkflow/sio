package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/oarkflow/sio/websocket"
)

func main() {
	// Create client with configuration
	config := websocket.DefaultConfig()
	config.MaxMessageSize = 1024 * 1024 // 1MB
	config.PingInterval = 30 * time.Second

	client := websocket.NewClient(config)

	// Connect to WebSocket server
	headers := make(http.Header)
	headers.Add("User-Agent", "WebSocket-Client/1.0")
	headers.Add("Origin", "http://localhost:3000") // Add allowed origin

	conn, err := client.Connect("ws://localhost:8080", headers) // Remove /ws path
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}

	fmt.Println("Connected to WebSocket server")

	// Set up event handlers
	conn.OnOpen(func(c *websocket.Conn) {
		fmt.Println("Connection opened")

		// Send initial message
		c.WriteText("Hello from client!")

		// Send binary data
		c.WriteBinary([]byte{0x01, 0x02, 0x03, 0x04})

		// Send ping
		c.WritePing([]byte("ping data"))
	})

	conn.OnMessage(func(c *websocket.Conn, messageType websocket.MessageType, data []byte) {
		switch messageType {
		case websocket.TextMessage:
			fmt.Printf("Received text: %s\n", string(data))
		case websocket.BinaryMessage:
			fmt.Printf("Received binary: %v\n", data)
		}
	})

	conn.OnClose(func(c *websocket.Conn, code websocket.CloseCode, reason string) {
		fmt.Printf("Connection closed: %d - %s\n", code, reason)
	})

	conn.OnError(func(c *websocket.Conn, err error) {
		fmt.Printf("Error: %v\n", err)
	})

	conn.OnPing(func(c *websocket.Conn, data []byte) {
		fmt.Printf("Received ping: %s\n", string(data))
	})

	conn.OnPong(func(c *websocket.Conn, data []byte) {
		fmt.Printf("Received pong: %s\n", string(data))
	})

	// Send periodic messages
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	counter := 0
	go func() {
		for {
			select {
			case <-ticker.C:
				counter++
				message := fmt.Sprintf("Message %d from client", counter)
				if err := conn.WriteText(message); err != nil {
					fmt.Printf("Failed to send message: %v\n", err)
					return
				}
			}
		}
	}()

	// Keep client running
	time.Sleep(30 * time.Second)

	// Close connection gracefully
	fmt.Println("Closing connection...")
	conn.Close()
}
