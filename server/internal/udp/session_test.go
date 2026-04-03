package udp

import (
	"crypto/rand"
	"testing"
	"time"
)

func TestSessionManagerCreateAndLookup(t *testing.T) {
	sm := NewSessionManager()

	key := make([]byte, 32)
	rand.Read(key)

	token := sm.Create(key, 1)
	if token == 0 {
		t.Fatal("token should not be 0")
	}

	sess, ok := sm.Get(token)
	if !ok {
		t.Fatal("session not found")
	}

	if sess.DeviceID != 1 {
		t.Fatalf("deviceID: got %d", sess.DeviceID)
	}
}

func TestSessionManagerExpiry(t *testing.T) {
	sm := NewSessionManager()

	key := make([]byte, 32)
	rand.Read(key)

	token := sm.Create(key, 1)

	// Manually expire
	sm.mu.Lock()
	sm.sessions[token].LastSeen = time.Now().Add(-3 * time.Minute)
	sm.mu.Unlock()

	sm.Cleanup(2 * time.Minute)

	_, ok := sm.Get(token)
	if ok {
		t.Fatal("expired session should be removed")
	}
}

func TestSessionManagerOpenCloseStream(t *testing.T) {
	sm := NewSessionManager()

	key := make([]byte, 32)
	rand.Read(key)

	token := sm.Create(key, 1)
	sess, _ := sm.Get(token)

	streamID := sess.AddStream()
	if streamID == 0 {
		t.Fatal("streamID should not be 0")
	}

	st, ok := sess.GetStream(streamID)
	if !ok || st == nil {
		t.Fatal("stream not found")
	}

	sess.RemoveStream(streamID)
	_, ok = sess.GetStream(streamID)
	if ok {
		t.Fatal("stream should be removed")
	}
}
