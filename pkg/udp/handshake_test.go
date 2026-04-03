package udp

import (
	"testing"
)

func TestHandshakeKeyExchange(t *testing.T) {
	clientPriv, clientPub, err := GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}
	serverPriv, serverPub, err := GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}

	clientSessionKey, err := DeriveSessionKey(clientPriv, serverPub)
	if err != nil {
		t.Fatal(err)
	}
	serverSessionKey, err := DeriveSessionKey(serverPriv, clientPub)
	if err != nil {
		t.Fatal(err)
	}

	if len(clientSessionKey) != 32 {
		t.Fatalf("key length: %d", len(clientSessionKey))
	}
	for i := range clientSessionKey {
		if clientSessionKey[i] != serverSessionKey[i] {
			t.Fatal("session keys do not match")
		}
	}
}

func TestHandshakeRequestEncodeDecode(t *testing.T) {
	_, pub, _ := GenerateEphemeralKey()
	deviceKey := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	machineID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

	req := &HandshakeRequest{
		EphemeralPub: pub,
		DeviceKey:    deviceKey,
		MachineID:    machineID,
	}

	data, err := req.Encode()
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeHandshakeRequest(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.MachineID != machineID {
		t.Fatal("machine ID mismatch")
	}

	// DeviceKey is not decoded — server validates auth via ValidateAuthMessageMulti
	// Verify the raw auth bytes can be extracted for validation
	encoded, _ := req.Encode()
	rawAuth := RawAuth(encoded)
	if len(rawAuth) != 41 {
		t.Fatalf("raw auth length: %d", len(rawAuth))
	}
}

func TestHandshakeResponseEncodeDecode(t *testing.T) {
	_, pub, _ := GenerateEphemeralKey()

	resp := &HandshakeResponse{
		EphemeralPub: pub,
		SessionToken: 0xDEADBEEF,
	}

	data := resp.Encode()

	decoded, err := DecodeHandshakeResponse(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.SessionToken != 0xDEADBEEF {
		t.Fatalf("token: got %x", decoded.SessionToken)
	}
}
