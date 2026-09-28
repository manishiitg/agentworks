package common

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBridgeTokenBindsItsSession(t *testing.T) {
	SetBridgeTokenSecret("server-secret")
	t.Cleanup(func() { SetBridgeTokenSecret("") })

	token := BridgeTokenForSession("chat-a")
	if sid, ok := VerifyBridgeToken(token); !ok || sid != "chat-a" {
		t.Fatalf("a minted token must verify as its session, got %q %v", sid, ok)
	}
	// Swapping in another session keeps the MAC of the first: refused.
	parts := strings.Split(token, ".")
	forged := BridgeTokenForSession("chat-b")
	forgedParts := strings.Split(forged, ".")
	if _, ok := VerifyBridgeToken(parts[0] + "." + forgedParts[1] + "." + parts[2] + "." + parts[3]); ok {
		t.Fatal("a token with another session's name must not verify")
	}
	bridgeTokenMu.RLock()
	key := bridgeTokenKey
	bridgeTokenMu.RUnlock()
	// A 3h-old token (a warm session's) still works; one past the lifetime
	// does not.
	warmHour := strconv.FormatInt(time.Now().Add(-3*time.Hour).Unix()/3600, 10)
	warm := bridgeTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte("chat-a")) + "." + warmHour + "." + base64.RawURLEncoding.EncodeToString(bridgeTokenMAC(key, "chat-a", warmHour))
	if sid, ok := VerifyBridgeToken(warm); !ok || sid != "chat-a" {
		t.Fatal("a long-lived session's token must keep working")
	}
	oldHour := strconv.FormatInt(time.Now().Add(-bridgeTokenLifetime-time.Hour).Unix()/3600, 10)
	expired := bridgeTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte("chat-a")) + "." + oldHour + "." + base64.RawURLEncoding.EncodeToString(bridgeTokenMAC(key, "chat-a", oldHour))
	if _, ok := VerifyBridgeToken(expired); ok {
		t.Fatal("expired token must not verify")
	}
	for _, bad := range []string{"", "server-secret", "mcps1.", "mcps1.x.y", token + "x"} {
		if _, ok := VerifyBridgeToken(bad); ok {
			t.Errorf("%q must not verify", bad)
		}
	}
	SetBridgeTokenSecret("another-server")
	if _, ok := VerifyBridgeToken(token); ok {
		t.Fatal("a token from another server key must not verify")
	}
}

func TestBridgeTokensOffByDefault(t *testing.T) {
	SetBridgeTokenSecret("")
	if BridgeTokensEnabled() || BridgeTokenForSession("chat-a") != "" {
		t.Fatal("session tokens must be off until the server enables them")
	}
	env := map[string]string{"MCP_API_URL": "http://h", "MCP_API_TOKEN": "global", "MCP_SESSION_ID": "chat-a"}
	PopulateMCPBridgeShortEnv(env)
	if env["MCP_API_TOKEN"] != "global" {
		t.Fatalf("with tokens off the env keeps its token, got %q", env["MCP_API_TOKEN"])
	}
}

// Every env that names a session gets that session's token, and MCP_AUTH
// follows it.
func TestPopulateMCPBridgeShortEnvUsesTheSessionsToken(t *testing.T) {
	SetBridgeTokenSecret("server-secret")
	t.Cleanup(func() { SetBridgeTokenSecret("") })
	env := map[string]string{"MCP_API_URL": "http://h/s/step-1", "MCP_API_TOKEN": "global", "MCP_SESSION_ID": "step-1"}
	PopulateMCPBridgeShortEnv(env)
	want := BridgeTokenForSession("step-1")
	if env["MCP_API_TOKEN"] != want || env["MCP_AUTH"] != "Authorization: Bearer "+want {
		t.Fatalf("env must carry the session token, got %q / %q", env["MCP_API_TOKEN"], env["MCP_AUTH"])
	}
}
