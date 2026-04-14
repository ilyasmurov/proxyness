package arq

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	pkgudp "proxyness/pkg/udp"
)

const (
	DefaultSendBufSize = 2048
	DefaultRecvBufSize = 1024
	DefaultMaxStreams  = 256
)

// Config holds tunable parameters for the ARQ controller.
type Config struct {
	SendBufSize int // max packets in send buffer (default 1024)
	RecvBufSize int // max out-of-order packets per stream (default 512)
	MaxStreams  int // max concurrent receive streams per connection (default 256)
}

// DefaultConfig returns the default ARQ configuration.
func DefaultConfig() Config {
	return Config{
		SendBufSize: DefaultSendBufSize,
		RecvBufSize: DefaultRecvBufSize,
		MaxStreams:  DefaultMaxStreams,
	}
}

// Controller ties together SendBuffer, RecvBuffer, CongestionControl,
// RTTEstimator and AckState into a single ARQ session manager.
type Controller struct {
	connID     uint32
	sessionKey []byte
	cfg        Config

	sendBuf    *SendBuffer
	nextPktNum atomic.Uint32
	cwnd       *CongestionControl
	rtt        *RTTEstimator
	pacer      *Pacer

	recvMu   sync.Mutex
	recvBufs map[uint32]*RecvBuffer
	ackState *AckState

	lastAckCumAck    uint32
	dupAckCount      int
	lastImmediateAck time.Time // rate-limit immediate ACKs from recvLoop

	sendFn    func([]byte) error
	deliverFn func(streamID uint32, data []byte)

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// New creates a new Controller for the given connection with default config.
// sendFn is called whenever a datagram needs to be transmitted.
// deliverFn is called in-order for each data payload delivered to a stream.
func New(connID uint32, sessionKey []byte, sendFn func([]byte) error, deliverFn func(streamID uint32, data []byte)) *Controller {
	return NewWithConfig(connID, sessionKey, sendFn, deliverFn, DefaultConfig())
}

// NewWithConfig creates a new Controller with the given configuration.
func NewWithConfig(connID uint32, sessionKey []byte, sendFn func([]byte) error, deliverFn func(streamID uint32, data []byte), cfg Config) *Controller {
	return &Controller{
		connID:     connID,
		sessionKey: sessionKey,
		cfg:        cfg,
		sendBuf:    NewSendBuffer(cfg.SendBufSize),
		cwnd:       NewCongestionControl(),
		rtt:        NewRTTEstimator(),
		pacer:      NewPacer(),
		recvBufs:   make(map[uint32]*RecvBuffer),
		ackState:   NewAckState(),
		sendFn:     sendFn,
		deliverFn:  deliverFn,
		done:       make(chan struct{}),
	}
}

// Send encodes and transmits a data packet, blocking when the congestion window
// is full. Returns an error if the controller is closed or encoding fails.
func (c *Controller) Send(msgType byte, streamID, seq uint32, data []byte) error {
	if !c.cwnd.AcquireSlot(c.done) {
		return fmt.Errorf("controller closed")
	}

	// Pacing: don't pace until BWE has enough samples — early samples are too
	// noisy and would throttle the initial burst needed for accurate estimation.
	// During STARTUP, use high gain (2.885x) to double rate each RTT.
	// During steady-state, moderate gain (1.25x) to probe without bloat.
	bwe := c.cwnd.BWE()
	if bwe.IsStable() {
		gain := c.cwnd.PacingGain()
		if pacingRate := bwe.PacingRate(gain); pacingRate > 0 {
			interval := time.Duration(float64(time.Second) * float64(packetMSS) / pacingRate)
			c.pacer.Pace(interval)
		}
	}

	// Take delivery snapshot before sending
	snap := bwe.TakeSnapshot()
	pktNum := c.nextPktNum.Add(1)

	pkt := &pkgudp.Packet{
		ConnID:   c.connID,
		Type:     msgType,
		PktNum:   pktNum,
		StreamID: streamID,
		Seq:      seq,
		Data:     data,
	}

	encoded, err := pkgudp.EncodePacket(pkt, c.sessionKey)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	payload := make([]byte, len(data))
	copy(payload, data)
	c.sendBuf.Add(pktNum, encoded, msgType, streamID, seq, payload, snap)

	if err := c.sendFn(encoded); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	return nil
}

// HandleData processes an incoming data packet: updates ACK state and delivers
// the payload to the appropriate receive buffer (if one exists for the stream).
func (c *Controller) HandleData(pkt *pkgudp.Packet) {
	select {
	case <-c.done:
		return
	default:
	}

	if pkt.PktNum > 0 {
		c.ackState.RecordReceived(pkt.PktNum)
	}

	c.recvMu.Lock()
	rb, ok := c.recvBufs[pkt.StreamID]
	c.recvMu.Unlock()

	if ok {
		rb.Insert(pkt.Seq, pkt.Data)
	}

	// Send immediate ACK on gap detection (out-of-order arrival) for fast
	// loss recovery. Rate-limited to avoid ACK spam on lossy paths — every
	// OOO packet would otherwise trigger an ACK, consuming uplink bandwidth
	// and blocking the recvLoop on conn.Write().
	if c.ackState.NeedsImmediateAck() && time.Since(c.lastImmediateAck) >= 5*time.Millisecond {
		c.lastImmediateAck = time.Now()
		c.sendAck()
	}
}

// HandleAck processes an incoming ACK datagram, removing acknowledged packets
// from the send buffer, feeding the bandwidth estimator, and signalling the
// congestion window.
func (c *Controller) HandleAck(data []byte) {
	select {
	case <-c.done:
		return
	default:
	}

	ack, err := DecodeAckData(data)
	if err != nil {
		return
	}

	bwe := c.cwnd.BWE()

	// Save the delivery snapshot and RTT from the cumAck packet BEFORE
	// AckCumulative removes it. Karn's algorithm: skip retransmits for RTT.
	var rttSample time.Duration
	var rateSnap DeliverySnapshot
	var hasSnap bool
	if p := c.sendBuf.Get(ack.CumAck); p != nil && !p.IsRetransmit() {
		rttSample = time.Since(p.SentAt)
		rateSnap = p.Delivery
		hasSnap = true
		c.rtt.Update(rttSample)
	}

	// Process cumulative ACK — removes packets from buffer, returns newly-acked count
	acked := c.sendBuf.AckCumulative(ack.CumAck)

	// Process selective ACKs
	for i := uint32(0); i < 256; i++ {
		pktNum := ack.CumAck + 1 + i
		if ack.IsReceived(pktNum) {
			p := c.sendBuf.Get(pktNum)
			if p != nil && !p.Acked {
				c.sendBuf.AckSelective(pktNum)
				acked++
			}
		}
	}

	// Feed bandwidth estimator with ALL acked bytes, then take one rate sample
	if acked > 0 {
		bwe.RecordDelivered(acked * packetMSS)
		if hasSnap {
			bwe.SampleRate(rateSnap, rttSample)
		}
		c.cwnd.OnAck(acked)
	}

	// SACK-based loss detection: retransmit gap packets immediately.
	// Unlike RTO retransmit, this does NOT reduce cwnd — random drops on
	// lossy ISP paths are not congestion signals. Recovery in ~1 RTT.
	c.sackDetect(ack)

	// Sender-side duplicate ACK detection for fast retransmit
	if ack.CumAck == c.lastAckCumAck && ack.CumAck > 0 {
		c.dupAckCount++
		if c.dupAckCount >= 3 {
			c.fastRetransmit()
			c.dupAckCount = 0
		}
	} else {
		c.lastAckCumAck = ack.CumAck
		c.dupAckCount = 0
	}
}

// maxRetransmitBatch limits how many packets can be retransmitted per tick
// to prevent retransmit bursts that ISPs drop, causing cascading loss.
const maxRetransmitBatch = 32

// RetransmitTick checks for timed-out packets and retransmits them.
// It should be called periodically (e.g. from a ticker goroutine).
func (c *Controller) RetransmitTick() {
	rto := c.rtt.RTO()
	expired := c.sendBuf.Expired(rto)

	if len(expired) == 0 {
		return
	}

	// Limit retransmit batch to prevent burst that ISPs drop.
	if len(expired) > maxRetransmitBatch {
		expired = expired[:maxRetransmitBatch]
	}

	newLoss := false
	for _, p := range expired {
		if c.sendBuf.IsMaxRetransmits(p.PktNum) {
			// Drop the packet and release the cwnd slot (without growing cwnd)
			if c.sendBuf.Drop(p.PktNum) {
				c.cwnd.OnDrop(1)
			}
			continue
		}

		// Track whether this is a fresh loss (first retransmit) vs re-retransmit.
		// Only fresh losses warrant cwnd reduction — re-retransmits of the same
		// packet are just recovery attempts, not new congestion signals.
		if p.Retransmits == 0 {
			newLoss = true
		}

		// Retransmit with the SAME PktNum so the receiver fills the original
		// gap in its cumulative ACK sequence. Re-encode to get a fresh nonce.
		retxPkt := &pkgudp.Packet{
			ConnID:   c.connID,
			Type:     p.MsgType,
			PktNum:   p.PktNum,
			StreamID: p.StreamID,
			Seq:      p.Seq,
			Data:     p.Payload,
		}

		encoded, err := pkgudp.EncodePacket(retxPkt, c.sessionKey)
		if err != nil {
			continue
		}

		c.sendBuf.MarkResent(p.PktNum, encoded)
		c.sendFn(encoded) //nolint:errcheck
	}

	if newLoss {
		c.rtt.Backoff()
		// OnLoss is a no-op in rate-based CC — cwnd is driven by delivery rate,
		// not loss events. We still backoff RTO to avoid retransmit storms.
		c.cwnd.OnLoss()
	}
}

// AckTick sends a delayed ACK if enough packets have accumulated since the last
// ACK was sent. Should be called periodically.
func (c *Controller) AckTick() {
	if c.ackState.NeedsDelayedAck() || c.ackState.NeedsImmediateAck() {
		c.sendAck()
	}
}

// sendAck constructs and transmits an ACK packet from the current ack state.
func (c *Controller) sendAck() {
	ack := c.ackState.BuildAck()
	c.ackState.AckSent()

	pkt := &pkgudp.Packet{
		ConnID: c.connID,
		Type:   pkgudp.MsgAck,
		PktNum: 0,
		Data:   ack.Encode(),
	}

	encoded, err := pkgudp.EncodePacket(pkt, c.sessionKey)
	if err != nil {
		return
	}

	c.sendFn(encoded) //nolint:errcheck
}

// fastRetransmit immediately retransmits the lowest unacknowledged packet
// (triggered by 3 duplicate ACKs).
func (c *Controller) fastRetransmit() {
	p := c.sendBuf.FirstUnacked()
	if p == nil {
		return
	}

	// Retransmit with the SAME PktNum so the receiver fills the original gap.
	retxPkt := &pkgudp.Packet{
		ConnID:   c.connID,
		Type:     p.MsgType,
		PktNum:   p.PktNum,
		StreamID: p.StreamID,
		Seq:      p.Seq,
		Data:     p.Payload,
	}

	encoded, err := pkgudp.EncodePacket(retxPkt, c.sessionKey)
	if err != nil {
		return
	}

	c.sendBuf.MarkResent(p.PktNum, encoded)
	c.sendFn(encoded) //nolint:errcheck
	// No cwnd reduction — on lossy ISP paths, dup ACKs indicate random
	// packet drops, not congestion. Pacing prevents burst-induced drops.
}

// maxSackRetransmit limits SACK-triggered retransmits per ACK to prevent storms.
const maxSackRetransmit = 16

// sackDetect uses SACK bitmap gaps to detect lost packets and retransmit
// immediately without cwnd reduction. If 3+ packets after a gap are SACKed,
// the gap packet is considered lost.
func (c *Controller) sackDetect(ack *AckData) {
	// Find highest SACKed packet.
	var highestSacked uint32
	for i := uint32(255); ; i-- {
		if ack.IsReceived(ack.CumAck + 1 + i) {
			highestSacked = ack.CumAck + 1 + i
			break
		}
		if i == 0 {
			break
		}
	}
	if highestSacked == 0 {
		return
	}

	minInterval := c.rtt.RTO() / 2
	if minInterval < 20*time.Millisecond {
		minInterval = 20 * time.Millisecond
	}

	retxCount := 0
	for pktNum := ack.CumAck + 1; pktNum < highestSacked && retxCount < maxSackRetransmit; pktNum++ {
		if ack.IsReceived(pktNum) {
			continue
		}

		// Count SACKed packets after this gap.
		laterAcked := 0
		for j := pktNum + 1; j <= highestSacked && j <= ack.CumAck+256; j++ {
			if ack.IsReceived(j) {
				laterAcked++
				if laterAcked >= 3 {
					break
				}
			}
		}
		if laterAcked < 3 {
			continue
		}

		p := c.sendBuf.Get(pktNum)
		if p == nil || p.Acked {
			continue
		}

		// Skip if recently sent to avoid retransmit storm.
		if time.Since(p.LastSentAt) < minInterval {
			continue
		}

		retxPkt := &pkgudp.Packet{
			ConnID:   c.connID,
			Type:     p.MsgType,
			PktNum:   p.PktNum,
			StreamID: p.StreamID,
			Seq:      p.Seq,
			Data:     p.Payload,
		}
		encoded, err := pkgudp.EncodePacket(retxPkt, c.sessionKey)
		if err != nil {
			continue
		}
		c.sendBuf.MarkResent(p.PktNum, encoded)
		c.sendFn(encoded) //nolint:errcheck
		retxCount++
	}
}

// CreateRecvBuffer creates a receive buffer for the given stream ID.
// Returns an error if the maximum number of concurrent streams is exceeded.
// Delivered payloads are forwarded to the Controller's deliverFn in order.
func (c *Controller) CreateRecvBuffer(streamID uint32) error {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	if _, exists := c.recvBufs[streamID]; !exists && len(c.recvBufs) >= c.cfg.MaxStreams {
		return fmt.Errorf("max streams limit reached (%d)", c.cfg.MaxStreams)
	}
	c.recvBufs[streamID] = NewRecvBuffer(c.cfg.RecvBufSize, func(seq uint32, data []byte) {
		c.deliverFn(streamID, data)
	})
	return nil
}

// RemoveRecvBuffer removes the receive buffer for the given stream ID.
func (c *Controller) RemoveRecvBuffer(streamID uint32) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	delete(c.recvBufs, streamID)
}

