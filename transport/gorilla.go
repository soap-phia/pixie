package transport

import (
	"context"
	"sync"
	"time"
)

type GorillaWebSocket interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

type Gorilla struct {
	Conn       GorillaWebSocket
	ReadMutex  sync.Mutex
	WriteMutex sync.Mutex
}

func NewGorilla(conn GorillaWebSocket) Transport {
	return &Gorilla{Conn: conn}
}

func (t *Gorilla) ReadMessage(ctx context.Context) ([]byte, error) {
	t.ReadMutex.Lock()
	defer t.ReadMutex.Unlock()

	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = t.Conn.SetReadDeadline(deadline)
		}
	}

	_, data, err := t.Conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (t *Gorilla) WriteMessage(ctx context.Context, data []byte) error {
	t.WriteMutex.Lock()
	defer t.WriteMutex.Unlock()

	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = t.Conn.SetWriteDeadline(deadline)
		}
	}

	return t.Conn.WriteMessage(2, data)
}

func (t *Gorilla) Close() error {
	return t.Conn.Close()
}
