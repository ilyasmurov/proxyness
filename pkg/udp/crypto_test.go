package udp

import (
	"crypto/rand"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	plaintext := []byte("hello world")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// ciphertext = 24-byte nonce + encrypted + 16-byte tag
	if len(ciphertext) != 24+len(plaintext)+16 {
		t.Fatalf("wrong ciphertext length: %d", len(ciphertext))
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	if string(decrypted) != "hello world" {
		t.Fatalf("got %q", decrypted)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	ciphertext, _ := Encrypt(key1, []byte("secret"))
	_, err := Decrypt(key2, ciphertext)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDecryptTampered(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	ciphertext, _ := Encrypt(key, []byte("secret"))
	ciphertext[len(ciphertext)-1] ^= 0xff // flip last byte

	_, err := Decrypt(key, ciphertext)
	if err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}
