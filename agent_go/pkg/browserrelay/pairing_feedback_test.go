package browserrelay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOldExtensionGetsUpdateReasonWithoutChangingProjectAuthority(t *testing.T) {
	m := New()
	defer m.Close()
	token, err := m.PairAccount("alice", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(m.ServeExtension))
	defer srv.Close()
	connect := func(hello envelope) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1), http.Header{"Origin": {"chrome-extension://example"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := conn.WriteJSON(hello); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	assertRejected := func(conn *websocket.Conn, reason string) {
		t.Helper()
		var reply envelope
		if err := conn.ReadJSON(&reply); err != nil || reply.Type != "error" || reply.Reason != reason {
			t.Fatalf("pairing error=%+v err=%v", reply, err)
		}
		_, _, err := conn.ReadMessage()
		var closed *websocket.CloseError
		if !errors.As(err, &closed) || closed.Code != 4002 || closed.Text != reason {
			t.Fatalf("want a real close reason %q, got %v", reason, err)
		}
	}
	// RTS 0.4.9 has no account browser ID and falls into the project path.
	assertRejected(connect(envelope{Type: "pair", Token: token, Version: "0.4.9"}), "extension_outdated")
	failure := m.PairingFailure("alice")
	if failure == nil || failure.Version != "0.4.9" || !strings.Contains(failure.Message, "Reload") || !strings.Contains(failure.Message, MinimumExtensionVersion) {
		t.Fatalf("missing actionable account feedback: %+v", failure)
	}
	if m.PairingFailure("bob") != nil || len(m.Browsers("alice")) != 0 || m.Status("alice", "not-authorized").Connected {
		t.Fatal("rejected pairing changed authority or exposed another account's error")
	}
	assertRejected(connect(envelope{Type: "pair", Token: "unknown-token", Version: "0.4.9"}), "invalid_connection_code")
	if m.PairingFailure("bob") != nil {
		t.Fatal("invalid token attributed feedback to another account")
	}
	// Even with a browser ID, a known old protocol gets the update message.
	id := "11111111-1111-4111-8111-111111111111"
	assertRejected(connect(envelope{Type: "pair", Token: token, BrowserID: id, Version: "0.4.9"}), "extension_outdated")
	current := connect(envelope{Type: "pair", Token: token, BrowserID: id, Version: "0.5.0"})
	var reply envelope
	if err := current.ReadJSON(&reply); err != nil || reply.Type != "account-paired" {
		t.Fatalf("current extension could not pair: %+v %v", reply, err)
	}
	if m.PairingFailure("alice") != nil || len(m.Browsers("alice")) != 1 {
		t.Fatal("successful updated pairing did not clear the old setup failure")
	}
}

func TestPairingFeedbackRequiresCurrentAccountAndExpires(t *testing.T) {
	m := New()
	defer m.Close()
	token, err := m.PairAccount("alice", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.ServeExtensionAuthorized(w, r, func(string, string, string, string) error { return errors.New("denied") })
	}))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1), http.Header{"Origin": {"chrome-extension://example"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_ = conn.WriteJSON(envelope{Type: "pair", Token: token, Version: "0.4.9"})
	var reply envelope
	if err := conn.ReadJSON(&reply); err != nil || reply.Reason != "invalid_connection_code" || m.PairingFailure("alice") != nil {
		t.Fatalf("denied account received attributed setup feedback: %+v %v", reply, err)
	}
	m.mu.Lock()
	m.pairingFailures = map[string]PairingFailure{"alice": {Reason: "extension_outdated", At: time.Now().Add(-11 * time.Minute).UnixMilli()}}
	m.mu.Unlock()
	if m.PairingFailure("alice") != nil {
		t.Fatal("expired pairing feedback remained visible")
	}
}

func TestExtensionProtocolVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		version string
		old     bool
	}{
		{"0.4.9", true}, {"0.5.0", false}, {"0.10.0", false}, {"1.0", false}, {"", false}, {"unknown", false}, {"0..4", false},
	} {
		if got := extensionVersionBefore(tc.version, MinimumExtensionVersion); got != tc.old {
			t.Fatalf("version %q old=%t, want %t", tc.version, got, tc.old)
		}
	}
}
