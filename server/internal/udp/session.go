package udp

import (
	"crypto/rand"
	"encoding/binary"
	"log"
	"net"
	"sync"
	"time"

	"smurov-proxy/pkg/udp/arq"
)

// Session represents an authenticated UDP client.
type Session struct {
	Token      uint32
	SessionKey []byte
	DeviceID   int
	ClientAddr net.Addr
	LastSeen   time.Time

	ARQ *arq.Controller

	mu      sync.Mutex
	streams map[uint32]*StreamState
	nextSID uint32
	nextSeq map[uint32]*uint32 // per-stream sequence counter for server→client
}

// StreamState tracks one proxied stream within a session.
type StreamState struct {
	Type     byte // 0x01=TCP, 0x02=UDP
	Addr     string
	Port     uint16
	Conn     net.Conn // outbound connection to destination
	BytesIn  int64
	BytesOut int64
	Created  time.Time
}

func (s *Session) AddStream() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSID++
	id := s.nextSID
	s.streams[id] = &StreamState{Created: time.Now()}
	seq := uint32(0)
	s.nextSeq[id] = &seq
	return id
}

func (s *Session) GetStream(id uint32) (*StreamState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[id]
	return st, ok
}

func (s *Session) NextSeq(streamID uint32) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := s.nextSeq[streamID]
	if !ok {
		return 0
	}
	v := *seq
	*seq++
	return v
}

func (s *Session) RemoveStream(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.streams[id]; ok {
		if st.Conn != nil {
			st.Conn.Close()
		}
		delete(s.streams, id)
		delete(s.nextSeq, id)
	}
	if s.ARQ != nil {
		s.ARQ.RemoveRecvBuffer(id)
	}
}

func (s *Session) CloseAllStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range s.streams {
		if st.Conn != nil {
			st.Conn.Close()
		}
		delete(s.streams, id)
	}
	s.nextSeq = make(map[uint32]*uint32)
	if s.ARQ != nil {
		s.ARQ.Close()
		s.ARQ = nil
	}
}

// SessionManager manages all active UDP sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[uint32]*Session
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[uint32]*Session),
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
		nextSeq:    make(map[uint32]*uint32),
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
		s.CloseAllStreams()
		delete(m.sessions, token)
	}
}

// Cleanup removes sessions older than maxAge.
func (m *SessionManager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for token, s := range m.sessions {
		if now.Sub(s.LastSeen) > maxAge {
			log.Printf("udp: cleanup session token=%d lastSeen=%v ago streams=%d", token, now.Sub(s.LastSeen).Round(time.Second), len(s.streams))
			s.CloseAllStreams()
			delete(m.sessions, token)
		}
	}
}
