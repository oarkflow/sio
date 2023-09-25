package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/oarkflow/sio"
	"github.com/oarkflow/sio/chi"
	"github.com/oarkflow/sio/chi/middleware"
)

func main() {
	srv := chi.NewRouter()
	srv.Use(middleware.AllowAll().Handler)
	server := sio.New(sio.Config{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		EnableCompression: true,
	})
	sioEvents(server)
	srv.Handle("/socket", server)
	srv.Mount("/", http.FileServer(http.Dir("webroot")))
	c := make(chan bool)
	server.EnableSignalShutdown(c)
	go func() {
		<-c
		os.Exit(0)
	}()
	slog.Info("Listening on http://localhost:8085")
	err := http.ListenAndServe(":8085", srv)
	if err != nil {
		log.Fatal(err)
	}
}

func sioEvents(server *sio.Server) {
	peerRemove := func(socket *sio.Socket) {
		for id, con := range server.SocketList() {
			con.Emit("action:peer-remove", map[string]any{
				"peer_id": socket.ID(),
			})
			socket.Emit("action:peer-remove", map[string]any{
				"peer_id": id,
			})
		}
		socket.LeaveAll()
	}
	server.OnConnect(func(socket *sio.Socket) error {
		return nil
	})
	server.OnDisconnect(func(socket *sio.Socket) error {
		peerRemove(socket)
		return nil
	})
	server.On("request:room-join", func(socket *sio.Socket, data []byte) {
		var d map[string]any
		err := json.Unmarshal(data, &d)
		if err == nil {
			room := d["room_id"].(string)
			socket.Join(room)
			for id, _ := range server.RoomSocketList(room) {
				if socket.ID() != id {
					socket.Emit("action:peer-add", map[string]any{
						"peer_id":             id,
						"should_create_offer": false,
						"channel":             room,
					})
				}
			}
			socket.BroadcastExcept([]string{socket.ID()}, "action:peer-add", map[string]any{
				"peer_id":             socket.ID(),
				"should_create_offer": false,
				"channel":             room,
			})
		}
	})
	server.On("request:room-leave", func(socket *sio.Socket, data []byte) {
		peerRemove(socket)
		room := string(data)
		socket.Leave(room)
	})

	server.On("request:candidate-peer", func(socket *sio.Socket, data []byte) {
		var config map[string]any
		err := json.Unmarshal(data, &config)
		if err == nil {
			if iceCandidate, exists := config["ice_candidate"]; exists {
				peerID := config["peer_id"].(string)
				for id, con := range server.SocketList() {
					if peerID == id {
						con.Emit("action:candidate-peer", map[string]any{
							"peer_id":       socket.ID(),
							"ice_candidate": iceCandidate,
						})
					}
				}
			}
		}
	})

	server.On("request:media-stopped", func(socket *sio.Socket, data []byte) {
		socket.BroadcastExcept([]string{socket.ID()}, "action:media-stopped", map[string]any{
			"peer_id": socket.ID(),
		})
	})

	server.On("request:peer-session", func(socket *sio.Socket, data []byte) {
		var config map[string]any
		err := json.Unmarshal(data, &config)
		if err == nil {
			if sessionDescription, exists := config["session_description"]; exists {
				peerID := config["peer_id"].(string)
				for id, con := range server.SocketList() {
					if peerID == id {
						con.Emit("action:peer-session", map[string]any{
							"peer_id":             socket.ID(),
							"session_description": sessionDescription,
						})
					}
				}
			}
		}
	})

	server.On("request:send-message", func(socket *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if v, exists := room["room_id"]; exists {
				server.ToRoomExcept(v.(string), []string{socket.ID()}, "action:message-received", room)
			}
		}
	})

	server.On("request:typing-start", func(socket *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if v, exists := room["room_id"]; exists {
				server.ToRoomExcept(v.(string), []string{socket.ID()}, "action:peer-typing-start", room)
			}
		}
	})
	server.On("request:typing-stopped", func(socket *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if v, exists := room["room_id"]; exists {
				server.ToRoomExcept(v.(string), []string{socket.ID()}, "action:peer-typing-stop", room)
			}
		}
	})
}
