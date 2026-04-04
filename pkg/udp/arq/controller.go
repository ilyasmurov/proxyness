package arq

import (
	"fmt"
	"sync"
	"sync/atomic"

	pkgudp "smurov-proxy/pkg/udp"
)

const (
	sendBufSize = 1024
	recvBufSize = 512
)

// Controller ties together SendBuffer, RecvBuffer, CongestionControl,
// RTTEstimator and AckState into a single ARQ session manager.
type Controller struct {
	connID     uint32
	sessionKey []byte

	sendBuf    *SendBuffer
	nextPktNum atomic.Uint32
	cwnd       *CongestionControl
	rtt        *RTTEstimator

	recvMu   sync.Mutex
	recvBufs map[uint32]*RecvBuffer
	ackState *AckState

	sendFn    func([]byte) error
	deliverFn func(streamID uint32, data []byte)

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// New creates a new Controller for the given connection.
// sendFn is called whenever a datagram needs to be transmitted.
// deliverFn is called in-order for each data payload delivered to a stream.
func New(connID uint32, sessionKey []byte, sendFn func([]byte) error, deliverFn func(streamID uint32, data []byte)) *Controller {
	return &Controller{
		connID:     connID,
		sessionKey: sessionKey,
		sendBuf:    NewSendBuffer(sendBufSize),
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

	if c.ackState.DupCount() >= 3 {
		c.fastRetransmit()
	}
}

// RetransmitTick checks for timed-out packets and retransmits them.
// It should be called periodically (e.g. from a ticker goroutine).
func (c *Controller) RetransmitTick() {
	rto := c.rtt.RTO()
	expired := c.sendBuf.Expired(rto)

	for _, p := range expired {
		if c.sendBuf.IsMaxRetransmits(p.PktNum) {
			continue
		}

		newPktNum := c.nextPktNum.Add(1)
		newPkt := &pkgudp.Packet{
			ConnID:   c.connID,
			Type:     p.MsgType,
			PktNum:   newPktNum,
			StreamID: p.StreamID,
			Seq:      p.Seq,
			Data:     p.Payload,
		}

		encoded, err := pkgudp.EncodePacket(newPkt, c.sessionKey)
		if err != nil {
			continue
		}

		c.sendBuf.MarkRetransmitted(p.PktNum, newPktNum, encoded)
		c.sendFn(encoded) //nolint:errcheck
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

	newPktNum := c.nextPktNum.Add(1)
	newPkt := &pkgudp.Packet{
		ConnID:   c.connID,
		Type:     p.MsgType,
		PktNum:   newPktNum,
		StreamID: p.StreamID,
		Seq:      p.Seq,
		Data:     p.Payload,
	}

	encoded, err := pkgudp.EncodePacket(newPkt, c.sessionKey)
	if err != nil {
		return
	}

	c.sendBuf.MarkRetransmitted(p.PktNum, newPktNum, encoded)
	c.sendFn(encoded) //nolint:errcheck
	c.cwnd.OnLoss()
}

// CreateRecvBuffer creates a receive buffer for the given stream ID.
// Delivered payloads are forwarded to the Controller's deliverFn in order.
func (c *Controller) CreateRecvBuffer(streamID uint32) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	c.recvBufs[streamID] = NewRecvBuffer(recvBufSize, func(seq uint32, data []byte) {
		c.deliverFn(streamID, data)
	})
}

// RemoveRecvBuffer removes the receive buffer for the given stream ID.
func (c *Controller) RemoveRecvBuffer(streamID uint32) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	delete(c.recvBufs, streamID)
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
