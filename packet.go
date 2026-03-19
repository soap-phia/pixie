package pixie

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

type PacketType uint8

type Packet interface {
	Type() PacketType
	StreamID() uint32
	Encode() []byte
}

type PacketHeader struct {
	PacketType PacketType
	StreamId uint32
}

type ConnectPacket struct {
	PacketHeader
	StreamType StreamType
	Host       string
	Port       uint16
}

type DataPacket struct {
	PacketHeader
	Payload []byte
}

type ContinuePacket struct {
	PacketHeader
	BufferRemaining uint32
}

type ClosePacket struct {
	PacketHeader
	Reason CloseReason
}

type InfoPacket struct {
	PacketHeader
	Version    Version
	Extensions []ExtensionData
}

type ExtensionData struct {
	Id      uint8
	Payload []byte
}

type PacketReader struct {
	Transport Transport
}

type PacketWriter struct {
	Transport *LockedTransport
}

const (
	pConnect  PacketType = 0x01
	pData     PacketType = 0x02
	pContinue PacketType = 0x03
	pClose    PacketType = 0x04
	pInfo     PacketType = 0x05
)

func (p PacketType) String() string {
	switch p {
	case pConnect:
		return "CONNECT"
	case pData:
		return "DATA"
	case pContinue:
		return "CONTINUE"
	case pClose:
		return "CLOSE"
	case pInfo:
		return "INFO"
	default:
		return fmt.Sprintf("Unknown(0x%02x)", uint8(p))
	}
}

func (h *PacketHeader) Type() PacketType {
	return h.PacketType
}

func (h *PacketHeader) StreamID() uint32 {
	return h.StreamId
}

func NewConnectPacket(streamId uint32, streamType StreamType, host string, port uint16) *ConnectPacket {
	return &ConnectPacket{
		PacketHeader: PacketHeader{
			PacketType: pConnect,
			StreamId: streamId,
		},
		StreamType: streamType,
		Host:       host,
		Port:       port,
	}
}

func (p *ConnectPacket) Encode() []byte {
	buffer := make([]byte, 1+4+1+2+len(p.Host))
	buffer[0] = byte(p.PacketType)
	binary.LittleEndian.PutUint32(buffer[1:5], p.StreamId)
	buffer[5] = byte(p.StreamType)
	binary.LittleEndian.PutUint16(buffer[6:8], p.Port)
	copy(buffer[8:], p.Host)
	return buffer
}

func NewDataPacket(streamId uint32, payload []byte) *DataPacket {
	return &DataPacket{
		PacketHeader: PacketHeader{
			PacketType: pData,
			StreamId: streamId,
		},
		Payload: payload,
	}
}

func (p *DataPacket) Encode() []byte {
	buffer := make([]byte, 1+4+len(p.Payload))
	buffer[0] = byte(p.PacketType)
	binary.LittleEndian.PutUint32(buffer[1:5], p.StreamId)
	copy(buffer[5:], p.Payload)
	return buffer
}

func NewContinuePacket(streamId uint32, bufferRemaining uint32) *ContinuePacket {
	return &ContinuePacket{
		PacketHeader: PacketHeader{
			PacketType: pContinue,
			StreamId: streamId,
		},
		BufferRemaining: bufferRemaining,
	}
}

func (p *ContinuePacket) Encode() []byte {
	buffer := make([]byte, 1+4+4)
	buffer[0] = byte(p.PacketType)
	binary.LittleEndian.PutUint32(buffer[1:5], p.StreamId)
	binary.LittleEndian.PutUint32(buffer[5:9], p.BufferRemaining)
	return buffer
}

func NewClosePacket(streamId uint32, reason CloseReason) *ClosePacket {
	return &ClosePacket{
		PacketHeader: PacketHeader{
			PacketType: pClose,
			StreamId: streamId,
		},
		Reason: reason,
	}
}

func (p *ClosePacket) Encode() []byte {
	buffer := make([]byte, 1+4+1)
	buffer[0] = byte(p.PacketType)
	binary.LittleEndian.PutUint32(buffer[1:5], p.StreamId)
	buffer[5] = byte(p.Reason)
	return buffer
}

func NewInfo(version Version, extensions []ExtensionData) *InfoPacket {
	return &InfoPacket{
		PacketHeader: PacketHeader{
			PacketType: pInfo,
			StreamId: 0,
		},
		Version:    version,
		Extensions: extensions,
	}
}

