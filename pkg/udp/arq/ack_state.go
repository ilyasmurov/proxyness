package arq

import "sync"

const delayedAckThreshold = 2

// AckState tracks received packet numbers on the receiver side and builds
// AckData for sending cumulative + selective ACKs back to the sender.
type AckState struct {
	mu           sync.Mutex
	received     map[uint32]bool
	cumAck       uint32
	pktsSinceAck int
	immediateAck bool
	dupCount     int
}

// NewAckState returns a fresh AckState with cumAck=0 (nothing received yet).
func NewAckState() *AckState {
	return &AckState{
		received: make(map[uint32]bool),
	}
}

// RecordReceived registers that pktNum has arrived.
func (as *AckState) RecordReceived(pktNum uint32) {
	as.mu.Lock()
	defer as.mu.Unlock()

	// Already covered by the cumulative ACK — duplicate, ignore.
	if pktNum <= as.cumAck {
		return
	}

	as.received[pktNum] = true
	as.pktsSinceAck++

	// Advance cumAck as far as possible through consecutive received packets.
	prevCumAck := as.cumAck
	for as.received[as.cumAck+1] {
		as.cumAck++
		delete(as.received, as.cumAck)
	}

	if pktNum > as.cumAck {
		// There is still a gap ahead of pktNum — request immediate ACK.
		as.immediateAck = true
		// Count out-of-order (above cumAck) packets for duplicate ACK tracking.
		as.dupCount++
	} else if as.cumAck > prevCumAck {
		// cumAck advanced — gap was filled; reset dup count.
		as.dupCount = 0
	}
}

// BuildAck constructs an AckData snapshot from the current state.
func (as *AckState) BuildAck() *AckData {
	as.mu.Lock()
	defer as.mu.Unlock()

	ack := &AckData{CumAck: as.cumAck}
	for pktNum := range as.received {
		ack.SetReceived(pktNum)
	}
	return ack
}

// NeedsImmediateAck reports whether a gap has been detected since the last
// AckSent call, meaning an ACK should be sent without delay.
func (as *AckState) NeedsImmediateAck() bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.immediateAck
}

// NeedsDelayedAck reports whether enough packets have accumulated since the
// last AckSent call to warrant sending a delayed ACK.
func (as *AckState) NeedsDelayedAck() bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.pktsSinceAck >= delayedAckThreshold
}

// AckSent resets the per-ACK counters after an ACK has been sent.
func (as *AckState) AckSent() {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.pktsSinceAck = 0
	as.immediateAck = false
}

// DupCount returns the number of out-of-order packets received above the
// current cumAck (resets to 0 when a gap is filled).
func (as *AckState) DupCount() int {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.dupCount
}

// CumAck returns the current cumulative ACK value.
func (as *AckState) CumAck() uint32 {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.cumAck
}
