package pixie

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
)

type WriteRequest struct {
	Data    []byte
	PoolBuf []byte
}

type BatchTransport struct {
	Transport    Transport
	WriteChannel chan WriteRequest
	CloseOnce    sync.Once
	CloseChannel chan struct{}
	NetConn      net.Conn
	FrameWrites  bool
}

type NetConnWriter interface {
	NetConn() net.Conn
}

var framePool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, 8192)
	},
}

func NewBatchTransport(t Transport, batchSize int) *BatchTransport {
	if batchSize <= 0 {
		batchSize = 4096 // funny number
	}

	bt := &BatchTransport{
		Transport:    t,
		WriteChannel: make(chan WriteRequest, batchSize),
		CloseChannel: make(chan struct{}),
	}

	if ncw, ok := t.(NetConnWriter); ok {
		bt.NetConn = ncw.NetConn()
	}

	go bt.WriteBatchLoop()

	return bt
}

func NewFramedTransport(conn net.Conn, batchSize int) *BatchTransport {
	if batchSize <= 0 {
		batchSize = 4096
	}

	bt := &BatchTransport{
		WriteChannel: make(chan WriteRequest, batchSize),
		CloseChannel: make(chan struct{}),
		NetConn:      conn,
		FrameWrites:  true,
	}

	go bt.WriteBatchLoop()

	return bt
}

func FrameWs(data []byte) []byte {
	payloadLen := len(data)
	totalLen := payloadLen + 2

	if payloadLen > 125 && payloadLen <= 65535 {
		totalLen = payloadLen + 4
	} else if payloadLen > 65535 {
		totalLen = payloadLen + 10
	}

	var frame []byte
	if totalLen <= 8192 {
		poolBuf := framePool.Get().([]byte)
		if cap(poolBuf) >= totalLen {
			frame = poolBuf[:totalLen]
		} else {
			framePool.Put(poolBuf)
			frame = make([]byte, totalLen)
		}
	} else {
		frame = make([]byte, totalLen)
	}

	frame[0] = 0x82
	if payloadLen <= 125 {
		frame[1] = byte(payloadLen)
		copy(frame[2:], data)
	} else if payloadLen <= 65535 {
		frame[1] = 126
		binary.BigEndian.PutUint16(frame[2:4], uint16(payloadLen))
		copy(frame[4:], data)
	} else {
		frame[1] = 127
		binary.BigEndian.PutUint64(frame[2:10], uint64(payloadLen))
		copy(frame[10:], data)
	}
	return frame
}

func (bt *BatchTransport) WriteBatchLoop() {
	batch := make(net.Buffers, 0, 512)
	poolBufs := make([][]byte, 0, 512)

	for {
		select {
		case <-bt.CloseChannel:
			return
		case req := <-bt.WriteChannel:
			batch = batch[:0]
			poolBufs = poolBufs[:0]

			batch = append(batch, req.Data)
			poolBufs = append(poolBufs, req.PoolBuf)

			pending := len(bt.WriteChannel)
			for i := 0; i < pending && len(batch) < 512; i++ {
				nextReq := <-bt.WriteChannel
				batch = append(batch, nextReq.Data)
				poolBufs = append(poolBufs, nextReq.PoolBuf)
			}

			var err error
			if bt.NetConn != nil {
				_, err = batch.WriteTo(bt.NetConn)
			} else {
				for _, d := range batch {
					if err = bt.Transport.WriteMessage(context.Background(), d); err != nil {
						break
					}
				}
			}

			for _, pb := range poolBufs {
				ReturnPacket(pb)
			}

			if err != nil {
				return
			}
		}
	}
}

func (bt *BatchTransport) ReadMessage(ctx context.Context) ([]byte, error) {
	if bt.Transport != nil {
		return bt.Transport.ReadMessage(ctx)
	}
	return nil, ePixieClosed
}

func (bt *BatchTransport) WriteMessage(data []byte) error {
	if bt.FrameWrites {
		data = FrameWs(data)
	}

	select {
	case bt.WriteChannel <- WriteRequest{Data: data}:
		return nil
	case <-bt.CloseChannel:
		return ePixieClosed
	}
}

func (bt *BatchTransport) WriteMessagePooled(poolBuf []byte, framedData []byte) error {
	select {
	case bt.WriteChannel <- WriteRequest{Data: framedData, PoolBuf: poolBuf}:
		return nil
	case <-bt.CloseChannel:
		ReturnPacket(poolBuf)
		return ePixieClosed
	}
}

func (bt *BatchTransport) Close() error {
	bt.CloseOnce.Do(func() {
		close(bt.CloseChannel)
	})
	if bt.Transport != nil {
		return bt.Transport.Close()
	}
	if bt.NetConn != nil {
		return bt.NetConn.Close()
	}
	return nil
}
