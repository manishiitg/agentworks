package common

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bridge session tokens bind an agent's tool calls to its own session.
//
// Every agent reaches the executor's /tools and /s/{session_id}/tools routes
// with a bearer token. It used to be one process-wide MCP_API_TOKEN, and the
// session the call acted as came from the URL path or the X-Session-ID header,
// both chosen by the caller: an agent could name another user's session and
// act as them. Now each agent gets a token for its own session:
//
//	mcps2.<base64url(session id)>.<issued-hour>.<base64url(HMAC-SHA256(key, session id + issued-hour))>
//
// The server verifies the HMAC and then requires the session the request
// names to be the token's session (cmd/server bridge_auth.go). The key comes
// from a secret held only in the server's memory (random per start unless
// pinned), never from the API token or anything in the environment: a process
// that inherits the server's environment must not be able to mint a token
// for another session.

const bridgeTokenPrefix = "mcps2."

// bridgeTokenLifetime bounds how long a leaked token stays usable within one
// server run (the signing secret is random per start, so a restart revokes
// every token anyway). A running CLI keeps the token it was launched with:
// warm sessions live up to the 3h idle limit and long steps or background
// agents run for hours, so a 2h lifetime made every tool call of a
// long-lived session fail with 401 "invalid API token" (82c864776, caught in
// review 2026-09-28 before it fired on server A). Keep it well above any session
// lifetime until tokens are refreshed in place.
const bridgeTokenLifetime = 7 * 24 * time.Hour

var (
	bridgeTokenMu  sync.RWMutex
	bridgeTokenKey []byte
)

// NewBridgeTokenSecret returns a fresh random signing secret.
func NewBridgeTokenSecret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("bridge token secret: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// SetBridgeTokenSecret enables session tokens signed with secret, which must
// live only in memory. An empty secret disables them.
func SetBridgeTokenSecret(secret string) {
	bridgeTokenMu.Lock()
	defer bridgeTokenMu.Unlock()
	secret = strings.TrimSpace(secret)
	if secret == "" {
		bridgeTokenKey = nil
		return
	}
	sum := sha256.Sum256([]byte("agentworks-bridge-session-v1:" + secret))
	bridgeTokenKey = sum[:]
}

// BridgeTokensEnabled reports whether session tokens are configured.
func BridgeTokensEnabled() bool {
	bridgeTokenMu.RLock()
	defer bridgeTokenMu.RUnlock()
	return len(bridgeTokenKey) > 0
}

func bridgeTokenMAC(key []byte, sessionID, issuedHour string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sessionID + "." + issuedHour))
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
	issuedHour := strconv.FormatInt(time.Now().Unix()/3600, 10)
	return bridgeTokenPrefix + enc.EncodeToString([]byte(sessionID)) + "." + issuedHour + "." + enc.EncodeToString(bridgeTokenMAC(key, sessionID, issuedHour))
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
	if len(parts) != 3 {
		return "", false
	}
	hour, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || strconv.FormatInt(hour, 10) != parts[1] {
		return "", false
	}
	issued := time.Unix(hour*3600, 0)
	now := time.Now()
	if issued.After(now) || !now.Before(issued.Add(bridgeTokenLifetime)) {
		return "", false
	}
	enc := base64.RawURLEncoding
	rawSession, err := enc.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	gotMAC, err := enc.DecodeString(parts[2])
	if err != nil {
		return "", false
	}
	sessionID := string(rawSession)
	if strings.TrimSpace(sessionID) == "" || subtle.ConstantTimeCompare(gotMAC, bridgeTokenMAC(key, sessionID, parts[1])) != 1 {
		return "", false
	}
	return sessionID, true
}
