package sio

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/oarkflow/sio/internal/maps"
)

var (
	socketRNG = NewRNG()
)

// Socket represents a websocket connection
type Socket struct {
	l       *sync.RWMutex
	id      string
	ws      *Conn
	closed  bool
	serv    *Server
	roomsl  *sync.RWMutex
	request *http.Request
	context *maps.Map[string, any]
	rooms   map[string]bool
}

const (
	idLen int = 24

	typeJSON string = "J"
	typeBin         = "B"
	typeStr         = "S"
)

func newSocket(serv *Server, ws *Conn, r *http.Request) *Socket {
	s := &Socket{
		l:       &sync.RWMutex{},
		id:      newSocketID(),
		ws:      ws,
		closed:  false,
		serv:    serv,
		roomsl:  &sync.RWMutex{},
		rooms:   make(map[string]bool),
		context: maps.New[string, any](100000),
		request: r,
	}
	serv.hub.addSocket(s)
	return s
}

func newSocketID() string {
	idBuf := make([]byte, idLen)
	socketRNG.Read(idBuf)
	return base64.StdEncoding.EncodeToString(idBuf)
}

func (s *Socket) receive() ([]byte, error) {
	_, data, err := s.ws.ReadMessage()
	return data, err
}

func (s *Socket) send(msgType int, data []byte) error {
	s.l.Lock()
	defer s.l.Unlock()
	return s.ws.WriteMessage(msgType, data)
}

// InRoom returns true if s is currently a member of roomName
func (s *Socket) InRoom(roomName string) bool {
	s.roomsl.RLock()
	defer s.roomsl.RUnlock()
	inRoom := s.rooms[roomName]
	return inRoom
}

// Request get request
func (s *Socket) Request() *http.Request {
	return s.request
}

// Set get request
func (s *Socket) Set(key string, val any) {
	s.context.Put(key, val)
}

// Get gets value
func (s *Socket) Get(key string) (any, bool) {
	return s.context.Get(key)
}

// Context gets value
func (s *Socket) Context() *maps.Map[string, any] {
	return s.context
}

// GetRooms returns a list of rooms that s is a member of
func (s *Socket) GetRooms() []string {
	s.roomsl.RLock()
	defer s.roomsl.RUnlock()

	var roomList []string
	for room := range s.rooms {
		roomList = append(roomList, room)
	}
	return roomList
}

// Join adds s to the specified room. If the room does
// not exist, it will be created
func (s *Socket) Join(roomName string) {
	s.roomsl.Lock()
	defer s.roomsl.Unlock()
	s.serv.hub.joinRoom(&joinRequest{roomName, s})
	s.rooms[roomName] = true
}

// Leave removes s from the specified room. If s
// is not a member of the room, nothing will happen. If the room is
// empty upon removal of s, the room will be closed
func (s *Socket) Leave(roomName string) {
	s.roomsl.Lock()
	defer s.roomsl.Unlock()
	s.serv.hub.leaveRoom(&leaveRequest{roomName, s})
	delete(s.rooms, roomName)
}

// ToRoom dispatches an event to all Sockets in the specified room.
func (s *Socket) ToRoom(roomName, eventName string, data any) {
	s.serv.hub.toRoom(&RoomMsg{RoomName: roomName, EventName: eventName, Data: data})
}

// ToRoomExcept dispatches an event to all Sockets in the specified room.
func (s *Socket) ToRoomExcept(roomName string, except []string, eventName string, data any) {
	s.serv.hub.toRoom(&RoomMsg{RoomName: roomName, EventName: eventName, Data: data, Except: except})
}

// Broadcast dispatches an event to all Sockets on the Server.
func (s *Socket) Broadcast(eventName string, data any) {
	s.serv.hub.broadcast(&BroadcastMsg{EventName: eventName, Data: data})
}

// BroadcastExcept dispatches an event to all Sockets on the Server.
func (s *Socket) BroadcastExcept(eventName string, except []string, data any) {
	s.serv.hub.broadcast(&BroadcastMsg{EventName: eventName, Data: data, Except: except})
}

// ToSocket dispatches an event to the specified socket ID.
func (s *Socket) ToSocket(socketID, eventName string, data any) {
	s.serv.ToRoom("__socket_id:"+socketID, eventName, data)
}

// Emit dispatches an event to s.
func (s *Socket) Emit(eventName string, data any) error {
	return s.send(emitData(eventName, data))
}

// ID returns the unique ID of s
func (s *Socket) ID() string {
	return s.id
}

// emitData combines the eventName and data into a payload that is understood
// by the sac-sock protocol.
func emitData(eventName string, data any) (int, []byte) {
	buf := bytes.NewBuffer(nil)
	buf.WriteString(eventName)
	buf.WriteByte(startOfHeaderByte)

	switch d := data.(type) {
	case string:
		buf.WriteString(typeStr)
		buf.WriteByte(startOfDataByte)
		buf.WriteString(d)
		return TextMessage, buf.Bytes()

	case []byte:
		buf.WriteString(typeBin)
		buf.WriteByte(startOfDataByte)
		buf.Write(d)
		return BinaryMessage, buf.Bytes()

	default:
		buf.WriteString(typeJSON)
		buf.WriteByte(startOfDataByte)
		jsonData, err := json.Marshal(d)
		if err != nil {
			slog.Error(err.Error())
		} else {
			buf.Write(jsonData)
		}
		return TextMessage, buf.Bytes()
	}
}

// Close closes the Socket connection and removes the Socket
// from any rooms that it was a member of
func (s *Socket) Close() error {
	s.l.Lock()
	isAlreadyClosed := s.closed
	s.closed = true
	s.l.Unlock()

	if isAlreadyClosed { //can't reclose the socket
		return nil
	}

	defer slog.Debug(s.ID(), "disconnected")

	err := s.ws.Close()
	if err != nil {
		return err
	}

	rooms := s.GetRooms()

	for _, room := range rooms {
		s.Leave(room)
	}

	s.serv.l.RLock()
	event := s.serv.onDisconnectFunc
	s.serv.l.RUnlock()

	if event != nil {
		if err := event(s); err != nil {
			return err
		}
	}

	s.serv.hub.removeSocket(s)
	return nil
}
