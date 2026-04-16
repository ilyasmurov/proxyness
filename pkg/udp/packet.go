package udp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// Message types for the inner payload.
const (
	MsgHandshake    byte = 0x01
	MsgStreamOpen   byte = 0x02
	MsgStreamData   byte = 0x03
	MsgStreamClose  byte = 0x04
	MsgKeepalive    byte = 0x05
	MsgSessionClose byte = 0x07 // server → client: session is being torn down, reconnect immediately
)

// Packet is the logical unit of the UDP transport protocol.
type Packet struct {
	ConnID   uint32 // session token (0 for handshake)
	Type     byte
	StreamID uint32
	Data     []byte
}

// EncodePacket encodes a Packet into a QUIC-disguised UDP datagram.
//
// Wire format:
//
//	[1 byte:  QUIC flags (0x40 | random)]
//	[4 bytes: Connection ID]
//	[N bytes: Encrypted(Type + StreamID + DataLen + Data)]
func EncodePacket(p *Packet, key []byte) ([]byte, error) {
	// Inner payload: type(1) + streamID(4) + dataLen(2) + data(N)
	inner := make([]byte, 1+4+2+len(p.Data))
	inner[0] = p.Type
	binary.BigEndian.PutUint32(inner[1:5], p.StreamID)
	binary.BigEndian.PutUint16(inner[5:7], uint16(len(p.Data)))
	copy(inner[7:], p.Data)

	encrypted, err := Encrypt(key, inner)
	if err != nil {
		return nil, err
	}

	// Outer: flags(1) + connID(4) + encrypted
	out := make([]byte, 1+4+len(encrypted))
	randByte := make([]byte, 1)
	rand.Read(randByte)
	out[0] = 0x40 | (randByte[0] & 0x3f) // QUIC flag set
	binary.BigEndian.PutUint32(out[1:5], p.ConnID)
	copy(out[5:], encrypted)

	return out, nil
}

// DecodePacket decodes a QUIC-disguised UDP datagram.
func DecodePacket(data []byte, key []byte) (*Packet, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}

	connID := binary.BigEndian.Uint32(data[1:5])
	encrypted := data[5:]

	inner, err := Decrypt(key, encrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	if len(inner) < 7 {
		return nil, fmt.Errorf("inner payload too short: %d bytes", len(inner))
	}

	dataLen := binary.BigEndian.Uint16(inner[5:7])
	if len(inner) < 7+int(dataLen) {
		return nil, fmt.Errorf("data truncated: have %d, need %d", len(inner)-7, dataLen)
	}

	return &Packet{
		ConnID:   connID,
		Type:     inner[0],
		StreamID: binary.BigEndian.Uint32(inner[1:5]),
		Data:     inner[7 : 7+dataLen],
	}, nil
}