// RecordPktNum records a packet number for ACK generation without delivering
// the payload. Use this for control messages (stream open/close) that are sent
// through ARQ but handled outside the normal data delivery path.
func (c *Controller) RecordPktNum(pktNum uint32) {
	if pktNum > 0 {
		c.ackState.RecordReceived(pktNum)
		// ACKs sent by ackLoop — never call sendAck here (blocks receive path)
	}
}

// InStartup reports whether the connection is in STARTUP phase.
func (c *Controller) InStartup() bool {
	return c.cwnd.InStartup()
}

// InProbeRTT reports whether the connection is in the ProbeRTT drain phase.
func (c *Controller) InProbeRTT() bool {
	return c.cwnd.InProbeRTT()
}

// BWEStable reports whether the BWE has enough samples.
func (c *Controller) BWEStable() bool {
	return c.cwnd.BWE().IsStable()
}

// CwndStats returns congestion window diagnostics (cwnd, inFlight, slots, losses).
func (c *Controller) CwndStats() (cwnd int, inFlight int, slots int, losses int) {
	return c.cwnd.Stats()
}

// BWEStats returns bandwidth estimator diagnostics (maxBW bytes/s, minRTT, BDP bytes).
func (c *Controller) BWEStats() (maxBW float64, minRTT time.Duration, bdp float64) {
	return c.cwnd.BWE().Stats()
}

// RTOMillis returns the current retransmission timeout in milliseconds.
func (c *Controller) RTOMillis() int64 {
	return c.rtt.RTO().Milliseconds()
}

// SendBufLen returns the number of unacked packets in the send buffer.
func (c *Controller) SendBufLen() int {
	return c.sendBuf.Len()
}

// Done returns a channel that is closed when the controller is shut down.
func (c *Controller) Done() <-chan struct{} {
	return c.done
}

// Close shuts down the controller, unblocking any goroutines waiting in Send.
func (c *Controller) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	c.mu.Unlock()
	c.cwnd.SignalAll()
}
