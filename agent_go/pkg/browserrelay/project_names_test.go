package browserrelay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Real sockets pin the regression: a slow workspace metadata read must not
// delay heartbeats or CDP replies, and unchanged projects are not re-read.
func TestProjectNameCacheKeepsCDPReaderResponsive(t *testing.T) {
	m := New()
	defer m.Close()
	token, err := m.PairForProfile("alice", "one", "Projects/one", "code")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.PairForProfile("alice", "two", "Projects/two", "work"); err != nil {
		t.Fatal(err)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var mu sync.Mutex
	calls := make(map[string]int)
	resolve := func(_, workspace, _ string) string {
		mu.Lock()
		calls[workspace]++
		mu.Unlock()
		if workspace == "Projects/two" {
			close(blocked)
			<-release
		}
		return "Friendly " + workspace
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.ServeExtensionAuthorizedWithNames(w, r, nil, resolve) }))
	defer server.Close()
	c, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), http.Header{"Origin": {"chrome-extension://test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	c.WriteJSON(envelope{Type: "pair", Token: token, Scope: "one"})
	var reply envelope
	if err := c.ReadJSON(&reply); err != nil || reply.Type != "paired" || reply.Name != "Friendly Projects/one" {
		t.Fatalf("pair: %+v %v", reply, err)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("background lookup did not begin")
	}
	b := m.Lookup("alice", "one")
	endpoint, unlock, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	client, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := c.ReadJSON(&reply); err != nil || reply.Type != "client-connected" {
		t.Fatalf("client: %+v %v", reply, err)
	}
	for i := 0; i < 3; i++ {
		c.WriteJSON(envelope{Type: "ping"})
		if err := c.ReadJSON(&reply); err != nil || reply.Type != "pong" {
			t.Fatalf("blocked metadata delayed heartbeat: %+v %v", reply, err)
		}
	}
	c.WriteJSON(envelope{Type: "cdp", Message: json.RawMessage(`{"id":1,"result":{}}`)})
	client.SetReadDeadline(time.Now().Add(time.Second))
	_, message, err := client.ReadMessage()
	if err != nil || !strings.Contains(string(message), `"id":1`) {
		t.Fatalf("metadata blocked CDP reply: %s %v", message, err)
	}
	mu.Lock()
	one, two := calls["Projects/one"], calls["Projects/two"]
	mu.Unlock()
	if one != 1 || two != 1 {
		t.Fatalf("heartbeat repeated lookups: %v", calls)
	}
}
