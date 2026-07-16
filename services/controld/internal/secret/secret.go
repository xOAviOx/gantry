// Package secret encrypts project env-var values at rest with AES-256-GCM, using
// a fresh random nonce per value (SPEC.md §11). The 32-byte master key comes from
// GANTRY_MASTER_KEY (base64). Ciphertext and nonce are stored in separate columns;
// values are only ever decrypted at container-create time.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Box seals and opens values under one master key.
type Box struct {
	aead cipher.AEAD
}

// New builds a Box from a base64-encoded 32-byte key. An empty or wrong-sized key
// is an error, so callers can disable env features rather than run insecurely.
func New(masterKeyB64 string) (*Box, error) {
	if masterKeyB64 == "" {
		return nil, errors.New("GANTRY_MASTER_KEY is not set")
	}
	key, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes (got %d)", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Encrypt seals plaintext and returns the ciphertext and the fresh nonce used.
func (b *Box) Encrypt(plaintext string) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("nonce: %w", err)
	}
	ciphertext = b.aead.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// Decrypt opens ciphertext sealed by Encrypt. It fails if the key is wrong or the
// ciphertext/nonce was tampered with (GCM authentication).
func (b *Box) Decrypt(ciphertext, nonce []byte) (string, error) {
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
