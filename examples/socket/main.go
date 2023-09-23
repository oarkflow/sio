/*
A complex web app example that implements ssredis for synchronizing multiple Sacrificial Socket instances
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/oarkflow/sio"
	"github.com/oarkflow/sio/chi"
	"github.com/oarkflow/sio/internal/maps"
)

type roomcast struct {
	Room string `json:"room"`
	Data string `json:"data"`
}

type message struct {
	Message string `json:"message"`
}

var (
	webPort = flag.String("webport", "0.0.0.0:8081", "host:port number used for webpage and socket connections")
	// redisPort = flag.String("redisport", ":6379", "host:port number used to connect to the redis server")
	key  = flag.String("key", "", "tls key used for https")
	cert = flag.String("cert", "", "tls cert used for https")
	// pass      = flag.String("p", "", "redis password, if there is one")
	// db        = flag.Int("db", 0, "redis db (default 0)")
)

func main() {
	flag.Parse()
	srv := chi.NewRouter()
	server := sio.New(sio.Config{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		EnableCompression: true,
	})
	var err error
	server.On("request:login", func(socket *sio.Socket, data []byte) {
		d := string(data)
		log.Println("Login", d)
	})
	server.On("request:room-join", func(s *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if userID, exists := room["user_id"]; exists {
				if _, e := s.Get(userID.(string)); !e {
					s.Set("user_id", userID)
				}
			}
			if v, exists := room["room_id"]; exists {
				roomID := v.(string)
				curRoom, exists := GetRoomByID(roomID)
				if !exists {
					curRoom = maps.New[string, map[string]any](10000)
					AddRoom(roomID, curRoom)
					curRoom.Put(s.ID(), room)
					fmt.Println("New", curRoom.Count())

					s.Join(roomID)
					var connections []map[string]any
					curRoom.Iter(func(key string, val map[string]any) bool {
						connections = append(connections, val)
						return false
					})
					d := string(data)
					s.ToRoomExcept(roomID, []string{s.ID()}, "action:peer-joined", d)
					s.Emit("action:room-joined", d)
					cons, err := json.Marshal(connections)
					if err == nil {
						s.Emit("action:peer-connections", string(cons))
					}

				} else {
					curRoom.Put(s.ID(), room)
					fmt.Println("Current", curRoom.Count())

					s.Join(roomID)
					var connections []map[string]any
					curRoom.Iter(func(key string, val map[string]any) bool {
						connections = append(connections, val)
						return false
					})
					fmt.Println("Len", len(connections))
					d := string(data)
					s.ToRoomExcept(roomID, []string{s.ID()}, "action:peer-joined", d)
					s.Emit("action:room-joined", d)
					cons, err := json.Marshal(connections)
					if err == nil {
						s.Emit("action:peer-connections", string(cons))
					}
				}
			}
		}
	})
	server.On("request:offer-media", func(socket *sio.Socket, data []byte) {
		var offer map[string]any
		err := json.Unmarshal(data, &offer)
		if err == nil {
			AddOffer(socket.ID(), offer)
		}
		userID, _ := socket.Get("user_id")
		offerData := map[string]any{
			"custom": map[string]any{
				"user_id":   userID,
				"socket_id": socket.ID(),
			},
			"data": offer,
		}
		jsonByte, err := json.Marshal(offerData)
		if err == nil {
			server.BroadcastExcept([]string{socket.ID()}, "action:peer-media-offer", string(jsonByte))
		}
	})
	server.On("request:accept-media", func(socket *sio.Socket, data []byte) {
		var offerAccepted map[string]any
		err := json.Unmarshal(data, &offerAccepted)
		if err == nil {
			if custom, exists := offerAccepted["custom"]; exists {
				bt, err := json.Marshal(offerAccepted["data"])
				if err == nil {
					switch custom := custom.(type) {
					case map[string]any:
						socket.ToSocket(custom["socket_id"].(string), "action:peer-media-accept", string(bt))
					case map[string]string:
						socket.ToSocket(custom["socket_id"], "action:peer-media-accept", string(bt))
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
	server.On("leave", func(s *sio.Socket, data []byte) {
		d := string(data)
		s.Leave(d)
		if room, exists := GetRoomByID(d); exists {
			var connections []map[string]any
			room.Iter(func(key string, val map[string]any) bool {
				connections = append(connections, val)
				return false
			})
			cons, err := json.Marshal(connections)
			if err == nil {
				s.ToRoom(d, "action:peer-connections", string(cons))
			} else {
				fmt.Println(err)
			}
		}
		_ = s.Emit("echo", "left room:"+d)
	})
	server.OnConnect(func(socket *sio.Socket) error {
		log.Println("Connected", socket.ID())
		return nil
	})
	server.OnDisconnect(func(socket *sio.Socket) error {
		log.Println("Disconnected")
		rooms := make(map[string][]map[string]any)
		GetRooms().Iter(func(roomID string, users *maps.Map[string, map[string]any]) bool {
			room, _ := rooms[roomID]
			if user, ok := users.Get(socket.ID()); ok {
				room = append(room, user)
				users.Delete(socket.ID())
			}
			socket.Leave(roomID)
			rooms[roomID] = room
			return true
		})
		for roomID, connections := range rooms {
			cons, err := json.Marshal(connections)
			if err == nil {
				server.ToRoom(roomID, "action:peer-connections-closed", string(cons))
			}
		}
		return nil
	})
	/*b, err := adapters.NewRedisAdapter(context.Background(), &redis.Options{
		Addr:     *redisPort,
		Password: *pass,
		DB:       *db,
	}, nil)

	if err != nil {
		log.Fatal(err)
	}

	server.SetMultihomeBackend(b)*/

	c := make(chan bool)
	server.EnableSignalShutdown(c)

	go func() {
		<-c
		os.Exit(0)
	}()

	srv.Handle("/socket", server)
	srv.Mount("/", http.FileServer(http.Dir("webroot")))

	if *cert == "" || *key == "" {
		slog.Info(fmt.Sprintf("Listening on http://localhost:%s", *webPort))
		err = http.ListenAndServe(*webPort, srv)
	} else {
		err = http.ListenAndServeTLS(*webPort, *cert, *key, srv)
	}

	if err != nil {
		slog.Error(err.Error())
	}
}