func (p *InfoPacket) Encode() []byte {
	size := 1 + 4 + 2
	for _, ext := range p.Extensions {
		size += 1 + 4 + len(ext.Payload)
	}

	buffer := make([]byte, size)
	buffer[0] = byte(p.PacketType)
	binary.LittleEndian.PutUint32(buffer[1:5], p.StreamId)
	buffer[5] = p.Version.Major
	buffer[6] = p.Version.Minor

	offset := 7
	for _, ext := range p.Extensions {
		buffer[offset] = ext.Id
		binary.LittleEndian.PutUint32(buffer[offset+1:offset+5], uint32(len(ext.Payload)))
		copy(buffer[offset+5:], ext.Payload)
		offset += 1 + 4 + len(ext.Payload)
	}

	return buffer
}

func DecodePacket(data []byte) (Packet, error) {
	if len(data) < 5 {
		return nil, ePacketTooSmall
	}

	packetType := PacketType(data[0])
	StreamId := binary.LittleEndian.Uint32(data[1:5])
	payload := data[5:]

	switch packetType {
	case pConnect:
		return DecodeConnectPacket(StreamId, payload)
	case pData:
		return DecodeDataPacket(StreamId, payload)
	case pContinue:
		return DecodeContinuePacket(StreamId, payload)
	case pClose:
		return DecodeClosePacket(StreamId, payload)
	case pInfo:
		return DecodeInfoPacket(StreamId, payload)
	default:
		return nil, fmt.Errorf("%w: 0x%02x", eInvalidPacket, packetType)
	}
}

func DecodeConnectPacket(streamId uint32, payload []byte) (*ConnectPacket, error) {
	if len(payload) < 3 {
		return nil, ePacketTooSmall
	}

	return &ConnectPacket{
		PacketHeader: PacketHeader{
			PacketType: pConnect,
			StreamId: streamId,
		},
		StreamType: StreamType(payload[0]),
		Port:       binary.LittleEndian.Uint16(payload[1:3]),
		Host:       string(payload[3:]),
	}, nil
}

func DecodeDataPacket(streamId uint32, payload []byte) (*DataPacket, error) {
	data := make([]byte, len(payload))
	copy(data, payload)

	return &DataPacket{
		PacketHeader: PacketHeader{
			PacketType: pData,
			StreamId: streamId,
		},
		Payload: data,
	}, nil
}

func DecodeContinuePacket(streamId uint32, payload []byte) (*ContinuePacket, error) {
	if len(payload) < 4 {
		return nil, ePacketTooSmall
	}

	return &ContinuePacket{
		PacketHeader: PacketHeader{
			PacketType: pContinue,
			StreamId: streamId,
		},
		BufferRemaining: binary.LittleEndian.Uint32(payload[0:4]),
	}, nil
}

func DecodeClosePacket(streamId uint32, payload []byte) (*ClosePacket, error) {
	if len(payload) < 1 {
		return nil, ePacketTooSmall
	}

	return &ClosePacket{
		PacketHeader: PacketHeader{
			PacketType: pClose,
			StreamId: streamId,
		},
		Reason: CloseReason(payload[0]),
	}, nil
}

func DecodeInfoPacket(streamId uint32, payload []byte) (*InfoPacket, error) {
	if len(payload) < 2 {
		return nil, ePacketTooSmall
	}

	version := Version{
		Major: payload[0],
		Minor: payload[1],
	}

	var extensions []ExtensionData
	offset := 2

	for offset+5 <= len(payload) {
		id := payload[offset]
		length := binary.LittleEndian.Uint32(payload[offset+1 : offset+5])
		offset += 5

		if offset+int(length) > len(payload) {
			return nil, ePacketTooSmall
		}

		extPayload := make([]byte, length)
		copy(extPayload, payload[offset:offset+int(length)])
		extensions = append(extensions, ExtensionData{
			Id:      id,
			Payload: extPayload,
		})
		offset += int(length)
	}

	return &InfoPacket{
		PacketHeader: PacketHeader{
			PacketType: pInfo,
			StreamId: streamId,
		},
		Version:    version,
		Extensions: extensions,
	}, nil
}

func NewPacketReader(t Transport) *PacketReader {
	return &PacketReader{Transport: t}
}

func (r *PacketReader) Read() (Packet, error) {
	data, err := r.Transport.ReadMessage(context.TODO())
	if err != nil {
		if err == io.EOF {
			return nil, eClosedSocket
		}
		return nil, err
	}
	return DecodePacket(data)
}

func NewPacketWriter(t Transport) *PacketWriter {
	return &PacketWriter{Transport: NewLockedTransport(t)}
}

func (w *PacketWriter) Write(p Packet) error {
	return w.Transport.WriteMessage(context.TODO(), p.Encode())
}

func (w *PacketWriter) WriteRaw(data []byte) error {
	return w.Transport.WriteMessage(context.TODO(), data)
}
