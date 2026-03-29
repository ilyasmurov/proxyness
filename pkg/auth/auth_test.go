package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"
)

func TestGenerateKey(t *testing.T) {
	key := GenerateKey()
	if len(key) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(key))
	}
	if _, err := hex.DecodeString(key); err != nil {
		t.Fatalf("key is not valid hex: %v", err)
	}

	key2 := GenerateKey()
	if key == key2 {
		t.Fatal("two generated keys should not be equal")
	}
}

func TestCreateAuthMessage(t *testing.T) {
	key := GenerateKey()
	msg, err := CreateAuthMessage(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg) != AuthMsgLen {
		t.Fatalf("expected %d bytes, got %d", AuthMsgLen, len(msg))
	}
	if msg[0] != Version {
		t.Fatalf("expected version %d, got %d", Version, msg[0])
	}
}

func TestValidateAuthMessage_Valid(t *testing.T) {
	key := GenerateKey()
	msg, err := CreateAuthMessage(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuthMessage(key, msg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateAuthMessage_WrongKey(t *testing.T) {
	key1 := GenerateKey()
	key2 := GenerateKey()
	msg, _ := CreateAuthMessage(key1)
	if err := ValidateAuthMessage(key2, msg); err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestValidateAuthMessage_ExpiredTimestamp(t *testing.T) {
	key := GenerateKey()
	keyBytes, _ := hex.DecodeString(key)

	msg := make([]byte, AuthMsgLen)
	msg[0] = Version
	ts := uint64(time.Now().Unix()) - 60
	binary.BigEndian.PutUint64(msg[1:9], ts)

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write(msg[1:9])
	copy(msg[9:], mac.Sum(nil))

	if err := ValidateAuthMessage(key, msg); err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

func TestValidateAuthMessage_BadLength(t *testing.T) {
	key := GenerateKey()
	if err := ValidateAuthMessage(key, []byte{0x01}); err == nil {
		t.Fatal("expected error for short message")
	}
}

func TestValidateAuthMessage_BadVersion(t *testing.T) {
	key := GenerateKey()
	msg, _ := CreateAuthMessage(key)
	msg[0] = 0xFF
	if err := ValidateAuthMessage(key, msg); err == nil {
		t.Fatal("expected error for wrong version")
	}
}
