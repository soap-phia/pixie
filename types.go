package pixie

import "fmt"

type Version struct {
	Major uint8
	Minor uint8
}

type Role uint8

type CloseReason uint8

func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

var ProtocolVersion = Version{Major: 2, Minor: 0}

const (
	rClient Role = iota
	rServer
)

type StreamType uint8

const (
	sTCP StreamType = 0x01
	sUDP StreamType = 0x02
)

func (s StreamType) String() string {
	switch s {
	case sTCP:
		return "tcp"
	case sUDP:
		return "udp"
	default:
		return fmt.Sprintf("unknown(0x%02x)", uint8(s))
	}
}

const (
	crUnknown          CloseReason = 0x01
	crVoluntary        CloseReason = 0x02
	crUnexpected       CloseReason = 0x03
	crInvalidExtension CloseReason = 0x04
	crInvalidInfo      CloseReason = 0x41
	crUnreachable      CloseReason = 0x42
	crConnTimeout      CloseReason = 0x43
	crRefused          CloseReason = 0x44
	crStreamTimeout    CloseReason = 0x47
	crBlockedAddr      CloseReason = 0x48
	crThrottled        CloseReason = 0x49
	crNetErr           CloseReason = 0x4a
	crClientUnexpected CloseReason = 0x81
	crPassAuthFailed   CloseReason = 0xc0
	crCertAuthFailed   CloseReason = 0xc1
	crAuthReq          CloseReason = 0xc2
)

func (cr CloseReason) String() string {
	switch cr {
	case crUnknown:
		return "Unknown"
	case crVoluntary:
		return "Voluntary"
	case crUnexpected:
		return "Unexpected"
	case crInvalidExtension:
		return "Incompatible extensions"
	case crInvalidInfo:
		return "Invalid information"
	case crUnreachable:
		return "Unreachable"
	case crConnTimeout:
		return "Connection timed out"
	case crRefused:
		return "Connection refused"
	case crStreamTimeout:
		return "Stream timed out"
	case crBlockedAddr:
		return "Blocked address"
	case crThrottled:
		return "Throttled"
	case crNetErr:
		return "Network error"
	case crClientUnexpected:
		return "Client unexpected error"
	case crPassAuthFailed:
		return "Password auth failed"
	case crCertAuthFailed:
		return "Certificate auth failed"
	case crAuthReq:
		return "Authentication required"
	default:
		return fmt.Sprintf("Unknown(0x%02x)", uint8(cr))
	}
}

func (cr CloseReason) Error() string {
	return cr.String()
}
