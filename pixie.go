package pixie

import (
	"context"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"unsafe"
)

type PixieCore struct {
	Transport      *LockedTransport
	cachedStream   unsafe.Pointer
	cachedStreamId uint32
	Closed         atomic.Bool
	CloseChannel   chan struct{}
	PacketChannel  chan Packet
	Streams        sync.Map

	UseFramedWrites bool
	NextId          atomic.Uint32

	ContinuePending    sync.Map
	ContinueBatchMutex sync.Mutex
	ContinueBatchList  [][2]uint32
	ContinueBatchCap   int

	ReceivedMetricsPacketCount uint64
	MetricsPacketsSent         uint64
	ReceivedMetricsByteCount   uint64
	MetricsBytesSent           uint64
	MetricsCounter             int

	Role      Role
	Config    *Config
	CloseOnce sync.Once
	Metrics   *Metrics

	Extensions     []Extension
	ExtensionMutex sync.RWMutex

	fcModeTCPCache   FlowControlMode
	fcModeCached     atomic.Bool
	fcModeCacheMutex sync.Mutex

	ErrorChannel chan error
}

func NewCore(transport Transport, role Role, config *Config) *PixieCore {
	if config == nil {
		config = DefaultConfig()
	}

	m := &PixieCore{
		Transport:     NewLockedTransport(transport),
		Role:          role,
		Config:        config,
		CloseChannel:  make(chan struct{}),
		PacketChannel: make(chan Packet, config.PacketChannelSize),
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

	if metrics := m.Metrics; metrics != nil && err == nil {
		m.MetricsPacketsSent++
		m.MetricsBytesSent += uint64(len(data))
		m.MetricsCounter++
		if m.MetricsCounter >= 1000 {
			m.FlushMetricsBatch()
		}
	}

	return err
}

func (m *PixieCore) SendPacketRaw(ctx context.Context, data []byte) error {
	if m.Closed.Load() {
		return ePixieClosed
	}

	err := m.Transport.WriteMessage(ctx, data)

	if metrics := m.Metrics; metrics != nil && err == nil {
		m.MetricsPacketsSent++
		m.MetricsBytesSent += uint64(len(data))
		m.MetricsCounter++
		if m.MetricsCounter >= 1000 {
			m.FlushMetricsBatch()
		}
	}

	return err
}

func (m *PixieCore) SendDataFramed(ctx context.Context, poolBuf []byte, framedData []byte, payloadLen int) error {
	err := m.Transport.WriteMessagePooled(ctx, poolBuf, framedData)

	if metrics := m.Metrics; metrics != nil && err == nil {
		m.MetricsPacketsSent++
		m.MetricsBytesSent += uint64(5 + payloadLen)
		m.MetricsCounter++
		if m.MetricsCounter >= 1000 {
			m.FlushMetricsBatch()
		}
	}

	return err
}

func (m *PixieCore) SendPendingContinue(streamId uint32, bufferSize uint32) {
	m.ContinuePending.Store(streamId, bufferSize)
}

func (m *PixieCore) FlushContinueBatch() {
	m.ContinueBatchMutex.Lock()
	m.ContinueBatchList = m.ContinueBatchList[:0]

	m.ContinuePending.Range(func(key, value interface{}) bool {
		streamId := key.(uint32)
		bufferSize := value.(uint32)
		m.ContinueBatchList = append(m.ContinueBatchList, [2]uint32{streamId, bufferSize})
		return true
	})

	if len(m.ContinueBatchList) > m.ContinueBatchCap {
		m.ContinueBatchCap = len(m.ContinueBatchList) + (len(m.ContinueBatchList) / 4)
	}

	batchLen := len(m.ContinueBatchList)
	m.ContinueBatchMutex.Unlock()

	for i := 0; i < batchLen; i++ {
		pair := m.ContinueBatchList[i]
		streamId, bufferSize := pair[0], pair[1]
		pktData := EncodeContinuePacket(streamId, bufferSize)
		_ = m.SendPacketRaw(context.Background(), pktData)
		ReturnContinuePacket(pktData)
		m.ContinuePending.Delete(streamId)
	}
}

func (m *PixieCore) FlushMetricsBatch() {
	if m.Metrics == nil || (m.ReceivedMetricsPacketCount == 0 && m.MetricsBytesSent == 0) {
		return
	}

	if m.ReceivedMetricsPacketCount > 0 {
		m.Metrics.PacketsReceived.Add(m.ReceivedMetricsPacketCount)
	}
	if m.ReceivedMetricsByteCount > 0 {
		m.Metrics.BytesReceived.Add(m.ReceivedMetricsByteCount)
	}
	if m.MetricsPacketsSent > 0 {
		m.Metrics.PacketsSent.Add(m.MetricsPacketsSent)
	}
	if m.MetricsBytesSent > 0 {
		m.Metrics.BytesSent.Add(m.MetricsBytesSent)
	}

	m.ReceivedMetricsPacketCount = 0
	m.ReceivedMetricsByteCount = 0
	m.MetricsPacketsSent = 0
	m.MetricsBytesSent = 0
	m.MetricsCounter = 0
}

func (m *PixieCore) GetStreamSlow(id uint32) *Stream {
	if val, ok := m.Streams.Load(id); ok {
		stream := val.(*Stream)
		atomic.StorePointer(&m.cachedStream, unsafe.Pointer(stream))
		m.cachedStreamId = id
		return stream
	}
	return nil
}

func (m *PixieCore) GetStream(id uint32) *Stream {
	if m.cachedStreamId == id {
		if stream := (*Stream)(atomic.LoadPointer(&m.cachedStream)); stream != nil {
			return stream
		}
	}
	return m.GetStreamSlow(id)
}

func (m *PixieCore) AddStream(s *Stream) {
	m.Streams.Store(s.Id, s)

	if m.Metrics != nil {
		m.Metrics.StreamsOpened.Add(1)
	}
}

func (m *PixieCore) RemoveStream(id uint32) {
	m.Streams.Delete(id)

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

		m.FlushMetricsBatch()

		m.Streams.Range(func(key, value interface{}) bool {
			s := value.(*Stream)
			s.CloseFromRemote(CrUnknown, true)
			m.Streams.Delete(key)
			return true
		})

		err = m.Transport.Close()
	})
	return err
}

