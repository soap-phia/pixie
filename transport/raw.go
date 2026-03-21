package transport

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"unsafe"
)

const (
	defaultReadBufSize  = 64 * 1024
	readMsgPoolSize     = 64 * 1024
	defaultSocketBuffer = 1 << 20
)

var readMsgPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, readMsgPoolSize)
	},
}

func GetReadMsgBuffer(size int) []byte {
	if size <= readMsgPoolSize {
		buf := readMsgPool.Get().([]byte)
		return buf[:size]
	}
	return make([]byte, size)
}

func ReturnReadMsgBuffer(buf []byte) {
	if cap(buf) >= readMsgPoolSize {
		readMsgPool.Put(buf[:cap(buf)])
	}
}

type RawWebSocket struct {
	Reader       *bufio.Reader
	Conn         net.Conn
	HeaderBuffer [14]byte
	WriteMutex   sync.Mutex
}

func NewRawWebSocket(conn net.Conn) *RawWebSocket {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetReadBuffer(defaultSocketBuffer)
		tc.SetWriteBuffer(defaultSocketBuffer)
	}

	return &RawWebSocket{
		Conn:   conn,
		Reader: bufio.NewReaderSize(conn, defaultReadBufSize),
	}
}

func (t *RawWebSocket) ReadMessage(ctx context.Context) ([]byte, error) {
	if _, err := io.ReadFull(t.Reader, t.HeaderBuffer[:2]); err != nil {
		return nil, err
	}

	masked := t.HeaderBuffer[1]&0x80 != 0
	lenCode := t.HeaderBuffer[1] & 0x7F

	var payloadLen uint64
	switch {
	case lenCode <= 125:
		payloadLen = uint64(lenCode)
	case lenCode == 126:
		if _, err := io.ReadFull(t.Reader, t.HeaderBuffer[2:4]); err != nil {
			return nil, err
		}
		payloadLen = uint64(binary.BigEndian.Uint16(t.HeaderBuffer[2:4]))
	case lenCode == 127:
		if _, err := io.ReadFull(t.Reader, t.HeaderBuffer[2:10]); err != nil {
			return nil, err
		}
		payloadLen = binary.BigEndian.Uint64(t.HeaderBuffer[2:10])
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(t.Reader, maskKey[:]); err != nil {
			return nil, err
		}
	}

	result := GetReadMsgBuffer(int(payloadLen))

	if payloadLen > 0 {
		if _, err := io.ReadFull(t.Reader, result); err != nil {
			return nil, err
		}
	}

	if masked && payloadLen > 0 {
		MaskXor(result, maskKey)
	}

	return result, nil
}

func (t *RawWebSocket) WriteMessage(ctx context.Context, data []byte) error {
	t.WriteMutex.Lock()
	defer t.WriteMutex.Unlock()

	payloadLen := len(data)

	var header [10]byte
	var headerLen int

	header[0] = 0x82
	if payloadLen <= 125 {
		header[1] = byte(payloadLen)
		headerLen = 2
	} else if payloadLen <= 65535 {
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(payloadLen))
		headerLen = 4
	} else {
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:10], uint64(payloadLen))
		headerLen = 10
	}

	bufs := net.Buffers{header[:headerLen], data}
	_, err := bufs.WriteTo(t.Conn)
	return err
}

func (t *RawWebSocket) Close() error {
	return t.Conn.Close()
}

func (t *RawWebSocket) NetConn() net.Conn {
	return t.Conn
}

func MaskXor(b []byte, key [4]byte) {
	maskKey := *(*uint32)(unsafe.Pointer(&key[0]))
	key64 := uint64(maskKey)<<32 | uint64(maskKey)

	for len(b) >= 64 {
		p := unsafe.Pointer(&b[0])
		*(*uint64)(p) ^= key64
		*(*uint64)(unsafe.Add(p, 8)) ^= key64
		*(*uint64)(unsafe.Add(p, 16)) ^= key64
		*(*uint64)(unsafe.Add(p, 24)) ^= key64
		*(*uint64)(unsafe.Add(p, 32)) ^= key64
		*(*uint64)(unsafe.Add(p, 40)) ^= key64
		*(*uint64)(unsafe.Add(p, 48)) ^= key64
		*(*uint64)(unsafe.Add(p, 56)) ^= key64
		b = b[64:]
	}

	for len(b) >= 8 {
		*(*uint64)(unsafe.Pointer(&b[0])) ^= key64
		b = b[8:]
	}

	for i := range b {
		b[i] ^= key[i&3]
	}
}
