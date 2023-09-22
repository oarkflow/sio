package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/oarkflow/sio"
	"github.com/oarkflow/sio/chi"
)

func main() {
	srv := chi.NewRouter()
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
	err := http.ListenAndServe(":8085", srv)
	if err != nil {
		log.Fatal(err)
	}
}

func sioEvents(server *sio.Server) {
	server.OnConnect(func(socket *sio.Socket) error {
		return nil
	})
	server.OnDisconnect(func(socket *sio.Socket) error {
		fmt.Println("Disconnected")
		for id, con := range server.SocketList() {
			con.Emit("removePeer", map[string]any{
				"peer_id": socket.ID(),
			})
			socket.Emit("removePeer", map[string]any{
				"peer_id": id,
			})
		}
		return nil
	})
	server.On("join", func(socket *sio.Socket, data []byte) {
		var d map[string]any
		err := json.Unmarshal(data, &d)
		if err == nil {
			room := d["channel"].(string)
			socket.Join(room)
			for id, con := range server.SocketList() {
				con.Emit("addPeer", map[string]any{
					"peer_id":             socket.ID(),
					"should_create_offer": false,
				})
				socket.Emit("addPeer", map[string]any{
					"peer_id":             id,
					"should_create_offer": true,
				})
			}
		}
	})
	server.On("part", func(socket *sio.Socket, data []byte) {
		room := string(data)
		for id, con := range server.SocketList() {
			con.Emit("removePeer", map[string]any{
				"peer_id": socket.ID(),
			})
			socket.Emit("removePeer", map[string]any{
				"peer_id": id,
			})
		}
		socket.Leave(room)
	})

	server.On("relayICECandidate", func(socket *sio.Socket, data []byte) {
		var config map[string]any
		err := json.Unmarshal(data, &config)
		if err == nil {
			if iceCandidate, exists := config["ice_candidate"]; exists {
				peerID := config["peer_id"].(string)
				for id, con := range server.SocketList() {
					if peerID == id {
						con.Emit("iceCandidate", map[string]any{
							"peer_id":       socket.ID(),
							"ice_candidate": iceCandidate,
						})
					}
				}
			}
		}
	})

	server.On("relaySessionDescription", func(socket *sio.Socket, data []byte) {
		var config map[string]any
		err := json.Unmarshal(data, &config)
		if err == nil {
			if sessionDescription, exists := config["session_description"]; exists {
				peerID := config["peer_id"].(string)
				for id, con := range server.SocketList() {
					if peerID == id {
						con.Emit("sessionDescription", map[string]any{
							"peer_id":             socket.ID(),
							"session_description": sessionDescription,
						})
					}
				}
			}
		}
	})
}
