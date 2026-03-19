package pixie

import (
	"context"
	"fmt"
	"sync"
)

type PixieServer struct {
	*PixieCore
	BufferSize    uint32
	Downgraded    bool
	StreamChannel chan *IncomingStream
	WaitGroup     sync.WaitGroup
}

type IncomingStream struct {
	Stream  *Stream
	Connect *ConnectPacket
}

type ServerOption func(*PixieServer)

func WithServerExtensions(extensions ...Extension) ServerOption {
	return func(s *PixieServer) {
		s.Extensions = extensions
	}
}

func WithServerBufferSize(size uint32) ServerOption {
	return func(s *PixieServer) {
		s.Config.BufferSize = size
	}
}

func WithServerMetrics() ServerOption {
	return func(s *PixieServer) {
		s.Config.EnableMetrics = true
		s.Metrics = &Metrics{}
	}
}

func WithStreamHandler(handler func(ctx context.Context, stream *Stream, connect *ConnectPacket) error) ServerOption {
	return func(s *PixieServer) {
		s.Config.OnStreamOpen = handler
	}
}

func NewPixieServer(ctx context.Context, transport Transport, opts ...ServerOption) (*PixieServer, error) {
	config := DefaultConfig()

	s := &PixieServer{
		PixieCore:     NewCore(transport, rServer, config),
		StreamChannel: make(chan *IncomingStream, 64),
	}

	for _, opt := range opts {
		opt(s)
	}

	s.BufferSize = s.Config.BufferSize

	if err := s.HandshakeV1(ctx); err != nil {
		s.Close()
		return nil, err
	}

	s.WaitGroup.Add(1)
	go func() {
		defer s.WaitGroup.Done()
		s.run(ctx)
	}()

	return s, nil
}

func (s *PixieServer) HandshakeV1(ctx context.Context) error {
	var extensionData []ExtensionData
	for _, ext := range s.Extensions {
		extensionData = append(extensionData, ExtensionData{
			Id:      ext.ID(),
			Payload: ext.Encode(rServer),
		})
	}

	info := NewInfo(ProtocolVersion, extensionData)
	if err := s.SendPacket(ctx, info); err != nil {
		return fmt.Errorf("%w: failed to send INFO: %v", eFailedHandshake, err)
	}

	data, err := s.Transport.ReadMessage(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", eFailedHandshake, err)
	}

	pkt, err := Decode(data)
	if err != nil {
		return fmt.Errorf("%w: %v", eFailedHandshake, err)
	}

	switch p := pkt.(type) {
	case *InfoPacket:
		return s.HandshakeV2(ctx, p)
	case *ConnectPacket:
		s.Downgraded = true
		s.Extensions = []Extension{&UDPExtension{}}

		if err := s.SendPacket(ctx, NewContinuePacket(0, s.BufferSize)); err != nil {
			return fmt.Errorf("%w: %v", eFailedHandshake, err)
		}

		s.HandleConnectPacket(p)
		return nil
	default:
		return fmt.Errorf("%w: unexpected packet type %T", eFailedHandshake, pkt)
	}
}

func (s *PixieServer) HandshakeV2(ctx context.Context, clientInfo *InfoPacket) error {
	if clientInfo.Version.Major != ProtocolVersion.Major {
		closeReason := crInvalidExtension
		_ = s.SendPacket(ctx, NewClosePacket(0, closeReason))
		return fmt.Errorf("%w: client version %s, server version %s",
			eVersion, clientInfo.Version, ProtocolVersion)
	}

	clientExtensions := make(map[uint8]ExtensionData)
	for _, ext := range clientInfo.Extensions {
		clientExtensions[ext.Id] = ext
	}

	var negotiatedExtensions []Extension
	for _, ext := range s.Extensions {
		if clientExt, ok := clientExtensions[ext.ID()]; ok {
			if err := ext.Decode(clientExt.Payload, rServer); err != nil {
				continue
			}
			negotiatedExtensions = append(negotiatedExtensions, ext)
		}
	}

	for _, ext := range negotiatedExtensions {
		if err := ext.HandleHandshake(ctx, s.Transport.Transport, rServer); err != nil {
			if ext.ID() == PasswordExtensionID {
				_ = s.SendPacket(ctx, NewClosePacket(0, crPassAuthFailed))
			} else {
				_ = s.SendPacket(ctx, NewClosePacket(0, crInvalidExtension))
			}
			return fmt.Errorf("%w: extension %d Handshake failed: %v", eFailedHandshake, ext.ID(), err)
		}
	}

	s.Extensions = negotiatedExtensions

	if err := s.SendPacket(ctx, NewContinuePacket(0, s.BufferSize)); err != nil {
		return fmt.Errorf("%w: %v", eFailedHandshake, err)
	}

	return nil
}

