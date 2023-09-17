package sio

type hub struct {
	sockets          map[string]*Socket
	rooms            map[string]*room
	shutdownCh       chan bool
	socketList       chan []*Socket
	addCh            chan *Socket
	delCh            chan *Socket
	joinRoomCh       chan *joinRequest
	leaveRoomCh      chan *leaveRequest
	roomMsgCh        chan *RoomMsg
	broomcastCh      chan *RoomMsg //for passing data from the backend
	broadcastCh      chan *BroadcastMsg
	bbroadcastCh     chan *BroadcastMsg
	multihomeEnabled bool
	multihomeBackend Adapter
}

type room struct {
	name    string
	sockets map[string]*Socket
}

type joinRequest struct {
	roomName string
	socket   *Socket
}

type leaveRequest struct {
	roomName string
	socket   *Socket
}

// RoomMsg represents an event to be dispatched to a room of sockets
type RoomMsg struct {
	RoomName  string
	EventName string
	Data      any
}

// BroadcastMsg represents an event to be dispatched to all Sockets on the Server
type BroadcastMsg struct {
	EventName string
	Data      any
}

func (h *hub) addSocket(s *Socket) {
	h.addCh <- s
}

func (h *hub) removeSocket(s *Socket) {
	h.delCh <- s
}

func (h *hub) joinRoom(j *joinRequest) {
	h.joinRoomCh <- j
}

func (h *hub) leaveRoom(l *leaveRequest) {
	h.leaveRoomCh <- l
}

func (h *hub) toRoom(msg *RoomMsg) {
	h.roomMsgCh <- msg
}

func (h *hub) broadcast(b *BroadcastMsg) {
	h.broadcastCh <- b
}

func (h *hub) setMultihomeBackend(b Adapter) {
	if h.multihomeEnabled {
		return //can't have two backends... yet
	}
	
	h.multihomeBackend = b
	h.multihomeEnabled = true
	
	h.multihomeBackend.Init()
	
	go h.multihomeBackend.BroadcastFromBackend(h.bbroadcastCh)
	go h.multihomeBackend.RoomcastFromBackend(h.broomcastCh)
}

func (h *hub) listen() {
	for {
		select {
		case c := <-h.addCh:
			h.sockets[c.ID()] = c
		case c := <-h.delCh:
			delete(h.sockets, c.ID())
		case c := <-h.joinRoomCh:
			if _, exists := h.rooms[c.roomName]; !exists { //make the room if it doesn't exist
				h.rooms[c.roomName] = &room{c.roomName, make(map[string]*Socket)}
			}
			h.rooms[c.roomName].sockets[c.socket.ID()] = c.socket
		case c := <-h.leaveRoomCh:
			if room, exists := h.rooms[c.roomName]; exists {
				delete(room.sockets, c.socket.ID())
				if len(room.sockets) == 0 { //room is empty, delete it
					delete(h.rooms, c.roomName)
				}
			}
		case c := <-h.roomMsgCh:
			if room, exists := h.rooms[c.RoomName]; exists {
				for _, s := range room.sockets {
					s.Emit(c.EventName, c.Data)
				}
			}
			if h.multihomeEnabled { //the room may exist on the other end
				go h.multihomeBackend.RoomcastToBackend(c)
			}
		case c := <-h.broomcastCh:
			if room, exists := h.rooms[c.RoomName]; exists {
				for _, s := range room.sockets {
					s.Emit(c.EventName, c.Data)
				}
			}
		case c := <-h.broadcastCh:
			for _, s := range h.sockets {
				s.Emit(c.EventName, c.Data)
			}
			if h.multihomeEnabled {
				go h.multihomeBackend.BroadcastToBackend(c)
			}
		case c := <-h.bbroadcastCh:
			for _, s := range h.sockets {
				s.Emit(c.EventName, c.Data)
			}
		case _ = <-h.shutdownCh:
			var socketList []*Socket
			for _, s := range h.sockets {
				socketList = append(socketList, s)
			}
			h.socketList <- socketList
		}
	}
}

func newHub() *hub {
	h := &hub{
		shutdownCh:       make(chan bool),
		socketList:       make(chan []*Socket),
		sockets:          make(map[string]*Socket),
		rooms:            make(map[string]*room),
		addCh:            make(chan *Socket),
		delCh:            make(chan *Socket),
		joinRoomCh:       make(chan *joinRequest),
		leaveRoomCh:      make(chan *leaveRequest),
		roomMsgCh:        make(chan *RoomMsg),
		broomcastCh:      make(chan *RoomMsg),
		broadcastCh:      make(chan *BroadcastMsg),
		bbroadcastCh:     make(chan *BroadcastMsg),
		multihomeEnabled: false,
	}
	
	go h.listen()
	
	return h
}
