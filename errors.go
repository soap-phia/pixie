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
	eInvalidPacket    = errors.New("pixie: invalid packet type")
	ePacketTooSmall   = errors.New("pixie: packet too small")
	eVersion          = errors.New("pixie: incompatible protocol version")
	eMaxStreams       = errors.New("pixie: maximum stream count reached")
	ePixieClosed      = errors.New("pixie: multiplexor closed")
	eInvalidExtension = errors.New("pixie: invalid extension")
	eClosedSocket     = errors.New("pixie: websocket closed")
	eFailedHandshake  = errors.New("pixie: Handshake failed")
	eAuthFail         = errors.New("pixie: authentication failed")
)

func (e *StreamError) Error() string {
	return fmt.Sprintf("stream %d closed: %s", e.StreamId, e.CloseReason)
}

func (e *StreamError) Unwrap() error {
	return e.CloseReason
}
