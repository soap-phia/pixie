package pixie

import (
	"encoding/binary"
	"sync"
)

type ControlPacketPool struct {
	ContinuePool sync.Pool
	ClosePool    sync.Pool
}

var controlPacketPool = &ControlPacketPool{
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
}

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
