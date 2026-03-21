package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

var ePacketTooLarge = errors.New("transport: packet too large")

type GobwasConn interface {
	io.ReadWriteCloser
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

type GobwasNetConn interface {
	GobwasConn
	NetConn() net.Conn
}

type Gobwas struct {
	Conn       GobwasConn
	ReadMutex  sync.Mutex
	WriteMutex sync.Mutex
	HeaderBuf  [4]byte
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

	if _, err := io.ReadFull(t.Conn, t.HeaderBuf[:]); err != nil {
		return nil, err
	}

	length := int(t.HeaderBuf[0])<<24 | int(t.HeaderBuf[1])<<16 | int(t.HeaderBuf[2])<<8 | int(t.HeaderBuf[3])
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
	header := [4]byte{
		byte(length >> 24),
		byte(length >> 16),
		byte(length >> 8),
		byte(length),
	}

	if nc, ok := t.Conn.(GobwasNetConn); ok {
		bufs := net.Buffers{header[:], data}
		_, err := bufs.WriteTo(nc.NetConn())
		return err
	}

	if _, err := t.Conn.Write(header[:]); err != nil {
		return err
	}
	_, err := t.Conn.Write(data)
	return err
}

func (t *Gobwas) Close() error {
	return t.Conn.Close()
}

func (t *Gobwas) NetConn() net.Conn {
	if nc, ok := t.Conn.(GobwasNetConn); ok {
		return nc.NetConn()
	}
	return nil
}
