package arq

import (
	"encoding/binary"
	"fmt"
)

// AckDataSize is the wire size of AckData: 4 bytes CumAck + 32 bytes bitmap.
const AckDataSize = 4 + 32

// AckData carries a cumulative ACK and a 256-bit selective-ACK bitmap.
// Bit i of Bitmap represents packet (CumAck + 1 + i), for i in [0, 255].
type AckData struct {
	CumAck uint32
	Bitmap [32]byte
}

// SetReceived marks pktNum as received in the bitmap.
// Valid range: (CumAck+1)..(CumAck+256). Out-of-range calls are silently ignored.
func (a *AckData) SetReceived(pktNum uint32) {
	if pktNum <= a.CumAck {
		return
	}
	offset := pktNum - a.CumAck - 1
	if offset > 255 {
		return
	}
	a.Bitmap[offset/8] |= 1 << (offset % 8)
}

// IsReceived reports whether pktNum is marked in the bitmap.
func (a *AckData) IsReceived(pktNum uint32) bool {
	if pktNum <= a.CumAck {
		return false
	}
	offset := pktNum - a.CumAck - 1
	if offset > 255 {
		return false
	}
	return a.Bitmap[offset/8]&(1<<(offset%8)) != 0
}

// Encode serialises AckData to a 36-byte slice (CumAck big-endian + bitmap).
func (a *AckData) Encode() []byte {
	buf := make([]byte, AckDataSize)
	binary.BigEndian.PutUint32(buf[:4], a.CumAck)
	copy(buf[4:], a.Bitmap[:])
	return buf
}

// DecodeAckData parses a 36-byte buffer into AckData.
func DecodeAckData(data []byte) (*AckData, error) {
	if len(data) < AckDataSize {
		return nil, fmt.Errorf("arq: ack data too short: %d < %d", len(data), AckDataSize)
	}
	a := &AckData{}
	a.CumAck = binary.BigEndian.Uint32(data[:4])
	copy(a.Bitmap[:], data[4:4+32])
	return a, nil
}