func Echo(s *sio.Socket, data []byte) {
	_ = s.Emit("echo", string(data))
}

func EchoBin(s *sio.Socket, data []byte) {
	_ = s.Emit("echobin", data)
}

func EchoJSON(s *sio.Socket, data []byte) {
	var m message
	err := json.Unmarshal(data, &m)
	check(err)

	_ = s.Emit("echojson", m)
}

func Join(s *sio.Socket, data []byte) {
	var room map[string]any
	err := json.Unmarshal(data, &room)
	if err == nil {
		if v, exists := room["room_id"]; exists {
			s.Join(v.(string))
			d := string(data)
			log.Println("joining", d)
			s.ToRoomExcept(v.(string), []string{s.ID()}, "action:peer-room-joined", d)
			s.Emit("action:room-joined", d)
		}
	}
}

func Leave(s *sio.Socket, data []byte) {
	d := string(data)
	s.Leave(d)
	_ = s.Emit("echo", "left room:"+d)
}

func Roomcast(s *sio.Socket, data []byte) {
	var r roomcast
	err := json.Unmarshal(data, &r)
	check(err)

	s.ToRoom(r.Room, "roomcast", r.Data)
}

func RoomcastBin(s *sio.Socket, data []byte) {
	var r roomcast
	err := json.Unmarshal(data, &r)
	check(err)

	s.ToRoom(r.Room, "roomcastbin", []byte(r.Data))
}

func RoomcastJSON(s *sio.Socket, data []byte) {
	var r roomcast
	err := json.Unmarshal(data, &r)
	check(err)

	s.ToRoom(r.Room, "roomcastjson", r)
}

func Broadcast(s *sio.Socket, data []byte) {
	s.Broadcast("broadcast", string(data))
}

func BroadcastBin(s *sio.Socket, data []byte) {
	s.Broadcast("broadcastbin", data)
}

func BroadcastJSON(s *sio.Socket, data []byte) {
	var m message
	err := json.Unmarshal(data, &m)
	check(err)

	s.Broadcast("broadcastjson", m)
}

func check(err error) {
	if err != nil {
		slog.Error(err.Error())
	}
}
