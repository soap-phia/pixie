package pixie

import (
	"context"
	"sync"

	"github.com/soap-phia/pixie/transport"
)

type Config struct {
	BufferSize        uint32
	StreamChannelSize int
	PacketChannelSize int
	MaxStreams        uint32
	Extensions        []Extension
	OnStreamOpen      func(ctx context.Context, stream *Stream, connect *ConnectPacket) error
	EnableMetrics     bool
	ForceV1           bool
}

type LockedTransport struct {
	Transport  Transport
	WriteMutex sync.Mutex
	PoolWriter PooledWriter
}

type Transport = transport.Transport

func DefaultConfig() *Config {
	return &Config{
		BufferSize:        128,
		StreamChannelSize: 64,
		PacketChannelSize: 2048,
		MaxStreams:        0,
	}
}

func NewLockedTransport(t Transport) *LockedTransport {
	lt := &LockedTransport{Transport: t}
	if pw, ok := t.(PooledWriter); ok {
		lt.PoolWriter = pw
	}
	return lt
}

func (lt *LockedTransport) ReadMessage(ctx context.Context) ([]byte, error) {
	return lt.Transport.ReadMessage(ctx)
}

func (lt *LockedTransport) WriteMessage(ctx context.Context, data []byte) error {
	lt.WriteMutex.Lock()
	defer lt.WriteMutex.Unlock()
	return lt.Transport.WriteMessage(ctx, data)
}

func (lt *LockedTransport) Close() error {
	return lt.Transport.Close()
}

type PooledWriter interface {
	WriteMessagePooled(ctx context.Context, poolBuf []byte, framedData []byte) error
}

func (lt *LockedTransport) WriteMessagePooled(ctx context.Context, poolBuf []byte, framedData []byte) error {
	if lt.PoolWriter != nil {
		return lt.PoolWriter.WriteMessagePooled(ctx, poolBuf, framedData)
	}

	lt.WriteMutex.Lock()
	err := lt.Transport.WriteMessage(ctx, framedData)
	lt.WriteMutex.Unlock()
	ReturnPacket(poolBuf)
	return err
}

type GorillaWebSocket = transport.GorillaWebSocket

func NewGorillaTransport(conn GorillaWebSocket) Transport {
	return transport.NewGorilla(conn)
}

type NhooyrWebSocket = transport.NhooyrWebSocket

func NewNhooyrTransport(conn NhooyrWebSocket) Transport {
	return transport.NewNhooyr(conn)
}

type GobwasConn = transport.GobwasConn

func NewGobwasTransport(conn GobwasConn) Transport {
	return transport.NewGobwas(conn)
}

func NewChannelTransportPair() (Transport, Transport) {
	return transport.NewChannelPair()
}
