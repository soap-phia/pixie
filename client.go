package pixie

import (
	"context"
	"fmt"
	"sync"
)

type PixieClient struct {
	*PixieCore
	BufferSize uint32
	Downgraded bool
	WaitGroup  sync.WaitGroup
}

type ClientOption func(*PixieClient)

func WithExtensions(extensions ...Extension) ClientOption {
	return func(c *PixieClient) {
		c.Extensions = extensions
	}
}

func WithBufferSize(size uint32) ClientOption {
	return func(c *PixieClient) {
		c.Config.BufferSize = size
	}
}

func WithMetrics() ClientOption {
	return func(c *PixieClient) {
		c.Config.EnableMetrics = true
		c.Metrics = &Metrics{}
	}
}

func NewPixie(ctx context.Context, transport Transport, opts ...ClientOption) (*PixieClient, error) {
	config := DefaultConfig()

	c := &PixieClient{
		PixieCore: NewCore(transport, RoleClient, config),
	}

	for _, opt := range opts {
		opt(c)
	}

	if err := c.HandshakeV1(ctx); err != nil {
		c.Close()
		return nil, err
	}

	c.WaitGroup.Add(1)
	go func() {
		defer c.WaitGroup.Done()
		c.run(ctx)
	}()

	return c, nil
}

func (c *PixieClient) HandshakeV1(ctx context.Context) error {
	data, err := c.Transport.ReadMessage(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", eFailedHandshake, err)
	}

	pkt, err := Decode(data)
	if err != nil {
		return fmt.Errorf("%w: %v", eFailedHandshake, err)
	}

	switch p := pkt.(type) {
	case *InfoPacket:
		return c.HandshakeV2(ctx, p)
	case *ContinuePacket:
		c.Downgraded = true
		c.BufferSize = p.BufferRemaining
		c.Extensions = append(c.Extensions, &UDPExtension{})
		return nil
	default:
		return fmt.Errorf("%w: unexpected packet type %T", eFailedHandshake, pkt)
	}
}

func (c *PixieClient) HandshakeV2(ctx context.Context, serverInfo *InfoPacket) error {
	if serverInfo.Version.Major != ProtocolVersion.Major {
		return fmt.Errorf("%w: server version %s, client version %s",
			eVersion, serverInfo.Version, ProtocolVersion)
	}

	serverExtensions := make(map[uint8]ExtensionData)
	for _, ext := range serverInfo.Extensions {
		serverExtensions[ext.Id] = ext
	}

	var negotiatedExtensions []Extension
	var extensionData []ExtensionData

	for _, ext := range c.Extensions {
		if serverExt, ok := serverExtensions[ext.ID()]; ok {
			if err := ext.Decode(serverExt.Payload, RoleClient); err != nil {
				continue
			}
			negotiatedExtensions = append(negotiatedExtensions, ext)
			extensionData = append(extensionData, ExtensionData{
				Id:      ext.ID(),
				Payload: ext.Encode(RoleClient),
			})
		}
	}

	clientInfo := NewInfo(ProtocolVersion, extensionData)
	if err := c.SendPacket(ctx, clientInfo); err != nil {
		return fmt.Errorf("%w: failed to send INFO: %v", eFailedHandshake, err)
	}

	for _, ext := range negotiatedExtensions {
		if err := ext.HandleHandshake(ctx, c.Transport.Transport, RoleClient); err != nil {
			return fmt.Errorf("%w: extension %d Handshake failed: %v", eFailedHandshake, ext.ID(), err)
		}
	}

	data, err := c.Transport.ReadMessage(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", eFailedHandshake, err)
	}

	pkt, err := Decode(data)
	if err != nil {
		return fmt.Errorf("%w: %v", eFailedHandshake, err)
	}

	switch p := pkt.(type) {
	case *ContinuePacket:
		if p.StreamID() != 0 {
			return fmt.Errorf("%w: unexpected stream ID %d in CONTINUE", eFailedHandshake, p.StreamID())
		}
		c.BufferSize = p.BufferRemaining
		c.Extensions = negotiatedExtensions
		return nil
	case *ClosePacket:
		return fmt.Errorf("%w: server rejected connection: %s", eFailedHandshake, p.Reason)
	default:
		return fmt.Errorf("%w: unexpected packet type %T", eFailedHandshake, pkt)
	}
}

func (c *PixieClient) run(ctx context.Context) {
	go c.ReadLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			c.Close()
			return
		case <-c.CloseChannel:
			return
		case err := <-c.ErrorChannel:
			if err != nil {
				c.Close()
			}
			return
		case pkt := <-c.PacketChannel:
			c.HandlePacket(pkt)
		}
	}
}

func (c *PixieClient) HandlePacket(pkt Packet) {
	switch p := pkt.(type) {
	case *DataPacket:
		if stream := c.GetStream(p.StreamID()); stream != nil {
			stream.ReceiveData(p.Payload)
		}

	case *ContinuePacket:
		if stream := c.GetStream(p.StreamID()); stream != nil {
			if stream.FlowControl != nil {
				stream.FlowControl.Set(p.BufferRemaining)
				stream.FlowControl.Awaken()
			}
		}

	case *ClosePacket:
		if p.StreamID() == 0 {
			c.Close()
			return
		}
		if stream := c.GetStream(p.StreamID()); stream != nil {
			stream.CloseFromRemote(p.Reason, false)
		}
	}
}

func (c *PixieClient) OpenStream(ctx context.Context, streamType StreamType, host string, port uint16) (*Stream, error) {
	if c.Closed.Load() {
		return nil, ePixieClosed
	}

	if streamType == StrUDP && !c.HasExtension(UDPExtensionID) {
		return nil, fmt.Errorf("%w: UDP extension required", eInvalidExtension)
	}

	streamID := c.CreateStreamID()

	fcMode := c.GetFlowControlMode(streamType)
	var fc *FlowControl
	if fcMode != fcDisabled {
		fc = NewFlowControl(fcMode, c.BufferSize)
	}

	stream := NewStream(streamID, streamType, c.PixieCore, fc, int(c.BufferSize))
	stream.Host = host
	stream.Port = port
	c.AddStream(stream)

	connectPkt := NewConnectPacket(streamID, streamType, host, port)
	if err := c.SendPacket(ctx, connectPkt); err != nil {
		c.RemoveStream(streamID)
		return nil, err
	}

	return stream, nil
}

func (c *PixieClient) DialTCP(ctx context.Context, host string, port uint16) (*Stream, error) {
	return c.OpenStream(ctx, StrTCP, host, port)
}

func (c *PixieClient) DialUDP(ctx context.Context, host string, port uint16) (*Stream, error) {
	return c.OpenStream(ctx, StrUDP, host, port)
}

func (c *PixieClient) Close() error {
	return c.CloseWithReason(CrVoluntary)
}

func (c *PixieClient) CloseWithReason(reason CloseReason) error {
	if c.Closed.Load() {
		return nil
	}

	_ = c.SendPacket(context.Background(), NewClosePacket(0, reason))

	err := c.PixieCore.Close()
	c.WaitGroup.Wait()
	return err
}

func (c *PixieClient) WasDowngraded() bool {
	return c.Downgraded
}

func (c *PixieClient) GetBufferSize() uint32 {
	return c.BufferSize
}

func (c *PixieClient) Done() <-chan struct{} {
	return c.CloseChannel
}

func (c *PixieClient) GetMetrics() *Metrics {
	return c.PixieCore.GetMetrics()
}

func (c *PixieClient) GetExtensions() []Extension {
	return c.PixieCore.GetExtensions()
}
