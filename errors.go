package pixie

import (
	"errors"
	"fmt"
)

type StreamError struct {
	StreamId    uint32
	CloseReason CloseReason
}

var (
	eInvalidPacket  = errors.New("pixie: invalid packet type")
	ePacketTooSmall = errors.New("pixie: packet too small")
	eClosedSocket   = errors.New("pixie: websocket closed")
)

func (e *StreamError) Error() string {
	return fmt.Sprintf("stream %d closed: %s", e.StreamId, e.CloseReason)
}

func (e *StreamError) Unwrap() error {
	return e.CloseReason
}
