// Package sealbox is the one AES-GCM helper for secrets at rest: a value
// sealed with a key and additional data (AAD) that binds it to where it
// belongs, so a ciphertext copied to another place does not open there.
// The result is base64(nonce || ciphertext).
package sealbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Seal encrypts plaintext with key (16, 24 or 32 bytes) bound to aad.
func Seal(key []byte, plaintext string, aad []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce error: %w", err)
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), aad)), nil
}

// Open decrypts a Seal result; it fails if key or aad differ.
func Open(key []byte, sealed string, aad []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("invalid base64: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}
	return string(plaintext), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher error: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM error: %w", err)
	}
	return gcm, nil
}
