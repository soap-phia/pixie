package pixie

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Stream struct {
	Id         uint32
	StreamType StreamType
	Host       string
	Port       uint16

	Pixie       *PixieCore
	FlowControl *FlowControl

	DataChannel chan []byte
	DataBuffer  []byte

	Closed       atomic.Bool
	CloseReason  atomic.Value
	CloseChannel chan struct{}
	CloseOnce    sync.Once

	ReadCount atomic.Uint32

	ReadDeadline  atomic.Value
	WriteDeadline atomic.Value
}

func NewStream(id uint32, streamType StreamType, pixie *PixieCore, flowControl *FlowControl, bufferSize int) *Stream {
	s := &Stream{
		Id:           id,
		StreamType:   streamType,
		Pixie:        pixie,
		FlowControl:  flowControl,
		DataChannel:  make(chan []byte, bufferSize),
		CloseChannel: make(chan struct{}),
	}
	s.CloseReason.Store(crUnknown)
	return s
}

func (s *Stream) ID() uint32 {
	return s.Id
}

func (s *Stream) GetType() StreamType {
	return s.StreamType
}

func (s *Stream) GetHost() string {
	return s.Host
}

func (s *Stream) GetPort() uint16 {
	return s.Port
}

func (s *Stream) Read(p []byte) (n int, err error) {
	if s.Closed.Load() {
		return 0, s.GetCloseError()
	}

	var deadline time.Time
	if d := s.ReadDeadline.Load(); d != nil {
		deadline = d.(time.Time)
	}

	if len(s.DataBuffer) > 0 {
		n = copy(p, s.DataBuffer)
		s.DataBuffer = s.DataBuffer[n:]
		s.IncrementReadCount()
		return n, nil
	}

	var timer <-chan time.Time
	if !deadline.IsZero() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		timer = t.C
	}

	select {
	case data, ok := <-s.DataChannel:
		if !ok {
			return 0, s.GetCloseError()
		}
		n = copy(p, data)
		if n < len(data) {
			s.DataBuffer = data[n:]
		}
		s.IncrementReadCount()
		return n, nil
	case <-timer:
		return 0, &net.OpError{Op: "read", Net: "wisp", Err: context.DeadlineExceeded}
	case <-s.CloseChannel:
		return 0, s.GetCloseError()
	}
}

func (s *Stream) Write(p []byte) (n int, err error) {
	if s.Closed.Load() {
		return 0, s.GetCloseError()
	}

	var deadline time.Time
	if d := s.WriteDeadline.Load(); d != nil {
		deadline = d.(time.Time)
	}

	ctx := context.Background()
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}

	if s.FlowControl != nil && s.FlowControl.Mode == fcTrackAmt {
		if err := s.FlowControl.WaitBuffer(ctx); err != nil {
			return 0, &net.OpError{Op: "write", Net: "wisp", Err: err}
		}
	}

	pkt := NewDataPacket(s.Id, p)
	if err := s.Pixie.SendPacket(ctx, pkt); err != nil {
		return 0, err
	}

	if s.FlowControl != nil {
		s.FlowControl.Subtract()
	}

	return len(p), nil
}

func (s *Stream) Close() error {
	return s.CloseWithReason(crVoluntary)
}

func (s *Stream) CloseWithReason(reason CloseReason) error {
	var err error
	s.CloseOnce.Do(func() {
		s.Closed.Store(true)
		s.CloseReason.Store(reason)
		close(s.CloseChannel)

		pkt := NewClosePacket(s.Id, reason)
		err = s.Pixie.SendPacket(context.Background(), pkt)

		s.Pixie.RemoveStream(s.Id)
	})
	return err
}

func (s *Stream) SetDeadline(t time.Time) error {
	s.ReadDeadline.Store(t)
	s.WriteDeadline.Store(t)
	return nil
}

func (s *Stream) SetReadDeadline(t time.Time) error {
	s.ReadDeadline.Store(t)
	return nil
}

func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.WriteDeadline.Store(t)
	return nil
}

func (s *Stream) LocalAddr() net.Addr {
	return &wispAddr{network: "wisp", address: "local"}
}

func (s *Stream) RemoteAddr() net.Addr {
	return &wispAddr{
		network: s.StreamType.String(),
		address: net.JoinHostPort(s.Host, string(rune(s.Port))),
	}
}

func (s *Stream) GetCloseReason() CloseReason {
	return s.CloseReason.Load().(CloseReason)
}

func (s *Stream) IsClosed() bool {
	return s.Closed.Load()
}

func (s *Stream) Done() <-chan struct{} {
	return s.CloseChannel
}

func (s *Stream) ReceiveData(data []byte) {
	if s.Closed.Load() {
		return
	}

	select {
	case s.DataChannel <- data:
	default:
	}
}

func (s *Stream) CloseFromRemote(reason CloseReason, pixieClosing bool) {
	s.CloseOnce.Do(func() {
		s.Closed.Store(true)
		s.CloseReason.Store(reason)
		close(s.CloseChannel)
		close(s.DataChannel)
		if !pixieClosing {
			s.Pixie.RemoveStream(s.Id)
		}
	})
}

func (s *Stream) GetCloseError() error {
	reason := s.CloseReason.Load().(CloseReason)
	if reason == crVoluntary {
		return io.EOF
	}
	return &StreamError{StreamId: s.Id, CloseReason: reason}
}

func (s *Stream) IncrementReadCount() {
	count := s.ReadCount.Add(1)

	if s.FlowControl != nil && s.FlowControl.ShouldContinue(count) {
		newBuffer := s.FlowControl.Add(count)
		s.ReadCount.Store(0)

		pkt := NewContinuePacket(s.Id, newBuffer)
		_ = s.Pixie.SendPacket(context.Background(), pkt)
	}
}

type wispAddr struct {
	network string
	address string
}

func (a *wispAddr) Network() string {
	return a.network
}

func (a *wispAddr) String() string {
	return a.address
}

var _ net.Conn = (*Stream)(nil)

type StreamReader struct {
	stream *Stream
}

func NewStreamReader(s *Stream) *StreamReader {
	return &StreamReader{stream: s}
}

func (r *StreamReader) Read(p []byte) (n int, err error) {
	return r.stream.Read(p)
}

func (r *StreamReader) ReadAll() ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.stream.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, nil
			}
			return buf, err
		}
	}
}

type StreamWriter struct {
	stream *Stream
}

func NewStreamWriter(s *Stream) *StreamWriter {
	return &StreamWriter{stream: s}
}

func (w *StreamWriter) Write(p []byte) (n int, err error) {
	return w.stream.Write(p)
}

func (w *StreamWriter) WriteString(s string) (n int, err error) {
	return w.stream.Write([]byte(s))
}
