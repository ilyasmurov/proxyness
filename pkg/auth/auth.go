package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	Version      = 0x01
	AuthMsgLen   = 41 // 1 version + 8 timestamp + 32 HMAC
	MaxClockSkew = 30 // seconds
)

// GenerateKey returns a random 256-bit key as a hex string.
func GenerateKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return hex.EncodeToString(key)
}

// CreateAuthMessage builds a 41-byte auth message: version + timestamp + HMAC.
func CreateAuthMessage(key string) ([]byte, error) {
	keyBytes, err := hex.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	msg := make([]byte, AuthMsgLen)
	msg[0] = Version
	binary.BigEndian.PutUint64(msg[1:9], uint64(time.Now().Unix()))

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write(msg[1:9])
	copy(msg[9:], mac.Sum(nil))

	return msg, nil
}

// ValidateAuthMessage checks version, timestamp freshness, and HMAC.
func ValidateAuthMessage(key string, msg []byte) error {
	if len(msg) != AuthMsgLen {
		return fmt.Errorf("invalid message length: %d", len(msg))
	}
	if msg[0] != Version {
		return fmt.Errorf("unsupported version: %d", msg[0])
	}

	keyBytes, err := hex.DecodeString(key)
	if err != nil {
		return fmt.Errorf("invalid key: %w", err)
	}

	ts := binary.BigEndian.Uint64(msg[1:9])
	now := uint64(time.Now().Unix())
	diff := int64(now) - int64(ts)
	if diff < 0 {
		diff = -diff
	}
	if diff > MaxClockSkew {
		return fmt.Errorf("timestamp expired: %d seconds skew", diff)
	}

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write(msg[1:9])
	expected := mac.Sum(nil)

	if !hmac.Equal(msg[9:], expected) {
		return fmt.Errorf("HMAC mismatch")
	}

	return nil
}
