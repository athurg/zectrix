// Package zectrix is a minimal Go client for the ZECTRIX Note 4 LAN v1
// protocol: mDNS discovery, connect, keepalive, status query and close.
package zectrix

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Message types (LAN v1). Responses set the high bit (0x80).
const (
	TypeInfo        uint8 = 1
	TypeConnect     uint8 = 2
	TypePing        uint8 = 3
	TypeClose       uint8 = 4
	TypeImageBegin  uint8 = 5
	TypeImageData   uint8 = 6
	TypeImageStatus uint8 = 7
	TypeAudioBegin  uint8 = 8
	TypeAudioData   uint8 = 9
	TypeAudioEnd    uint8 = 10
	TypeAudioStop   uint8 = 11
	TypeState       uint8 = 12
	TypeResult      uint8 = 13
	TypeKey         uint8 = 14
	TypeStatus      uint8 = 15
	TypePalette     uint8 = 16

	responseBit uint8 = 0x80
)

const (
	DefaultPort = 32342
	maxDatagram = 1200
	headerLen   = 16
	// Note4Model is the model string the Note 4 reports in INFO and mDNS TXT.
	Note4Model = "zectrix-s3-epaper-4.2"
)

// Packet is one datagram: "ZL", version 1, type, u64 connection, u32 sequence, body.
type Packet struct {
	Kind uint8
	Conn uint64
	Seq  uint32
	Body []byte
}

func (p Packet) Encode() []byte {
	b := make([]byte, headerLen, headerLen+len(p.Body))
	b[0], b[1], b[2], b[3] = 'Z', 'L', 1, p.Kind
	binary.BigEndian.PutUint64(b[4:], p.Conn)
	binary.BigEndian.PutUint32(b[12:], p.Seq)
	return append(b, p.Body...)
}

func Decode(b []byte) (Packet, bool) {
	if len(b) < headerLen || len(b) > maxDatagram || b[0] != 'Z' || b[1] != 'L' || b[2] != 1 {
		return Packet{}, false
	}
	return Packet{
		Kind: b[3],
		Conn: binary.BigEndian.Uint64(b[4:]),
		Seq:  binary.BigEndian.Uint32(b[12:]),
		Body: append([]byte(nil), b[headerLen:]...),
	}, true
}

// Result codes carried in the first response byte.
var (
	ErrBusy          = errors.New("zectrix: device busy")
	ErrInvalid       = errors.New("zectrix: request rejected as invalid")
	ErrStale         = errors.New("zectrix: connection is no longer valid")
	ErrFailed        = errors.New("zectrix: device processing failed")
	ErrOldRequest    = errors.New("zectrix: request sequence is outdated")
	ErrResultUnknown = errors.New("zectrix: no response; result unknown, do not resend automatically")
	ErrDisconnected  = errors.New("zectrix: disconnected")
	ErrBadResponse   = errors.New("zectrix: malformed response")
)

// ErrWrongModel means the address answered but is not a Note 4; retrying
// will not help.
var ErrWrongModel = errors.New("zectrix: device is not a Note 4")

func resultErr(body []byte) error {
	if len(body) == 0 {
		return ErrBadResponse
	}
	switch body[0] {
	case 0:
		return nil
	case 1:
		return ErrBusy
	case 2:
		return ErrInvalid
	case 3:
		return ErrStale
	case 4:
		return ErrFailed
	case 5:
		return ErrOldRequest
	}
	return fmt.Errorf("%w: result code %d", ErrBadResponse, body[0])
}

// CloseReason is the shared end-reason code used by CLOSE and friends.
type CloseReason uint8

const (
	ReasonNormal CloseReason = iota
	ReasonDeviceExit
	ReasonKeepaliveTimeout
	ReasonNetwork
	ReasonSystem
	ReasonInterrupted
	ReasonLimit
	ReasonFailed
)

func (r CloseReason) String() string {
	names := [...]string{"normal", "device exit", "keepalive timeout", "network change",
		"system interrupt", "key interrupt", "limit exceeded", "failed"}
	if int(r) < len(names) {
		return names[r]
	}
	return fmt.Sprintf("reason(%d)", uint8(r))
}
