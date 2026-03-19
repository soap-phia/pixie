package transport

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

var ePacketTooLarge = errors.New("transport: packet too large")

type GobwasConn interface {
	io.ReadWriteCloser
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

type Gobwas struct {
	Conn       GobwasConn
	ReadMutex  sync.Mutex
	WriteMutex sync.Mutex
}

func NewGobwas(conn GobwasConn) Transport {
	return &Gobwas{Conn: conn}
}

func (t *Gobwas) ReadMessage(ctx context.Context) ([]byte, error) {
	t.ReadMutex.Lock()
	defer t.ReadMutex.Unlock()

	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = t.Conn.SetReadDeadline(deadline)
		}
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(t.Conn, header); err != nil {
		return nil, err
	}

	length := int(header[0])<<24 | int(header[1])<<16 | int(header[2])<<8 | int(header[3])
	if length > 16*1024*1024 {
		return nil, ePacketTooLarge
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(t.Conn, data); err != nil {
		return nil, err
	}

	return data, nil
}

func (t *Gobwas) WriteMessage(ctx context.Context, data []byte) error {
	t.WriteMutex.Lock()
	defer t.WriteMutex.Unlock()

	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = t.Conn.SetWriteDeadline(deadline)
		}
	}

	length := len(data)
	header := []byte{
		byte(length >> 24),
		byte(length >> 16),
		byte(length >> 8),
		byte(length),
	}

	if _, err := t.Conn.Write(header); err != nil {
		return err
	}
	_, err := t.Conn.Write(data)
	return err
}

func (t *Gobwas) Close() error {
	return t.Conn.Close()
}
