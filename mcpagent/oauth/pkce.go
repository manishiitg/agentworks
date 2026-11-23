package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// GeneratePKCEPair generates a PKCE code verifier and challenge pair.
// Returns (verifier, challenge) using S256 method as per RFC 7636.
//
// The verifier is a cryptographically random string between 43-128 characters.
// The challenge is the Base64URL-encoded SHA256 hash of the verifier.
func GeneratePKCEPair() (verifier, challenge string) {
	// Generate 32 random bytes (will be 43 chars when base64url encoded)
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		// Fallback to timestamp-based randomness (should never happen)
		panic("crypto/rand is unavailable: " + err.Error())
	}

	// Create verifier: base64url-encode without padding
	verifier = base64.RawURLEncoding.EncodeToString(randomBytes)

	// Create challenge: SHA256(verifier) then base64url-encode
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])

	return verifier, challenge
}

// VerifyPKCEChallenge verifies that a challenge matches a verifier.
// This is used for testing purposes - in production, the OAuth server validates this.
func VerifyPKCEChallenge(verifier, challenge string) bool {
	hash := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(hash[:])
	return challenge == expectedChallenge
}
