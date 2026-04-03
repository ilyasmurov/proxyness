package udp

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Encrypt encrypts plaintext with XChaCha20-Poly1305.
// Returns: [24-byte nonce][ciphertext + 16-byte tag].
func Encrypt(key, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("new xchacha20: %w", err)
	}

	nonce := make([]byte, aead.NonceSize()) // 24 bytes
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	// nonce + Seal appends ciphertext+tag to nonce
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts a packet produced by Encrypt.
func Decrypt(key, data []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("new xchacha20: %w", err)
	}

	nonceSize := aead.NonceSize()
	if len(data) < nonceSize+aead.Overhead() {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return aead.Open(nil, nonce, ciphertext, nil)
}
