package pixie

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
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

type CertAuthExtension struct {
	Required            bool
	SupportedAlgorithms uint8
	Challenge           []byte
	SelectedAlgorithm uint8
	PublicKeyHash     [32]byte
	Signature         []byte
	PrivateKey ed25519.PrivateKey
	Validator func(publicKeyHash [32]byte) ed25519.PublicKey
}

type CertAuthExtensionBuilder struct {
	Required            bool
	SupportedAlgorithms uint8
	ChallengeSize       int
	PrivateKey          ed25519.PrivateKey
	Validator           func(publicKeyHash [32]byte) ed25519.PublicKey
}

const (
	UDPExtensionID      uint8 = 0x01
	PasswordExtensionID uint8 = 0x02
	CertAuthExtensionID uint8 = 0x03
	MOTDExtensionID     uint8 = 0x04
)

const (
	SigAlgoEd25519 uint8 = 0b00000001
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
	DefaultRegistry.AddExtension(&CertAuthExtensionBuilder{})
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
	if role == RoleServer {
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
	if role == RoleClient {
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
	if role == RoleServer && e.Validator != nil {
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

func (e *CertAuthExtension) ID() uint8 { return CertAuthExtensionID }

func (e *CertAuthExtension) Encode(role Role) []byte {
	if role == RoleServer {
		buf := make([]byte, 2+len(e.Challenge))
		if e.Required {
			buf[0] = 1
		} else {
			buf[0] = 0
		}
		buf[1] = e.SupportedAlgorithms
		copy(buf[2:], e.Challenge)
		return buf
	}

	buf := make([]byte, 1+32+len(e.Signature))
	buf[0] = e.SelectedAlgorithm
	copy(buf[1:33], e.PublicKeyHash[:])
	copy(buf[33:], e.Signature)
	return buf
}

func (e *CertAuthExtension) Decode(data []byte, role Role) error {
	if role == RoleClient {
		if len(data) < 2 {
			return ePacketTooSmall
		}
		e.Required = data[0] != 0
		e.SupportedAlgorithms = data[1]
		e.Challenge = make([]byte, len(data)-2)
		copy(e.Challenge, data[2:])
	} else {
		if len(data) < 33 {
			return ePacketTooSmall
		}
		e.SelectedAlgorithm = data[0]
		copy(e.PublicKeyHash[:], data[1:33])
		e.Signature = make([]byte, len(data)-33)
		copy(e.Signature, data[33:])
	}
	return nil
}

func (e *CertAuthExtension) HandleHandshake(ctx context.Context, transport Transport, role Role) error {
	if role == RoleServer && e.Validator != nil {
		publicKey := e.Validator(e.PublicKeyHash)
		if publicKey == nil {
			return eCertAuthFail
		}

		if e.SelectedAlgorithm&SigAlgoEd25519 != 0 {
			if !ed25519.Verify(publicKey, e.Challenge, e.Signature) {
				return eCertAuthFail
			}
		} else {
			return eCertAuthFail
		}
	}
	return nil
}

func (e *CertAuthExtension) SupportedPacketTypes() []PacketType  { return nil }
func (e *CertAuthExtension) CongestionStreamTypes() []StreamType { return nil }

func (e *CertAuthExtension) PacketHandler(ctx context.Context, packetType PacketType, data []byte, transport Transport) error {
	return nil
}

func (e *CertAuthExtension) Clone() Extension {
	clone := &CertAuthExtension{
		Required:            e.Required,
		SupportedAlgorithms: e.SupportedAlgorithms,
		SelectedAlgorithm:   e.SelectedAlgorithm,
		PublicKeyHash:       e.PublicKeyHash,
		Validator:           e.Validator,
	}
	if e.Challenge != nil {
		clone.Challenge = make([]byte, len(e.Challenge))
		copy(clone.Challenge, e.Challenge)
	}
	if e.Signature != nil {
		clone.Signature = make([]byte, len(e.Signature))
		copy(clone.Signature, e.Signature)
	}
	if e.PrivateKey != nil {
		clone.PrivateKey = make(ed25519.PrivateKey, len(e.PrivateKey))
		copy(clone.PrivateKey, e.PrivateKey)
	}
	return clone
}

func (e *CertAuthExtension) SignChallenge() error {
	if e.PrivateKey == nil {
		return fmt.Errorf("no private key configured")
	}
	if len(e.Challenge) == 0 {
		return fmt.Errorf("no challenge to sign")
	}

	publicKey := e.PrivateKey.Public().(ed25519.PublicKey)
	e.PublicKeyHash = sha256.Sum256(publicKey)
	e.SelectedAlgorithm = SigAlgoEd25519
	e.Signature = ed25519.Sign(e.PrivateKey, e.Challenge)

	return nil
}

func (b *CertAuthExtensionBuilder) ID() uint8 { return CertAuthExtensionID }

func (b *CertAuthExtensionBuilder) Build(data []byte, role Role) (Extension, error) {
	ext := &CertAuthExtension{
		Required:            b.Required,
		SupportedAlgorithms: b.SupportedAlgorithms,
		PrivateKey:          b.PrivateKey,
		Validator:           b.Validator,
	}
	if err := ext.Decode(data, role); err != nil {
		return nil, err
	}
	return ext, nil
}

func (b *CertAuthExtensionBuilder) BuildDefault(role Role) Extension {
	ext := &CertAuthExtension{
		Required:            b.Required,
		SupportedAlgorithms: b.SupportedAlgorithms,
		PrivateKey:          b.PrivateKey,
		Validator:           b.Validator,
	}

	if role == RoleServer {
		challengeSize := b.ChallengeSize
		if challengeSize <= 0 {
			challengeSize = 64
		}
		ext.Challenge = make([]byte, challengeSize)
		rand.Read(ext.Challenge)

		if ext.SupportedAlgorithms == 0 {
			ext.SupportedAlgorithms = SigAlgoEd25519
		}
	}

	return ext
}
