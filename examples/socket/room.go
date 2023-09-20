package main

import (
	"github.com/oarkflow/sio/internal/maps"
)

var rooms *maps.Map[string, *maps.Map[string, map[string]any]]

func init() {
	rooms = maps.New[string, *maps.Map[string, map[string]any]](1000)
}

func GetRoomByID(id string) (*maps.Map[string, map[string]any], bool) {
	return rooms.Get(id)
}

func AddRoom(id string, room *maps.Map[string, map[string]any]) {
	rooms.Put(id, room)
}

func GetRooms() *maps.Map[string, *maps.Map[string, map[string]any]] {
	return rooms
}
