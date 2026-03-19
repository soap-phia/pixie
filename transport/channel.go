package transport

import (
	"context"
	"errors"
	"sync"
)

var eTransportClosed = errors.New("transport: closed")

type Channel struct {
	Send    chan []byte
	Receive chan []byte
	Closed  chan struct{}
	Once    sync.Once
}

func NewChannelPair() (Transport, Transport) {
	ch1 := make(chan []byte, 256)
	ch2 := make(chan []byte, 256)

	t1 := &Channel{
		Send:    ch1,
		Receive: ch2,
		Closed:  make(chan struct{}),
	}
	t2 := &Channel{
		Send:    ch2,
		Receive: ch1,
		Closed:  make(chan struct{}),
	}

	return t1, t2
}

func (t *Channel) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case data := <-t.Receive:
		return data, nil
	case <-t.Closed:
		return nil, eTransportClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *Channel) WriteMessage(ctx context.Context, data []byte) error {
	copied := make([]byte, len(data))
	copy(copied, data)

	select {
	case t.Send <- copied:
		return nil
	case <-t.Closed:
		return eTransportClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Channel) Close() error {
	t.Once.Do(func() {
		close(t.Closed)
	})
	return nil
}
