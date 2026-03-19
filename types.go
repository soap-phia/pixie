package pixie

import "fmt"

type Version struct {
	Major uint8
	Minor uint8
}

type Role uint8

type CloseReason uint8

type StreamType uint8

const (
	RoleClient Role = iota
	RoleServer
)

const (
	StrTCP StreamType = 0x01
	StrUDP StreamType = 0x02
)

const (
	CrUnknown          CloseReason = 0x01
	CrVoluntary        CloseReason = 0x02
	CrUnexpected       CloseReason = 0x03
	CrInvalidExtension CloseReason = 0x04
	CrInvalidInfo      CloseReason = 0x41
	CrUnreachable      CloseReason = 0x42
	CrConnTimeout      CloseReason = 0x43
	CrRefused          CloseReason = 0x44
	CrStreamTimeout    CloseReason = 0x47
	CrBlockedAddr      CloseReason = 0x48
	CrThrottled        CloseReason = 0x49
	CrNetErr           CloseReason = 0x4a
	CrClientUnexpected CloseReason = 0x81
	CrPassAuthFailed   CloseReason = 0xc0
	CrCertAuthFailed   CloseReason = 0xc1
	CrAuthReq          CloseReason = 0xc2
)

var ProtocolVersion = Version{Major: 2, Minor: 0}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func (s StreamType) String() string {
	switch s {
	case StrTCP:
		return "tcp"
	case StrUDP:
		return "udp"
	default:
		return fmt.Sprintf("unknown(0x%02x)", uint8(s))
	}
}

func (cr CloseReason) String() string {
	switch cr {
	case CrUnknown:
		return "Unknown"
	case CrVoluntary:
		return "Voluntary"
	case CrUnexpected:
		return "Unexpected"
	case CrInvalidExtension:
		return "Incompatible extensions"
	case CrInvalidInfo:
		return "Invalid information"
	case CrUnreachable:
		return "Unreachable"
	case CrConnTimeout:
		return "Connection timed out"
	case CrRefused:
		return "Connection refused"
	case CrStreamTimeout:
		return "Stream timed out"
	case CrBlockedAddr:
		return "Blocked address"
	case CrThrottled:
		return "Throttled"
	case CrNetErr:
		return "Network error"
	case CrClientUnexpected:
		return "Client unexpected error"
	case CrPassAuthFailed:
		return "Password auth failed"
	case CrCertAuthFailed:
		return "Certificate auth failed"
	case CrAuthReq:
		return "Authentication required"
	default:
		return fmt.Sprintf("Unknown(0x%02x)", uint8(cr))
	}
}

func (cr CloseReason) Error() string {
	return cr.String()
}
