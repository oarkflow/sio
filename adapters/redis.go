package adapters

import (
	"context"
	"encoding/hex"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/oarkflow/ss"
)

var rng *ss.RNG

func init() {
	rng = ss.NewRNG()
}

const (
	// DefServerGroup the default redis.PubSub channel that will be subscribed to
	DefServerGroup = "ss-rmhb-group-default"
)

// RedisAdapter implements the ss.Adapter interface and uses
// Redis to syncronize between multiple machines running ss.Server
type RedisAdapter struct {
	r           *redis.Client
	o           *Options
	rps         *redis.PubSub
	bps         *redis.PubSub
	ctx         context.Context
	roomPSName  string
	bcastPSName string
}

type Options struct {

	//ServerName is a unique name for the ss.Adapter instance.
	//This name must be unique per backend instance or the backend
	//will not broadcast and roomcast properly.
	//
	//Leave this name blank to auto generate a unique name.
	ServerName string

	//ServerGroup is the server pool name that this instance's broadcasts
	//and roomcasts will be published to. This can be used to break up
	//ss.Adapter instances into separate domains.
	//
	//Leave this empty to use the default group "ss-rmhb-group-default"
	ServerGroup string
}

// NewRedisAdapter creates a new *RedisAdapter specified by redis Options and ssredis Options
func NewRedisAdapter(ctx context.Context, rOpts *redis.Options, ssrOpts *Options) (*RedisAdapter, error) {
	rClient := redis.NewClient(rOpts)
	_, err := rClient.Ping(context.Background()).Result()
	if err != nil {
		return nil, err
	}

	if ssrOpts == nil {
		ssrOpts = &Options{}
	}

	if ssrOpts.ServerGroup == "" {
		ssrOpts.ServerGroup = DefServerGroup
	}

	if ssrOpts.ServerName == "" {
		uid := make([]byte, 16)
		_, err := rng.Read(uid)
		if err != nil {
			return nil, err
		}
		ssrOpts.ServerName = hex.EncodeToString(uid)
	}

	roomPSName := ssrOpts.ServerGroup + ":_ss_roomcasts"
	bcastPSName := ssrOpts.ServerGroup + ":_ss_broadcasts"

	rmhb := &RedisAdapter{
		r:           rClient,
		rps:         rClient.Subscribe(ctx, roomPSName),
		bps:         rClient.Subscribe(ctx, bcastPSName),
		roomPSName:  roomPSName,
		bcastPSName: bcastPSName,
		o:           ssrOpts,
		ctx:         ctx,
	}

	return rmhb, nil
}

// Init is just here to satisfy the ss.Adapter interface.
func (r *RedisAdapter) Init() {

}

// Shutdown closes the subscribed redis channel, then the redis connection.
func (r *RedisAdapter) Shutdown() error {
	err := r.rps.Close()
	if err != nil {
		return err
	}
	err = r.bps.Close()
	if err != nil {
		return err
	}
	return r.r.Close()
}

// BroadcastToBackend will publish a broadcast message to the redis backend
func (r *RedisAdapter) BroadcastToBackend(b *ss.BroadcastMsg) {
	t := &transmission{
		ServerName: r.o.ServerName,
		EventName:  b.EventName,
		Data:       b.Data,
	}

	data, err := t.toJSON()
	if err != nil {
		slog.Error(err.Error())
		return
	}

	err = r.r.Publish(context.Background(), r.bcastPSName, string(data)).Err()
	if err != nil {
		slog.Error(err.Error())
	}
}

// RoomcastToBackend will publish a roomcast message to the redis backend
func (r *RedisAdapter) RoomcastToBackend(rm *ss.RoomMsg) {
	t := &transmission{
		ServerName: r.o.ServerName,
		EventName:  rm.EventName,
		RoomName:   rm.RoomName,
		Data:       rm.Data,
	}

	data, err := t.toJSON()
	if err != nil {
		slog.Error(err.Error())
		return
	}

	err = r.r.Publish(context.Background(), r.roomPSName, string(data)).Err()
	if err != nil {
		slog.Error(err.Error())
	}
}

// BroadcastFromBackend will receive broadcast messages from redis and propogate them to the neccessary sockets
func (r *RedisAdapter) BroadcastFromBackend(bc chan<- *ss.BroadcastMsg) {
	bChan := r.bps.Channel()

	for d := range bChan {
		var t transmission

		err := t.fromJSON([]byte(d.Payload))
		if err != nil {
			slog.Error(err.Error())
			continue
		}

		if t.ServerName == r.o.ServerName {
			continue
		}

		bc <- &ss.BroadcastMsg{
			EventName: t.EventName,
			Data:      t.Data,
		}
	}
}

// RoomcastFromBackend will receive roomcast messages from redis and propogate them to the neccessary sockets
func (r *RedisAdapter) RoomcastFromBackend(rc chan<- *ss.RoomMsg) {
	rChan := r.rps.Channel()

	for d := range rChan {
		var t transmission

		err := t.fromJSON([]byte(d.Payload))
		if err != nil {
			slog.Error(err.Error())
			continue
		}

		if t.ServerName == r.o.ServerName {
			continue
		}

		rc <- &ss.RoomMsg{
			EventName: t.EventName,
			RoomName:  t.RoomName,
			Data:      t.Data,
		}
	}
}
