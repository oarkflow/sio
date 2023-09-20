/*
A complex web app example that implements ssredis for synchronizing multiple Sacrificial Socket instances
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/oarkflow/sio"
	"github.com/oarkflow/sio/chi"
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
	s := sio.New(sio.Config{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		EnableCompression: true,
	})
	var err error
	s.On("request:login", func(socket *sio.Socket, data []byte) {
		d := string(data)
		fmt.Println("Login", d)
	})
	s.On("join", func(s *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if v, exists := room["room_id"]; exists {
				s.Join(v.(string))
				d := string(data)
				fmt.Println("joining", d)
				s.ToRoomExcept(v.(string), []string{s.ID()}, "action:peer-room-joined", d)
				s.Emit("action:room-joined", d)
			}
		}
	})
	s.On("request:send-message", func(socket *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if v, exists := room["room_id"]; exists {
				s.ToRoomExcept(v.(string), []string{socket.ID()}, "action:message-received", room)
			}
		}
	})
	s.On("request:typing-start", func(socket *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if v, exists := room["room_id"]; exists {
				s.ToRoomExcept(v.(string), []string{socket.ID()}, "action:peer-typing-start", room)
			}
		}
	})
	s.On("request:typing-stopped", func(socket *sio.Socket, data []byte) {
		var room map[string]any
		err := json.Unmarshal(data, &room)
		if err == nil {
			if v, exists := room["room_id"]; exists {
				s.ToRoomExcept(v.(string), []string{socket.ID()}, "action:peer-typing-stop", room)
			}
		}
	})
	s.On("leave", func(s *sio.Socket, data []byte) {
		d := string(data)
		s.Leave(d)
		_ = s.Emit("echo", "left room:"+d)
	})
	s.OnConnect(func(socket *sio.Socket) error {
		fmt.Println("Connected", socket.ID())
		return nil
	})
	s.OnDisconnect(func(socket *sio.Socket) error {
		fmt.Println("Disconnected")
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

	s.SetMultihomeBackend(b)*/

	c := make(chan bool)
	s.EnableSignalShutdown(c)

	go func() {
		<-c
		os.Exit(0)
	}()

	srv.Handle("/socket", s)
	srv.Mount("/", http.FileServer(http.Dir("webroot")))

	if *cert == "" || *key == "" {
		slog.Info(fmt.Sprintf("Listening on http://localhost%s", *webPort))
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
			fmt.Println("joining", d)
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
