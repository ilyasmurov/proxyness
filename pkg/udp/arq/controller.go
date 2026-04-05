package arq

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	pkgudp "smurov-proxy/pkg/udp"
)

const (
	DefaultSendBufSize = 1024
	DefaultRecvBufSize = 512
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

	recvMu   sync.Mutex
	recvBufs map[uint32]*RecvBuffer
	ackState *AckState

	lastAckCumAck uint32
	dupAckCount   int

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
	if !c.cwnd.WaitForSlot(c.done) {
		return fmt.Errorf("controller closed")
	}

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
	c.sendBuf.Add(pktNum, encoded, msgType, streamID, seq, payload)
	c.cwnd.OnSend()

	if err := c.sendFn(encoded); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	return nil
}

// HandleData processes an incoming data packet: updates ACK state and delivers
// the payload to the appropriate receive buffer (if one exists for the stream).
func (c *Controller) HandleData(pkt *pkgudp.Packet) {
	if pkt.PktNum > 0 {
		c.ackState.RecordReceived(pkt.PktNum)
	}

	c.recvMu.Lock()
	rb, ok := c.recvBufs[pkt.StreamID]
	c.recvMu.Unlock()

	if ok {
		rb.Insert(pkt.Seq, pkt.Data)
	}

	// Send immediate ACK only on gap detection (out-of-order arrival).
	// This is rare and critical for fast loss recovery.
	// Delayed ACKs (every 2nd packet) are handled by ackLoop only —
	// sending them here would block recvLoop on conn.Write().
	if c.ackState.NeedsImmediateAck() {
		c.sendAck()
	}
}

// HandleAck processes an incoming ACK datagram, removing acknowledged packets
// from the send buffer and signalling the congestion window.
func (c *Controller) HandleAck(data []byte) {
	ack, err := DecodeAckData(data)
	if err != nil {
		return
	}

	// Sample RTT from the cumAck packet (Karn's algorithm: skip retransmits)
	if p := c.sendBuf.Get(ack.CumAck); p != nil && !p.IsRetransmit() {
		c.rtt.Update(time.Since(p.SentAt))
	}

	// Process cumulative ACK
	acked := c.sendBuf.AckCumulative(ack.CumAck)

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

	if acked > 0 {
		c.cwnd.OnAck(acked)
	}

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

// RetransmitTick checks for timed-out packets and retransmits them.
// It should be called periodically (e.g. from a ticker goroutine).
func (c *Controller) RetransmitTick() {
	rto := c.rtt.RTO()
	expired := c.sendBuf.Expired(rto)

	if len(expired) == 0 {
		return
	}

	retransmitted := false
	for _, p := range expired {
		if c.sendBuf.IsMaxRetransmits(p.PktNum) {
			// Drop the packet and release the cwnd slot (without growing cwnd)
			if c.sendBuf.Drop(p.PktNum) {
				c.cwnd.OnDrop(1)
			}
			continue
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
		retransmitted = true
	}

	// Signal loss only if packets were actually retransmitted (not just dropped)
	if retransmitted {
		c.rtt.Backoff()
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
	c.cwnd.OnLoss()
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

// CwndStats returns congestion window diagnostics (cwnd, inFlight, slots).
func (c *Controller) CwndStats() (cwnd int, inFlight int, slots int) {
	return c.cwnd.Stats()
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
