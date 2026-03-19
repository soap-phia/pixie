package transport

import (
	"context"
)

type NhooyrWebSocket interface {
	Read(ctx context.Context) (typ int, data []byte, err error)
	Write(ctx context.Context, typ int, data []byte) error
	Close(statusCode int, reason string) error
}

type Nhooyr struct {
	Conn NhooyrWebSocket
}

func NewNhooyr(conn NhooyrWebSocket) Transport {
	return &Nhooyr{Conn: conn}
}

func (t *Nhooyr) ReadMessage(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	_, data, err := t.Conn.Read(ctx)
	return data, err
}

func (t *Nhooyr) WriteMessage(ctx context.Context, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return t.Conn.Write(ctx, 2, data)
}

func (t *Nhooyr) Close() error {
	return t.Conn.Close(1000, "")
}
