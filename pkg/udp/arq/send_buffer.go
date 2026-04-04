package arq

import (
	"sync"
	"time"
)

const maxRetransmits = 10

// SentPacket holds a packet that has been sent but not yet acknowledged.
type SentPacket struct {
	PktNum      uint32
	RawData     []byte
	MsgType     byte
	StreamID    uint32
	Seq         uint32
	Payload     []byte
	SentAt      time.Time
	LastSentAt  time.Time
	Retransmits int
	Acked       bool
}

// IsRetransmit reports whether this packet has been retransmitted at least once.
func (p *SentPacket) IsRetransmit() bool {
	return p.Retransmits > 0
}

// SendBuffer stores unacknowledged sent packets for potential retransmission.
type SendBuffer struct {
	mu      sync.Mutex
	packets map[uint32]*SentPacket
	minPkt  uint32
	maxSize int
}

// NewSendBuffer creates a SendBuffer with the given maximum capacity.
func NewSendBuffer(maxSize int) *SendBuffer {
	return &SendBuffer{
		packets: make(map[uint32]*SentPacket),
		maxSize: maxSize,
	}
}

// Add stores a new sent packet. SentAt and LastSentAt are set to now.
func (sb *SendBuffer) Add(pktNum uint32, raw []byte, msgType byte, streamID, seq uint32, payload []byte) {
	now := time.Now()
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.packets[pktNum] = &SentPacket{
		PktNum:     pktNum,
		RawData:    raw,
		MsgType:    msgType,
		StreamID:   streamID,
		Seq:        seq,
		Payload:    payload,
		SentAt:     now,
		LastSentAt: now,
	}
}

// Get returns the SentPacket for the given packet number, or nil if not present.
func (sb *SendBuffer) Get(pktNum uint32) *SentPacket {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.packets[pktNum]
}

// AckCumulative removes all packets with PktNum <= cumAck and returns the
// number of packets removed.
func (sb *SendBuffer) AckCumulative(cumAck uint32) int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	count := 0
	for pktNum := range sb.packets {
		if pktNum <= cumAck {
			delete(sb.packets, pktNum)
			count++
		}
	}
	return count
}

// AckSelective marks the packet with the given number as acknowledged without
// removing it from the buffer (it will be removed by the next AckCumulative).
func (sb *SendBuffer) AckSelective(pktNum uint32) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if pkt, ok := sb.packets[pktNum]; ok {
		pkt.Acked = true
	}
}

// Expired returns all unacked packets whose LastSentAt is older than rto.
func (sb *SendBuffer) Expired(rto time.Duration) []*SentPacket {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	now := time.Now()
	var result []*SentPacket
	for _, pkt := range sb.packets {
		if !pkt.Acked && now.Sub(pkt.LastSentAt) > rto {
			result = append(result, pkt)
		}
	}
	return result
}

// MarkRetransmitted removes the entry for oldPktNum and creates a new entry
// for newPktNum with an incremented Retransmits counter. SentAt is preserved
// from the original packet; LastSentAt is set to now.
func (sb *SendBuffer) MarkRetransmitted(oldPktNum, newPktNum uint32, newRaw []byte) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	old, ok := sb.packets[oldPktNum]
	if !ok {
		return
	}
	retransmits := old.Retransmits + 1
	sentAt := old.SentAt
	streamID := old.StreamID
	seq := old.Seq
	payload := old.Payload
	msgType := old.MsgType
	delete(sb.packets, oldPktNum)
	sb.packets[newPktNum] = &SentPacket{
		PktNum:      newPktNum,
		RawData:     newRaw,
		MsgType:     msgType,
		StreamID:    streamID,
		Seq:         seq,
		Payload:     payload,
		SentAt:      sentAt,
		LastSentAt:  time.Now(),
		Retransmits: retransmits,
	}
}

// IsMaxRetransmits reports whether the packet has reached the retransmit limit.
func (sb *SendBuffer) IsMaxRetransmits(pktNum uint32) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	pkt, ok := sb.packets[pktNum]
	if !ok {
		return false
	}
	return pkt.Retransmits >= maxRetransmits
}

// Len returns the number of packets currently in the buffer.
func (sb *SendBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return len(sb.packets)
}

// IsFull reports whether the buffer has reached its maximum capacity.
func (sb *SendBuffer) IsFull() bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return len(sb.packets) >= sb.maxSize
}

// FirstUnacked returns the SentPacket with the lowest PktNum that is not yet
// acknowledged, or nil if all packets are acknowledged (or the buffer is empty).
func (sb *SendBuffer) FirstUnacked() *SentPacket {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	var first *SentPacket
	for _, pkt := range sb.packets {
		if pkt.Acked {
			continue
		}
		if first == nil || pkt.PktNum < first.PktNum {
			first = pkt
		}
	}
	return first
}
