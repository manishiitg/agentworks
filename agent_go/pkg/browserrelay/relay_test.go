package browserrelay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// One transport test pins the authority and revocation rules on live sockets.
func TestRelayPairingIsolationAndStop(t *testing.T) {
	stateRoot := t.TempDir()
	m, err := NewPersistent(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	token, err := m.Pair("alice", "project-one", "Project one")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(m.ServeExtension))
	defer server.Close()
	dial := func() *websocket.Conn {
		t.Helper()
		c, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://test"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	extension := dial()
	extension.WriteJSON(envelope{Type: "pair", Token: token})
	var reply envelope
	if err := extension.ReadJSON(&reply); err != nil || reply.Type != "paired" {
		t.Fatalf("pair: %v %+v", err, reply)
	}
	extension.WriteJSON(envelope{Type: "tabs", Tabs: 1})
	if m.Lookup("bob", "project-one") != nil || m.Lookup("alice", "project-two") != nil {
		t.Fatal("cross-account or cross-workspace lookup")
	}
	b := m.Lookup("alice", "project-one")
	deadline := time.Now().Add(time.Second)
	for !b.Status().Connected || b.Status().Tabs == 0 {
		if time.Now().After(deadline) {
			t.Fatal("tab announcement not received")
		}
		time.Sleep(time.Millisecond)
	}
	endpoint, release, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, release, err := b.AcquireFor(context.Background(), "chat-one"); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	if _, _, err := b.AcquireFor(context.Background(), "chat-two"); err == nil {
		t.Fatal("second conversation could reuse cached refs")
	}
	client, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	extension.ReadJSON(&reply)
	if reply.Type != "client-connected" {
		t.Fatal(reply.Type)
	}
	if c, _, err := websocket.DefaultDialer.Dial(endpoint, nil); err == nil {
		c.Close()
		t.Fatal("second controller admitted")
	}
	if c, _, err := websocket.DefaultDialer.Dial(endpoint+"bad", nil); err == nil {
		c.Close()
		t.Fatal("wrong capability admitted")
	}
	client.WriteMessage(websocket.TextMessage, []byte(`{"id":1,"method":"Target.getTargets"}`))
	extension.ReadJSON(&reply)
	if reply.Type != "cdp" || !strings.Contains(string(reply.Message), "getTargets") {
		t.Fatal("request not forwarded")
	}
	extension.WriteJSON(envelope{Type: "cdp", Message: json.RawMessage(`{"id":1,"result":{"targetInfos":[]}}`)})
	client.SetReadDeadline(time.Now().Add(time.Second))
	_, message, err := client.ReadMessage()
	if err != nil || !strings.Contains(string(message), "targetInfos") {
		t.Fatal("response not forwarded", err)
	}
	reused := dial()
	reused.WriteJSON(envelope{Type: "pair", Token: token})
	reused.ReadJSON(&reply)
	if reply.Type != "error" {
		t.Fatal("pair reused")
	}
	extension.WriteJSON(envelope{Type: "stop"})
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("stop did not close controller")
	}
	if _, _, err := b.Acquire(context.Background()); err == nil {
		t.Fatal("stopped binding allowed command")
	}
	if !b.Status().Selected {
		t.Fatal("stop silently removed selection")
	}
	restarted, err := NewPersistent(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if status := restarted.Status("alice", "project-one"); !status.Selected || status.Connected {
		t.Fatal("restart lost disconnected selection")
	}
	state, err := os.ReadFile(filepath.Join(stateRoot, "selected.json"))
	if err != nil || strings.Contains(string(state), token) || strings.Contains(string(state), b.capability) {
		t.Fatal("selection persisted a credential")
	}
	m.Disconnect("alice", "project-one")
	if m.Lookup("alice", "project-one") != nil {
		t.Fatal("disconnect did not restore workspace choice")
	}
	expired, _ := m.Pair("alice", "project-one", "Project one")
	m.mu.Lock()
	g := m.pairs[expired]
	g.Expires = time.Now().Add(-time.Second)
	m.pairs[expired] = g
	m.mu.Unlock()
	c := dial()
	c.WriteJSON(envelope{Type: "pair", Token: expired})
	c.ReadJSON(&reply)
	if reply.Type != "error" {
		t.Fatal("expired pairing admitted")
	}
}