func (m *PixieCore) ReadLoop(ctx context.Context) {
	closeCh := m.CloseChannel
	ctxDone := ctx.Done()

	metrics := m.Metrics

	for {
		if m.Closed.Load() {
			if metrics != nil {
				m.FlushMetricsBatch()
			}
			return
		}

		data, err := m.Transport.ReadMessage(ctx)
		if err != nil {
			if metrics != nil {
				m.FlushMetricsBatch()
			}
			select {
			case m.ErrorChannel <- err:
			default:
			}
			return
		}

		if metrics != nil {
			m.ReceivedMetricsPacketCount++
			m.ReceivedMetricsByteCount += uint64(len(data))
			m.MetricsCounter++
			if m.MetricsCounter >= 1000 {
				m.FlushMetricsBatch()
			}
		}

		if len(data) >= 5 && data[0] == 0x02 {
			streamId := binary.LittleEndian.Uint32(data[1:5])

			var stream *Stream
			if m.cachedStreamId == streamId {
				stream = (*Stream)(atomic.LoadPointer(&m.cachedStream))
			} else {
				if val, ok := m.Streams.Load(streamId); ok {
					stream = val.(*Stream)
					atomic.StorePointer(&m.cachedStream, unsafe.Pointer(stream))
					m.cachedStreamId = streamId
				}
			}

			if stream != nil {
				stream.ReceiveData(data[5:])
				continue
			}
		}

		if len(data) >= 9 && data[0] == 0x03 {
			streamId := binary.LittleEndian.Uint32(data[1:5])
			bufferRemaining := binary.LittleEndian.Uint32(data[5:9])

			var stream *Stream
			if m.cachedStreamId == streamId {
				stream = (*Stream)(atomic.LoadPointer(&m.cachedStream))
			} else {
				if val, ok := m.Streams.Load(streamId); ok {
					stream = val.(*Stream)
					atomic.StorePointer(&m.cachedStream, unsafe.Pointer(stream))
					m.cachedStreamId = streamId
				}
			}

			if stream != nil {
				if stream.FlowControl != nil {
					stream.FlowControl.Set(bufferRemaining)
				}
				continue
			}
		}

		if len(data) >= 5 && data[0] >= 0xF0 {
			m.HandleExtensionPacket(ctx, PacketType(data[0]), data)
			continue
		}

		pkt, err := Decode(data)
		if err != nil {
			continue
		}

		select {
		case m.PacketChannel <- pkt:
		case <-closeCh:
			return
		case <-ctxDone:
			return
		}
	}
}

func (m *PixieCore) GetFlowControlMode(streamType StreamType) FlowControlMode {
	if streamType == StrUDP {
		return fcDisabled
	}

	if m.fcModeCached.Load() {
		return m.fcModeTCPCache
	}

	m.fcModeCacheMutex.Lock()
	defer m.fcModeCacheMutex.Unlock()

	if m.fcModeCached.Load() {
		return m.fcModeTCPCache
	}

	var mode FlowControlMode
	m.ExtensionMutex.RLock()
	defer m.ExtensionMutex.RUnlock()

	for _, ext := range m.Extensions {
		for _, st := range ext.CongestionStreamTypes() {
			if st == StrTCP {
				if m.Role == RoleClient {
					mode = fcTrackAmt
				} else {
					mode = fcSendMessages
				}
				break
			}
		}
		if mode != 0 {
			break
		}
	}

	if mode == 0 {
		if m.Role == RoleClient {
			mode = fcTrackAmt
		} else {
			mode = fcSendMessages
		}
	}

	m.fcModeTCPCache = mode
	m.fcModeCached.Store(true)
	return mode
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

func (m *PixieCore) HandleExtensionPacket(ctx context.Context, packetType PacketType, data []byte) {
	m.ExtensionMutex.RLock()
	extensions := m.Extensions
	m.ExtensionMutex.RUnlock()

	for _, ext := range extensions {
		for _, pt := range ext.SupportedPacketTypes() {
			if pt == packetType {
				_ = ext.PacketHandler(ctx, packetType, data, m.Transport.Transport)
				return
			}
		}
	}
}
