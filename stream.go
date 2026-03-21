package pixie

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/soap-phia/pixie/transport"
)

type Stream struct {
	DataChannel chan []byte
	Pixie       *PixieCore
	FlowControl *FlowControl
	DataBuffer  []byte
	Id          uint32
	ReadCount   atomic.Uint32
	Closed      atomic.Bool
	_           [7]byte

	pooledBuf    []byte
	CloseChannel chan struct{}

	CloseOnce     sync.Once
	CloseReason   atomic.Value
	ReadDeadline  atomic.Value
	WriteDeadline atomic.Value

	Host       string
	Port       uint16
	StreamType StreamType
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
	s.CloseReason.Store(CrUnknown)
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
	if len(s.DataBuffer) > 0 {
		n = copy(p, s.DataBuffer)
		s.DataBuffer = s.DataBuffer[n:]
		if len(s.DataBuffer) == 0 && s.pooledBuf != nil {
			transport.ReturnReadMsgBuffer(s.pooledBuf)
			s.pooledBuf = nil
		}
		s.IncrementReadCount()
		return n, nil
	}

	if s.Closed.Load() {
		return 0, s.GetCloseError()
	}

	var deadline time.Time
	if d := s.ReadDeadline.Load(); d != nil {
		deadline = d.(time.Time)
	}

	if deadline.IsZero() {
		select {
		case data, ok := <-s.DataChannel:
			if !ok {
				return 0, s.GetCloseError()
			}
			n = copy(p, data)
			if n < len(data) {
				s.DataBuffer = data[n:]
				s.pooledBuf = data[:cap(data)]
			} else {
				transport.ReturnReadMsgBuffer(data)
			}
			s.IncrementReadCount()
			return n, nil
		case <-s.CloseChannel:
			return 0, s.GetCloseError()
		}
	}

	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()

	select {
	case data, ok := <-s.DataChannel:
		if !ok {
			return 0, s.GetCloseError()
		}
		n = copy(p, data)
		if n < len(data) {
			s.DataBuffer = data[n:]
			s.pooledBuf = data[:cap(data)]
		} else {
			transport.ReturnReadMsgBuffer(data)
		}
		s.IncrementReadCount()
		return n, nil
	case <-t.C:
		return 0, &net.OpError{Op: "read", Net: "wisp", Err: context.DeadlineExceeded}
	case <-s.CloseChannel:
		return 0, s.GetCloseError()
	}
}

func (s *Stream) Write(p []byte) (n int, err error) {
	if s.Closed.Load() {
		return 0, s.GetCloseError()
	}

	ctx := context.Background()
	if d := s.WriteDeadline.Load(); d != nil {
		if deadline, ok := d.(time.Time); ok && !deadline.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, deadline)
			defer cancel()
		}
	}

	fc := s.FlowControl

	if fc != nil && fc.Mode == fcTrackAmt {
		if err := fc.WaitBuffer(ctx); err != nil {
			return 0, &net.OpError{Op: "write", Net: "wisp", Err: err}
		}
	}

	if s.Pixie.UseFramedWrites {
		poolBuf, framedData := EncodeDataFramed(s.Id, p)
		if err := s.Pixie.SendDataFramed(ctx, poolBuf, framedData, len(p)); err != nil {
			return 0, err
		}
	} else {
		poolBuf, data := EncodePacketPooled(s.Id, p)
		if err := s.Pixie.SendPacketRaw(ctx, data); err != nil {
			ReturnPacket(poolBuf)
			return 0, err
		}
		ReturnPacket(poolBuf)
	}

	if fc != nil {
		fc.Subtract()
	}

	return len(p), nil
}

func (s *Stream) Close() error {
	return s.CloseWithReason(CrVoluntary)
}

func (s *Stream) CloseWithReason(reason CloseReason) error {
	var err error
	s.CloseOnce.Do(func() {
		s.Closed.Store(true)
		s.CloseReason.Store(reason)
		close(s.CloseChannel)

		if s.pooledBuf != nil {
			transport.ReturnReadMsgBuffer(s.pooledBuf)
			s.pooledBuf = nil
			s.DataBuffer = nil
		}

		pktData := EncodeClosePacket(s.Id, reason)
		err = s.Pixie.SendPacketRaw(context.Background(), pktData)
		ReturnClosePacket(pktData)

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
		address: net.JoinHostPort(s.Host, strconv.FormatUint(uint64(s.Port), 10)),
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
	select {
	case s.DataChannel <- data:
	case <-s.CloseChannel:
		transport.ReturnReadMsgBuffer(data)
	}
}

func (s *Stream) CloseFromRemote(reason CloseReason, pixieClosing bool) {
	s.CloseOnce.Do(func() {
		s.Closed.Store(true)
		s.CloseReason.Store(reason)
		close(s.CloseChannel)

		for {
			select {
			case data := <-s.DataChannel:
				transport.ReturnReadMsgBuffer(data)
			default:
				goto done
			}
		}
	done:
		close(s.DataChannel)

		if s.pooledBuf != nil {
			transport.ReturnReadMsgBuffer(s.pooledBuf)
			s.pooledBuf = nil
			s.DataBuffer = nil
		}

		if !pixieClosing {
			s.Pixie.RemoveStream(s.Id)
		}
	})
}

func (s *Stream) GetCloseError() error {
	reason := s.CloseReason.Load().(CloseReason)
	if reason == CrVoluntary {
		return io.EOF
	}
	return &StreamError{StreamId: s.Id, CloseReason: reason}
}

func (s *Stream) IncrementReadCount() {
	count := s.ReadCount.Add(1)

	if s.FlowControl != nil && s.FlowControl.ShouldContinue(count) {
		newBuffer := s.FlowControl.Add(count)
		s.ReadCount.Store(0)

		s.Pixie.SendPendingContinue(s.Id, newBuffer)
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
	tmpPointer := GetReadBuffer()
	tmp := *tmpPointer
	defer ReturnReadBuffer(tmpPointer)

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
