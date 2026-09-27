package common

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"sync"
)

// Bridge session tokens bind an agent's tool calls to its own session.
//
// Every agent reaches the executor's /tools and /s/{session_id}/tools routes
// with a bearer token. It used to be one process-wide MCP_API_TOKEN, and the
// session the call acted as came from the URL path or the X-Session-ID header,
// both chosen by the caller: an agent could name another user's session and
// act as them. Now each agent gets a token for its own session:
//
//	mcps1.<base64url(session id)>.<base64url(HMAC-SHA256(key, session id))>
//
// The server verifies the HMAC and then requires the session the request
// names to be the token's session (cmd/server bridge_auth.go). The key is
// derived from the server's API token, so no state is stored and tokens live
// exactly as long as that token does.

const bridgeTokenPrefix = "mcps1."

var (
	bridgeTokenMu  sync.RWMutex
	bridgeTokenKey []byte
)

// SetBridgeTokenSecret enables session tokens, keyed from the server's API
// token. An empty secret disables them.
func SetBridgeTokenSecret(serverAPIToken string) {
	bridgeTokenMu.Lock()
	defer bridgeTokenMu.Unlock()
	serverAPIToken = strings.TrimSpace(serverAPIToken)
	if serverAPIToken == "" {
		bridgeTokenKey = nil
		return
	}
	sum := sha256.Sum256([]byte("agentworks-bridge-session-v1:" + serverAPIToken))
	bridgeTokenKey = sum[:]
}

// BridgeTokensEnabled reports whether session tokens are configured.
func BridgeTokensEnabled() bool {
	bridgeTokenMu.RLock()
	defer bridgeTokenMu.RUnlock()
	return len(bridgeTokenKey) > 0
}

func bridgeTokenMAC(key []byte, sessionID string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sessionID))
	return mac.Sum(nil)
}

// BridgeTokenForSession returns the bearer token for sessionID, or "" when
// session tokens are not configured or the session is empty.
func BridgeTokenForSession(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	bridgeTokenMu.RLock()
	key := bridgeTokenKey
	bridgeTokenMu.RUnlock()
	if len(key) == 0 || sessionID == "" {
		return ""
	}
	enc := base64.RawURLEncoding
	return bridgeTokenPrefix + enc.EncodeToString([]byte(sessionID)) + "." + enc.EncodeToString(bridgeTokenMAC(key, sessionID))
}

// IsBridgeSessionToken reports whether token has the session-token shape
// (not whether it is valid).
func IsBridgeSessionToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), bridgeTokenPrefix)
}

// VerifyBridgeToken returns the session a valid session token was minted for.
func VerifyBridgeToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, bridgeTokenPrefix) {
		return "", false
	}
	bridgeTokenMu.RLock()
	key := bridgeTokenKey
	bridgeTokenMu.RUnlock()
	if len(key) == 0 {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(token, bridgeTokenPrefix), ".")
	if len(parts) != 2 {
		return "", false
	}
	enc := base64.RawURLEncoding
	rawSession, err := enc.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	gotMAC, err := enc.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	sessionID := string(rawSession)
	if strings.TrimSpace(sessionID) == "" || subtle.ConstantTimeCompare(gotMAC, bridgeTokenMAC(key, sessionID)) != 1 {
		return "", false
	}
	return sessionID, true
}
