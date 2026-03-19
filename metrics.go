package pixie

import "sync/atomic"

type Metrics struct {
	StreamsOpened   atomic.Uint64
	StreamsClosed   atomic.Uint64
	BytesSent       atomic.Uint64
	BytesReceived   atomic.Uint64
	PacketsSent     atomic.Uint64
	PacketsReceived atomic.Uint64
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		StreamsOpened:   m.StreamsOpened.Load(),
		StreamsClosed:   m.StreamsClosed.Load(),
		BytesSent:       m.BytesSent.Load(),
		BytesReceived:   m.BytesReceived.Load(),
		PacketsSent:     m.PacketsSent.Load(),
		PacketsReceived: m.PacketsReceived.Load(),
	}
}

type MetricsSnapshot struct {
	StreamsOpened   uint64
	StreamsClosed   uint64
	BytesSent       uint64
	BytesReceived   uint64
	PacketsSent     uint64
	PacketsReceived uint64
}
