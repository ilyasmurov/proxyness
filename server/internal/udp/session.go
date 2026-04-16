package udp

import (
	"crypto/rand"
	"encoding/binary"
	"log"
	"net"
	"sync"
	"time"

	"proxyness/server/internal/stats"
)

// Session represents an authenticated UDP client.
type Session struct {
	Token      uint32
	SessionKey []byte
	DeviceID   int
	TrackerID  int64 // stats.Tracker connection ID
	ClientAddr net.Addr
	LastSeen   time.Time

	mu      sync.Mutex
	streams map[uint32]*StreamState
	nextSID uint32
}

// sendBufSize is the number of slots in the per-stream retransmit ring buffer.
// 512 × ~1400 bytes ≈ 700 KB per active stream. Packets older than 512 sends
// are unrecoverable via NACK; the upper TCP will retransmit at that point.
const sendBufSize = 512

// SendBuf is a ring buffer that stores recently-sent packets for NACK retransmit.
type SendBuf struct {
	ring [sendBufSize]*SentPacket
	seq  uint32 // next sequence number to assign (starts at 1)
}

// SentPacket holds a pre-encoded packet for fast retransmit.
type SentPacket struct {
	Seq uint32
	Raw []byte // pre-encoded wire bytes, ready to WriteTo
}

// Next assigns and returns the next sequence number.
func (sb *SendBuf) Next() uint32 {
	sb.seq++
	return sb.seq
}

// Store saves a sent packet in the ring buffer.
func (sb *SendBuf) Store(seq uint32, raw []byte) {
	sb.ring[seq%sendBufSize] = &SentPacket{Seq: seq, Raw: raw}
}

// Get retrieves a stored packet by seq, or nil if evicted/missing.
func (sb *SendBuf) Get(seq uint32) *SentPacket {
	p := sb.ring[seq%sendBufSize]
	if p != nil && p.Seq == seq {
		return p
	}
	return nil
}

// StreamState tracks one proxied stream within a session.
type StreamState struct {
	Type     byte // 0x01=TCP, 0x02=UDP
	Addr     string
	Port     uint16
	Conn     net.Conn // outbound connection to destination
	WriteCh  chan []byte
	Dialing  bool // true while handleStreamOpen is dialing
	BytesIn  int64
	BytesOut int64
	Created  time.Time
	SendBuf  SendBuf // ring buffer for NACK-based retransmit
}

func (s *Session) AddStream() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSID++
	id := s.nextSID
	s.streams[id] = &StreamState{Created: time.Now()}
	return id
}

func (s *Session) GetStream(id uint32) (*StreamState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[id]
	return st, ok
}

func (s *Session) RemoveStream(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.streams[id]; ok {
		if st.WriteCh != nil {
			close(st.WriteCh)
			st.WriteCh = nil
		}
		if st.Conn != nil {
			st.Conn.Close()
		}
		delete(s.streams, id)
	}
}

func (s *Session) CloseAllStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range s.streams {
		if st.WriteCh != nil {
			close(st.WriteCh)
			st.WriteCh = nil
		}
		if st.Conn != nil {
			st.Conn.Close()
		}
		delete(s.streams, id)
	}
}

// SessionManager manages all active UDP sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[uint32]*Session
	tracker  *stats.Tracker
}

func NewSessionManager(tracker *stats.Tracker) *SessionManager {
	return &SessionManager{
		sessions: make(map[uint32]*Session),
		tracker:  tracker,
	}
}

func (m *SessionManager) Create(sessionKey []byte, deviceID int) uint32 {
	var token uint32
	buf := make([]byte, 4)
	for token == 0 {
		rand.Read(buf)
		token = binary.BigEndian.Uint32(buf)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[token] = &Session{
		Token:      token,
		SessionKey: sessionKey,
		DeviceID:   deviceID,
		LastSeen:   time.Now(),
		streams:    make(map[uint32]*StreamState),
	}

	return token
}

func (m *SessionManager) Get(token uint32) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[token]
	return s, ok
}

func (m *SessionManager) Remove(token uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[token]; ok {
		if s.TrackerID != 0 && m.tracker != nil {
			m.tracker.Remove(s.TrackerID)
		}
		s.CloseAllStreams()
		delete(m.sessions, token)
	}
}

// Snapshot returns a shallow copy of all currently-active sessions.
// Used by graceful shutdown to broadcast session-close to every client
// without holding the manager lock while doing I/O.
func (m *SessionManager) Snapshot() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Cleanup removes sessions older than maxAge.
func (m *SessionManager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for token, s := range m.sessions {
		if now.Sub(s.LastSeen) > maxAge {
			log.Printf("udp: cleanup session token=%d lastSeen=%v ago streams=%d", token, now.Sub(s.LastSeen).Round(time.Second), len(s.streams))
			if s.TrackerID != 0 && m.tracker != nil {
				m.tracker.Remove(s.TrackerID)
			}
			s.CloseAllStreams()
			delete(m.sessions, token)
		}
	}
}
