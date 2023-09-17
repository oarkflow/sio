package ss

type Adapter interface {
	//Init is called as soon as the Adapter is
	//registered using Server.SetMultihomeBackend
	Init()

	//Shutdown is called immediately after all sockets have
	//been closed
	Shutdown() error

	//BroadcastToBackend is called everytime a BroadcastMsg is
	//sent by a Socket
	//
	//BroadcastToBackend must be safe for concurrent use by multiple
	//go routines
	BroadcastToBackend(*BroadcastMsg)

	//RoomcastToBackend is called everytime a RoomMsg is sent
	//by a socket, even if none of this server's sockets are
	//members of that room
	//
	//RoomcastToBackend must be safe for concurrent use by multiple
	//go routines
	RoomcastToBackend(*RoomMsg)

	//BroadcastFromBackend is called once and only once as a go routine as
	//soon as the Adapter is registered using
	//Server.SetMultihomeBackend
	//
	//b consumes a BroadcastMsg and dispatches
	//it to all sockets on this server
	BroadcastFromBackend(b chan<- *BroadcastMsg)

	//RoomcastFromBackend is called once and only once as a go routine as
	//soon as the Adapter is registered using
	//Server.SetMultihomeBackend
	//
	//r consumes a RoomMsg and dispatches it to all sockets
	//that are members of the specified room
	RoomcastFromBackend(r chan<- *RoomMsg)
}
