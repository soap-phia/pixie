package pixie

import (
	"encoding/binary"
	"sync"
)

const (
	DefaultDataBufSize = 131072 + 15
	DefaultReadBufSize = 131072
)

type PacketPool struct {
	ContinuePool sync.Pool
	ClosePool    sync.Pool
	DataPool     sync.Pool
	ReadPool     sync.Pool
}

var packetPool = &PacketPool{
	ContinuePool: sync.Pool{
		New: func() interface{} {
			return make([]byte, 9)
		},
	},
	ClosePool: sync.Pool{
		New: func() interface{} {
			return make([]byte, 6)
		},
	},
	DataPool: sync.Pool{
		New: func() interface{} {
			return make([]byte, DefaultDataBufSize)
		},
	},
	ReadPool: sync.Pool{
		New: func() interface{} {
			buf := make([]byte, DefaultReadBufSize)
			return &buf
		},
	},
}

var controlPacketPool = packetPool

func EncodeContinuePacket(streamId uint32, bufferRemaining uint32) []byte {
	buffer := controlPacketPool.ContinuePool.Get().([]byte)
	buffer[0] = byte(pContinue)
	binary.LittleEndian.PutUint32(buffer[1:5], streamId)
	binary.LittleEndian.PutUint32(buffer[5:9], bufferRemaining)
	return buffer
}

func ReturnContinuePacket(buffer []byte) {
	if len(buffer) == 9 {
		controlPacketPool.ContinuePool.Put(buffer)
	}
}

func EncodeClosePacket(streamId uint32, reason CloseReason) []byte {
	buffer := controlPacketPool.ClosePool.Get().([]byte)
	buffer[0] = byte(pClose)
	binary.LittleEndian.PutUint32(buffer[1:5], streamId)
	buffer[5] = byte(reason)
	return buffer
}

func ReturnClosePacket(buffer []byte) {
	if len(buffer) == 6 {
		controlPacketPool.ClosePool.Put(buffer)
	}
}

func EncodePacketPooled(streamId uint32, payload []byte) (poolBuf []byte, data []byte) {
	totalLen := 1 + 4 + len(payload)

	if totalLen <= DefaultDataBufSize {
		poolBuf = packetPool.DataPool.Get().([]byte)
		data = poolBuf[:totalLen]
	} else {
		poolBuf = nil
		data = make([]byte, totalLen)
	}

	data[0] = byte(pData)
	binary.LittleEndian.PutUint32(data[1:5], streamId)
	copy(data[5:], payload)
	return poolBuf, data
}

func ReturnPacket(poolBuf []byte) {
	if poolBuf != nil && cap(poolBuf) >= DefaultDataBufSize {
		packetPool.DataPool.Put(poolBuf)
	}
}

func GetReadBuffer() *[]byte {
	return packetPool.ReadPool.Get().(*[]byte)
}

func ReturnReadBuffer(buf *[]byte) {
	if buf != nil && cap(*buf) >= DefaultReadBufSize {
		packetPool.ReadPool.Put(buf)
	}
}

func EncodeDataFramed(streamId uint32, payload []byte) (poolBuf []byte, framedData []byte) {
	wispLen := 1 + 4 + len(payload)

	var wsHeaderLen int
	if wispLen <= 125 {
		wsHeaderLen = 2
	} else if wispLen <= 65535 {
		wsHeaderLen = 4
	} else {
		wsHeaderLen = 10
	}

	totalLen := wsHeaderLen + wispLen

	if totalLen <= DefaultDataBufSize {
		poolBuf = packetPool.DataPool.Get().([]byte)
		framedData = poolBuf[:totalLen]
	} else {
		poolBuf = nil
		framedData = make([]byte, totalLen)
	}

	framedData[0] = 0x82

	switch wsHeaderLen {
	case 2:
		framedData[1] = byte(wispLen)
	case 4:
		framedData[1] = 126
		binary.BigEndian.PutUint16(framedData[2:4], uint16(wispLen))
	case 10:
		framedData[1] = 127
		binary.BigEndian.PutUint64(framedData[2:10], uint64(wispLen))
	}

	framedData[wsHeaderLen] = byte(pData)
	binary.LittleEndian.PutUint32(framedData[wsHeaderLen+1:wsHeaderLen+5], streamId)

	copy(framedData[wsHeaderLen+5:], payload)

	return poolBuf, framedData
}