func (s *PixieServer) run(ctx context.Context) {
	go s.ReadLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			s.Close()
			return
		case <-s.CloseChannel:
			return
		case err := <-s.ErrorChannel:
			if err != nil {
				s.Close()
			}
			return
		case pkt := <-s.PacketChannel:
			s.PacketHandler(ctx, pkt)
		}
	}
}

func (s *PixieServer) PacketHandler(ctx context.Context, pkt Packet) {
	switch p := pkt.(type) {
	case *ConnectPacket:
		s.HandleConnectPacket(p)

	case *DataPacket:
		if stream := s.GetStream(p.StreamID()); stream != nil {
			stream.ReceiveData(p.Payload)
		}

	case *ContinuePacket:

	case *ClosePacket:
		if p.StreamID() == 0 {
			s.Close()
			return
		}
		if stream := s.GetStream(p.StreamID()); stream != nil {
			stream.CloseFromRemote(p.Reason, false)
		}
	}
}

func (s *PixieServer) HandleConnectPacket(pkt *ConnectPacket) {
	streamID := pkt.StreamID()

	if s.Closed.Load() {
		return
	}

	fcMode := s.GetFlowControlMode(pkt.StreamType)
	var fc *FlowControl
	if fcMode != fcDisabled {
		fc = NewFlowControl(fcMode, s.BufferSize)
	}

	stream := NewStream(streamID, pkt.StreamType, s.PixieCore, fc, int(s.BufferSize))
	stream.Host = pkt.Host
	stream.Port = pkt.Port
	s.AddStream(stream)

	if s.Config.OnStreamOpen != nil {
		go func() {
			ctx := context.Background()
			if err := s.Config.OnStreamOpen(ctx, stream, pkt); err != nil {
				stream.CloseWithReason(crInvalidInfo)
			}
		}()
	} else {
		select {
		case s.StreamChannel <- &IncomingStream{Stream: stream, Connect: pkt}:
		case <-s.CloseChannel:
			stream.CloseWithReason(crUnknown)
		default:
			stream.CloseWithReason(crThrottled)
		}
	}
}

func (s *PixieServer) AcceptStream(ctx context.Context) (*Stream, *ConnectPacket, error) {
	select {
	case incoming := <-s.StreamChannel:
		return incoming.Stream, incoming.Connect, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-s.CloseChannel:
		return nil, nil, ePixieClosed
	}
}

func (s *PixieServer) Close() error {
	return s.CloseWithReason(crVoluntary)
}

func (s *PixieServer) CloseWithReason(reason CloseReason) error {
	if s.Closed.Load() {
		return nil
	}

	_ = s.SendPacket(context.Background(), NewClosePacket(0, reason))

	err := s.PixieCore.Close()
	s.WaitGroup.Wait()
	return err
}

func (s *PixieServer) WasDowngraded() bool {
	return s.Downgraded
}

func (s *PixieServer) GetBufferSize() uint32 {
	return s.BufferSize
}

func (s *PixieServer) Done() <-chan struct{} {
	return s.CloseChannel
}

func (s *PixieServer) GetMetrics() *Metrics {
	return s.PixieCore.GetMetrics()
}

func (s *PixieServer) GetExtensions() []Extension {
	return s.PixieCore.GetExtensions()
}
