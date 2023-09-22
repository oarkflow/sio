package main

import (
	"github.com/oarkflow/sio/internal/maps"
)

var mediaOffers *maps.Map[string, map[string]any]

func init() {
	mediaOffers = maps.New[string, map[string]any](1000)
}

func GetOfferByID(id string) (map[string]any, bool) {
	return mediaOffers.Get(id)
}

func AddOffer(id string, offer map[string]any) {
	mediaOffers.Put(id, offer)
}

func RemoveOffer(id string) {
	mediaOffers.Delete(id)
}

func GetOffers() *maps.Map[string, map[string]any] {
	return mediaOffers
}
