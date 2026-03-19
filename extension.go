package pixie

import (
	"context"
	"fmt"
	"sync"
)

type Extension interface {
	ID() uint8
	Encode(role Role) []byte
	Decode(data []byte, role Role) error
	HandleHandshake(ctx context.Context, transport Transport, role Role) error
	SupportedPacketTypes() []PacketType
	PacketHandler(ctx context.Context, packetType PacketType, data []byte, transport Transport) error
	CongestionStreamTypes() []StreamType
	Clone() Extension
}

type ExtensionBuilder interface {
	ID() uint8
	Build(data []byte, role Role) (Extension, error)
	BuildDefault(role Role) Extension
}

type ExtensionRegistry struct {
	Builders map[uint8]ExtensionBuilder
	Mutex    sync.RWMutex
}

type UDPExtension struct{}

type UDPExtensionBuilder struct{}

type PasswordExtension struct {
	Required  bool
	Username  string
	Password  string
	Validator func(username, password string) bool
}

type PasswordExtensionBuilder struct {
	Username  string
	Password  string
	Required  bool
	Validator func(username, password string) bool
}

type MOTDExtension struct {
	Message string
}

type MOTDExtensionBuilder struct {
	Message string
}

const (
	UDPExtensionID      uint8 = 0x01
	PasswordExtensionID uint8 = 0x02
	MOTDExtensionID     uint8 = 0x04
)

var DefaultRegistry = NewExtensionRegistry()

func NewExtensionRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{
		Builders: make(map[uint8]ExtensionBuilder),
	}
}

func (r *ExtensionRegistry) AddExtension(builder ExtensionBuilder) {
	r.Mutex.Lock()
	defer r.Mutex.Unlock()
	r.Builders[builder.ID()] = builder
}

func (r *ExtensionRegistry) Get(id uint8) (ExtensionBuilder, bool) {
	r.Mutex.RLock()
	defer r.Mutex.RUnlock()
	b, ok := r.Builders[id]
	return b, ok
}

func (r *ExtensionRegistry) BuildExtension(id uint8, data []byte, role Role) (Extension, error) {
	builder, ok := r.Get(id)
	if !ok {
		return nil, fmt.Errorf("%w: extension 0x%02x", eInvalidExtension, id)
	}
	return builder.Build(data, role)
}

func init() {
	DefaultRegistry.AddExtension(&UDPExtensionBuilder{})
	DefaultRegistry.AddExtension(&PasswordExtensionBuilder{})
	DefaultRegistry.AddExtension(&MOTDExtensionBuilder{})
}

func (e *UDPExtension) ID() uint8                           { return UDPExtensionID }
func (e *UDPExtension) Encode(role Role) []byte             { return nil }
func (e *UDPExtension) Decode(data []byte, role Role) error { return nil }
func (e *UDPExtension) SupportedPacketTypes() []PacketType  { return nil }
func (e *UDPExtension) CongestionStreamTypes() []StreamType { return nil }
func (e *UDPExtension) Clone() Extension                    { return &UDPExtension{} }

func (e *UDPExtension) HandleHandshake(ctx context.Context, transport Transport, role Role) error {
	return nil
}

func (e *UDPExtension) PacketHandler(ctx context.Context, packetType PacketType, data []byte, transport Transport) error {
	return nil
}

func (b *UDPExtensionBuilder) ID() uint8 { return UDPExtensionID }

func (b *UDPExtensionBuilder) Build(data []byte, role Role) (Extension, error) {
	return &UDPExtension{}, nil
}

func (b *UDPExtensionBuilder) BuildDefault(role Role) Extension {
	return &UDPExtension{}
}

func (e *PasswordExtension) ID() uint8 { return PasswordExtensionID }

func (e *PasswordExtension) Encode(role Role) []byte {
	if role == rServer {
		if e.Required {
			return []byte{1}
		}
		return []byte{0}
	}
	buffer := make([]byte, 1+len(e.Username)+len(e.Password))
	buffer[0] = byte(len(e.Username))
	copy(buffer[1:], e.Username)
	copy(buffer[1+len(e.Username):], e.Password)
	return buffer
}

func (e *PasswordExtension) Decode(data []byte, role Role) error {
	if role == rClient {
		if len(data) >= 1 {
			e.Required = data[0] != 0
		}
	} else {
		if len(data) < 1 {
			return ePacketTooSmall
		}
		usernameLen := int(data[0])
		if len(data) < 1+usernameLen {
			return ePacketTooSmall
		}
		e.Username = string(data[1 : 1+usernameLen])
		e.Password = string(data[1+usernameLen:])
	}
	return nil
}

func (e *PasswordExtension) HandleHandshake(ctx context.Context, transport Transport, role Role) error {
	if role == rServer && e.Validator != nil {
		if !e.Validator(e.Username, e.Password) {
			return eAuthFail
		}
	}
	return nil
}

func (e *PasswordExtension) SupportedPacketTypes() []PacketType  { return nil }
func (e *PasswordExtension) CongestionStreamTypes() []StreamType { return nil }

func (e *PasswordExtension) PacketHandler(ctx context.Context, packetType PacketType, data []byte, transport Transport) error {
	return nil
}

func (e *PasswordExtension) Clone() Extension {
	return &PasswordExtension{
		Required:  e.Required,
		Username:  e.Username,
		Password:  e.Password,
		Validator: e.Validator,
	}
}

func (b *PasswordExtensionBuilder) ID() uint8 { return PasswordExtensionID }

func (b *PasswordExtensionBuilder) Build(data []byte, role Role) (Extension, error) {
	ext := &PasswordExtension{
		Username:  b.Username,
		Password:  b.Password,
		Required:  b.Required,
		Validator: b.Validator,
	}
	if err := ext.Decode(data, role); err != nil {
		return nil, err
	}
	return ext, nil
}

func (b *PasswordExtensionBuilder) BuildDefault(role Role) Extension {
	return &PasswordExtension{
		Required:  b.Required,
		Username:  b.Username,
		Password:  b.Password,
		Validator: b.Validator,
	}
}

func (e *MOTDExtension) ID() uint8               { return MOTDExtensionID }
func (e *MOTDExtension) Encode(role Role) []byte { return []byte(e.Message) }

func (e *MOTDExtension) Decode(data []byte, role Role) error {
	e.Message = string(data)
	return nil
}

func (e *MOTDExtension) HandleHandshake(ctx context.Context, transport Transport, role Role) error {
	return nil
}

func (e *MOTDExtension) SupportedPacketTypes() []PacketType  { return nil }
func (e *MOTDExtension) CongestionStreamTypes() []StreamType { return nil }

func (e *MOTDExtension) PacketHandler(ctx context.Context, packetType PacketType, data []byte, transport Transport) error {
	return nil
}

func (e *MOTDExtension) Clone() Extension {
	return &MOTDExtension{Message: e.Message}
}

func (b *MOTDExtensionBuilder) ID() uint8 { return MOTDExtensionID }

func (b *MOTDExtensionBuilder) Build(data []byte, role Role) (Extension, error) {
	return &MOTDExtension{Message: string(data)}, nil
}

func (b *MOTDExtensionBuilder) BuildDefault(role Role) Extension {
	return &MOTDExtension{Message: b.Message}
}
