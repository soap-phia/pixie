package pixie

import (
	"context"
	"sync"
	"sync/atomic"
	"unsafe"
)

type PixieCore struct {
	Transport *LockedTransport
	Role      Role
	Config    *Config

	Streams      map[uint32]*Stream
	StreamsMutex sync.RWMutex
	NextId       atomic.Uint32

	cachedStreamId uint32
	cachedStream   unsafe.Pointer

	Extensions     []Extension
	ExtensionMutex sync.RWMutex

	Closed       atomic.Bool
	CloseChannel chan struct{}
	CloseOnce    sync.Once

	Metrics *Metrics

	PacketChannel chan Packet
	ErrorChannel  chan error
}

func NewCore(transport Transport, role Role, config *Config) *PixieCore {
	if config == nil {
		config = DefaultConfig()
	}

	m := &PixieCore{
		Transport:     NewLockedTransport(transport),
		Role:          role,
		Config:        config,
		Streams:       make(map[uint32]*Stream),
		CloseChannel:  make(chan struct{}),
		PacketChannel: make(chan Packet, 256),
		ErrorChannel:  make(chan error, 1),
	}

	if config.EnableMetrics {
		m.Metrics = &Metrics{}
	}

	return m
}

func (m *PixieCore) SendPacket(ctx context.Context, pkt Packet) error {
	if m.Closed.Load() {
		return ePixieClosed
	}

	data := pkt.Encode()
	err := m.Transport.WriteMessage(ctx, data)

	if err == nil && m.Metrics != nil {
		m.Metrics.PacketsSent.Add(1)
		m.Metrics.BytesSent.Add(uint64(len(data)))
	}

	return err
}

func (m *PixieCore) GetStream(id uint32) *Stream {
	if m.cachedStreamId == id {
		if stream := (*Stream)(atomic.LoadPointer(&m.cachedStream)); stream != nil {
			return stream
		}
	}

	m.StreamsMutex.RLock()
	stream := m.Streams[id]
	m.StreamsMutex.RUnlock()

	if stream != nil {
		atomic.StorePointer(&m.cachedStream, unsafe.Pointer(stream))
		m.cachedStreamId = id
	}

	return stream
}

func (m *PixieCore) AddStream(s *Stream) {
	m.StreamsMutex.Lock()
	defer m.StreamsMutex.Unlock()
	m.Streams[s.Id] = s

	if m.Metrics != nil {
		m.Metrics.StreamsOpened.Add(1)
	}
}

func (m *PixieCore) RemoveStream(id uint32) {
	m.StreamsMutex.Lock()
	delete(m.Streams, id)
	m.StreamsMutex.Unlock()

	if m.cachedStreamId == id {
		atomic.StorePointer(&m.cachedStream, nil)
		m.cachedStreamId = 0
	}

	if m.Metrics != nil {
		m.Metrics.StreamsClosed.Add(1)
	}
}

func (m *PixieCore) CreateStreamID() uint32 {
	return m.NextId.Add(1)
}

func (m *PixieCore) Close() error {
	var err error
	m.CloseOnce.Do(func() {
		m.Closed.Store(true)
		close(m.CloseChannel)

		m.StreamsMutex.Lock()
		for _, s := range m.Streams {
			s.CloseFromRemote(CrUnknown, true)
		}
		m.Streams = make(map[uint32]*Stream)
		m.StreamsMutex.Unlock()

		err = m.Transport.Close()
	})
	return err
}

func (m *PixieCore) ReadLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.CloseChannel:
			return
		default:
		}

		data, err := m.Transport.ReadMessage(ctx)
		if err != nil {
			select {
			case m.ErrorChannel <- err:
			default:
			}
			return
		}

		if m.Metrics != nil {
			m.Metrics.PacketsReceived.Add(1)
			m.Metrics.BytesReceived.Add(uint64(len(data)))
		}

		pkt, err := Decode(data)
		if err != nil {
			continue
		}

		select {
		case m.PacketChannel <- pkt:
		case <-m.CloseChannel:
			return
		}
	}
}

func (m *PixieCore) GetFlowControlMode(streamType StreamType) FlowControlMode {
	if streamType == StrUDP {
		return fcDisabled
	}

	m.ExtensionMutex.RLock()
	defer m.ExtensionMutex.RUnlock()

	for _, ext := range m.Extensions {
		for _, st := range ext.CongestionStreamTypes() {
			if st == streamType {
				if m.Role == RoleClient {
					return fcTrackAmt
				}
				return fcSendMessages
			}
		}
	}

	if m.Role == RoleClient {
		return fcTrackAmt
	}
	return fcSendMessages
}

func (m *PixieCore) GetMetrics() *Metrics {
	return m.Metrics
}

func (m *PixieCore) GetExtensions() []Extension {
	m.ExtensionMutex.RLock()
	defer m.ExtensionMutex.RUnlock()
	exts := make([]Extension, len(m.Extensions))
	copy(exts, m.Extensions)
	return exts
}

func (m *PixieCore) HasExtension(id uint8) bool {
	m.ExtensionMutex.RLock()
	defer m.ExtensionMutex.RUnlock()
	for _, ext := range m.Extensions {
		if ext.ID() == id {
			return true
		}
	}
	return false
}

func (m *PixieCore) GetExtension(id uint8) Extension {
	m.ExtensionMutex.RLock()
	defer m.ExtensionMutex.RUnlock()
	for _, ext := range m.Extensions {
		if ext.ID() == id {
			return ext
		}
	}
	return nil
}
