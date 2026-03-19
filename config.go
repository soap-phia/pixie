package pixie

import (
	"context"
	"sync"

	"github.com/soap-phia/pixie/transport"
)

type Config struct {
	BufferSize    uint32
	MaxStreams    uint32
	Extensions    []Extension
	OnStreamOpen  func(ctx context.Context, stream *Stream, connect *ConnectPacket) error
	EnableMetrics bool
}

type LockedTransport struct {
	Transport  Transport
	WriteMutex sync.Mutex
}

type Transport = transport.Transport

func DefaultConfig() *Config {
	return &Config{
		BufferSize: 128,
		MaxStreams: 0,
	}
}

func NewLockedTransport(t Transport) *LockedTransport {
	return &LockedTransport{Transport: t}
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
