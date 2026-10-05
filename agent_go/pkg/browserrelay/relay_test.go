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
	token, err := m.PairForProfile("alice", "project-one", "Project one", "code")
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
	_, focusRelease, err := b.AcquireForActive(context.Background(), "chat-one", true)
	if err != nil {
		t.Fatal(err)
	}
	client.WriteMessage(websocket.TextMessage, []byte(`{"id":1,"method":"Target.getTargets"}`))
	extension.ReadJSON(&reply)
	if reply.Type != "cdp" || !reply.Active || !strings.Contains(string(reply.Message), "getTargets") {
		t.Fatal("request not forwarded")
	}
	extension.WriteJSON(envelope{Type: "cdp", Message: json.RawMessage(`{"id":1,"result":{"targetInfos":[]}}`)})
	client.SetReadDeadline(time.Now().Add(time.Second))
	_, message, err := client.ReadMessage()
	if err != nil || !strings.Contains(string(message), "targetInfos") {
		t.Fatal("response not forwarded", err)
	}
	focusRelease()
	client.WriteMessage(websocket.TextMessage, []byte(`{"id":2,"method":"Target.getTargets"}`))
	reply = envelope{}
	extension.ReadJSON(&reply)
	if reply.Active {
		t.Fatal("activation permission survived the tool call")
	}
	reused := dial()
	reused.WriteJSON(envelope{Type: "pair", Token: token})
	reused.ReadJSON(&reply)
	if reply.Type != "paired" {
		t.Fatal("stable code could not reconnect")
	}
	if m.Lookup("alice", "project-one").Session() == b.Session() {
		t.Fatal("reconnect retained stale refs")
	}
	reused.WriteJSON(envelope{Type: "stop"})
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
	legacyRoot := t.TempDir()
	legacySelection, _ := json.Marshal(map[string]string{key("alice", "legacy-code"): "_users/alice/Chats/Code/projects/legacy"})
	if err := os.WriteFile(filepath.Join(legacyRoot, "selected.json"), legacySelection, 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := NewPersistent(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if binding := legacy.Lookup("alice", "legacy-code"); binding == nil || binding.Profile() != "code" || binding.Status().Connected {
		t.Fatal("legacy Code selection lost its fail-closed routing")
	}
	state, err := os.ReadFile(filepath.Join(stateRoot, "selected.json"))
	if err != nil || strings.Contains(string(state), token) || strings.Contains(string(state), b.capability) {
		t.Fatal("selection persisted a credential")
	}
	m.Disconnect("alice", "project-one")
	if m.Lookup("alice", "project-one") != nil {
		t.Fatal("disconnect did not restore workspace choice")
	}
	copied, err := restarted.PairForProfile("alice", "project-one", "Project one", "code")
	if err != nil || copied != token {
		t.Fatal("restart rotated private code", err)
	}
	if restarted.Lookup("alice", "project-one").Profile() != "code" {
		t.Fatal("restart lost Code choice and could fall back")
	}
	info, err := os.Stat(filepath.Join(stateRoot, "credentials.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private code file permissions", err)
	}
	reset, err := m.Reset("alice", "project-one", "Project one", "code")
	if err != nil || reset == token {
		t.Fatal("reset did not rotate code", err)
	}
	c := dial()
	c.WriteJSON(envelope{Type: "pair", Token: token})
	c.ReadJSON(&reply)
	if reply.Type != "error" {
		t.Fatal("revoked code admitted")
	}
	fresh := dial()
	fresh.WriteJSON(envelope{Type: "pair", Token: reset})
	fresh.ReadJSON(&reply)
	if reply.Type != "paired" {
		t.Fatal("new code rejected")
	}
	fresh.WriteJSON(envelope{Type: "ping"})
	if err := fresh.ReadJSON(&reply); err != nil || reply.Type != "pong" {
		t.Fatal("reconnected heartbeat", err)
	}
	resetAgain, err := m.Reset("alice", "project-one", "Project one", "code")
	if err != nil || resetAgain == reset {
		t.Fatal("active reset did not rotate", err)
	}
	if err := fresh.ReadJSON(&reply); err == nil {
		t.Fatal("reset kept live authority")
	}
	if m.Status("alice", "project-one").Connected {
		t.Fatal("reset kept connected status")
	}
	binding := m.Lookup("alice", "project-one")
	if remaining := time.Until(binding.expires); remaining <= 7*time.Hour || remaining > ConnectionLifetime {
		t.Fatal("live authority lifetime changed", remaining)
	}
}
