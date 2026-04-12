package udp

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"

	"proxyness/pkg/auth"
)

// GenerateEphemeralKey generates an X25519 ephemeral keypair.
func GenerateEphemeralKey() (*ecdh.PrivateKey, []byte, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return priv, priv.PublicKey().Bytes(), nil
}

// DeriveSessionKey performs ECDH and derives a 32-byte session key via HKDF.
func DeriveSessionKey(priv *ecdh.PrivateKey, peerPub []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("parse peer public key: %w", err)
	}

	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}

	hk := hkdf.New(sha256.New, shared, nil, []byte("proxyness-udp-session"))
	sessionKey := make([]byte, 32)
	if _, err := io.ReadFull(hk, sessionKey); err != nil {
		return nil, err
	}

	return sessionKey, nil
}

// HandshakeRequest is sent by the client to establish a UDP session.
type HandshakeRequest struct {
	EphemeralPub []byte   // 32 bytes X25519 public key
	DeviceKey    string   // hex-encoded device key (for HMAC auth)
	MachineID    [16]byte // hardware fingerprint
}

// Encode serializes HandshakeRequest: pubkey(32) + auth(41) + machineID(16) = 89 bytes.
func (r *HandshakeRequest) Encode() ([]byte, error) {
	authMsg, err := auth.CreateAuthMessage(r.DeviceKey)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 32+len(authMsg)+16)
	copy(buf[0:32], r.EphemeralPub)
	copy(buf[32:32+len(authMsg)], authMsg)
	copy(buf[32+len(authMsg):], r.MachineID[:])

	return buf, nil
}

// DecodeHandshakeRequest parses a HandshakeRequest from raw bytes.
func DecodeHandshakeRequest(data []byte) (*HandshakeRequest, error) {
	if len(data) < 32+41+16 {
		return nil, fmt.Errorf("handshake request too short: %d", len(data))
	}

	return &HandshakeRequest{
		EphemeralPub: data[0:32],
		DeviceKey:    "", // server validates auth via ValidateAuthMessageMulti
		MachineID:    [16]byte(data[73:89]),
	}, nil
}

// RawAuth returns the 41-byte auth message from encoded request.
func RawAuth(encoded []byte) []byte {
	return encoded[32:73]
}

// HandshakeResponse is sent by the server after successful auth.
type HandshakeResponse struct {
	EphemeralPub []byte // 32 bytes X25519 public key
	SessionToken uint32 // becomes Connection ID for all future packets
}

// Encode serializes HandshakeResponse: pubkey(32) + token(4) = 36 bytes.
func (r *HandshakeResponse) Encode() []byte {
	buf := make([]byte, 36)
	copy(buf[0:32], r.EphemeralPub)
	binary.BigEndian.PutUint32(buf[32:36], r.SessionToken)
	return buf
}

// DecodeHandshakeResponse parses a HandshakeResponse from raw bytes.
func DecodeHandshakeResponse(data []byte) (*HandshakeResponse, error) {
	if len(data) < 36 {
		return nil, fmt.Errorf("handshake response too short: %d", len(data))
	}

	return &HandshakeResponse{
		EphemeralPub: data[0:32],
		SessionToken: binary.BigEndian.Uint32(data[32:36]),
	}, nil
}
