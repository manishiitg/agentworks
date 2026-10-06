package browserrelay

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Live sockets pin the multiplexing boundary: identical native request IDs
// cannot cross chat channels, and revoking one chat cannot stop another.
func TestCodeConversationChannels(t *testing.T) {
	m := New()
	defer m.Close()
	token, err := m.PairForProfile("alice", "code-project", "Code project", "code")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(m.ServeExtension))
	defer server.Close()
	ext, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ext.Close()
	if err := ext.WriteJSON(envelope{Type: "pair", Token: token, Features: []string{"chat-clients"}}); err != nil {
		t.Fatal(err)
	}
	var paired envelope
	if err := ext.ReadJSON(&paired); err != nil || paired.Type != "paired" {
		t.Fatal(err, paired)
	}
	forwarded := make(chan envelope, 10)
	go func() {
		for {
			var e envelope
			if ext.ReadJSON(&e) != nil {
				return
			}
			if e.Type == "client-register" {
				_ = ext.WriteJSON(envelope{Type: "client-ready", ClientID: e.ClientID})
			} else if e.Type == "cdp" {
				forwarded <- e
			}
		}
	}()
	b := m.Lookup("alice", "code-project")
	a, release, err := b.AcquireClient(context.Background(), "chat-a", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if _, _, err := b.AcquireClient(ctx, "chat-b", false); err == nil {
		t.Fatal("project tool gate not held")
	}
	cancel()
	release()
	c, release, err := b.AcquireClient(context.Background(), "chat-b", false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if a.Session() == c.Session() || a.Endpoint() == c.Endpoint() {
		t.Fatal("private native state shared")
	}
	aAgain, release, err := b.AcquireClient(context.Background(), "chat-a", false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if aAgain != a {
		t.Fatal("one chat lost its reference cache")
	}
	dial := func(url string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		return conn
	}
	aw, bw := dial(a.Endpoint()), dial(c.Endpoint())
	for _, w := range []*websocket.Conn{aw, bw} {
		if err := w.WriteMessage(websocket.TextMessage, []byte(`{"id":7,"method":"Target.getTargets"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		var e envelope
		select {
		case e = <-forwarded:
		case <-time.After(time.Second):
			t.Fatal("missing routed command")
		}
		if e.ClientID != a.id && e.ClientID != c.id {
			t.Fatal("unknown channel", e.ClientID)
		}
		reply, _ := json.Marshal(map[string]any{"id": 7, "result": map[string]string{"client": e.ClientID}})
		if err := ext.WriteJSON(envelope{Type: "cdp", ClientID: e.ClientID, Message: reply}); err != nil {
			t.Fatal(err)
		}
	}
	for id, w := range map[string]*websocket.Conn{a.id: aw, c.id: bw} {
		var reply struct {
			ID     int
			Result struct{ Client string }
		}
		if err := w.ReadJSON(&reply); err != nil || reply.ID != 7 || reply.Result.Client != id {
			t.Fatal("response crossed chat boundary", err, reply)
		}
	}
	sessions := m.ReleaseConversation(context.Background(), "chat-a", "code-project")
	if len(sessions) != 1 || sessions[0] != a.Session() {
		t.Fatal("wrong runtime revoked", sessions)
	}
	if conn, _, err := websocket.DefaultDialer.Dial(a.Endpoint(), nil); err == nil {
		conn.Close()
		t.Fatal("revoked private endpoint usable")
	}
	if !b.Status().Connected {
		t.Fatal("revoking one chat stopped project")
	}
	if err := bw.WriteMessage(websocket.TextMessage, []byte(`{"id":8,"method":"Target.getTargets"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-forwarded:
		if e.ClientID != c.id {
			t.Fatal("surviving chat misrouted")
		}
	case <-time.After(time.Second):
		t.Fatal("surviving chat cannot command")
	}
}
