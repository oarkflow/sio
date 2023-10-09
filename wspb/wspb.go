// package siopb provides helpers for reading and writing protobuf messages.
package siopb // import "github.com/oarkflow/sio/wspb"

import (
	"bytes"
	"context"
	"fmt"

	"github.com/golang/protobuf/proto"

	"github.com/oarkflow/sio"
	"github.com/oarkflow/sio/internal/bpool"
	"github.com/oarkflow/sio/internal/errd"
)

// Read reads a protobuf message from c into v.
// It will reuse buffers in between calls to avoid allocations.
func Read(ctx context.Context, c *sio.Conn, v proto.Message) error {
	return read(ctx, c, v)
}

func read(ctx context.Context, c *sio.Conn, v proto.Message) (err error) {
	defer errd.Wrap(&err, "failed to read protobuf message")

	typ, r, err := c.Reader(ctx)
	if err != nil {
		return err
	}

	if typ != sio.MessageBinary {
		c.Close(sio.StatusUnsupportedData, "expected binary message")
		return fmt.Errorf("expected binary message for protobuf but got: %v", typ)
	}

	b := bpool.Get()
	defer bpool.Put(b)

	_, err = b.ReadFrom(r)
	if err != nil {
		return err
	}

	err = proto.Unmarshal(b.Bytes(), v)
	if err != nil {
		c.Close(sio.StatusInvalidFramePayloadData, "failed to unmarshal protobuf")
		return fmt.Errorf("failed to unmarshal protobuf: %w", err)
	}

	return nil
}

// Write writes the protobuf message v to c.
// It will reuse buffers in between calls to avoid allocations.
func Write(ctx context.Context, c *sio.Conn, v proto.Message) error {
	return write(ctx, c, v)
}

func write(ctx context.Context, c *sio.Conn, v proto.Message) (err error) {
	defer errd.Wrap(&err, "failed to write protobuf message")

	b := bpool.Get()
	pb := proto.NewBuffer(b.Bytes())
	defer func() {
		bpool.Put(bytes.NewBuffer(pb.Bytes()))
	}()

	err = pb.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	return c.Write(ctx, sio.MessageBinary, pb.Bytes())
}
