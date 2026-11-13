package browserrelay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// PLAT-746-953: a restart renews credentials, not the chat's CLI tab namespace.
// Two Crews sharing one browser and two chats in one Crew remain independent.
func TestChatBrowserSessionSurvivesRestartAndKeepsCrewsSeparate(t *testing.T) {
	root := t.TempDir()
	m, err := NewPersistent(root)
	if err != nil {
		t.Fatal(err)
	}
	var live atomic.Pointer[Manager]
	live.Store(m)
	defer func() { live.Load().Close() }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { live.Load().ServeExtension(w, r) }))
	defer server.Close()
	tokens := map[string]string{}
	for _, crew := range []string{"crew-one", "crew-two"} {
		token, err := m.PairForProfile("person", crew, "Crew/"+crew, "work")
		if err != nil {
			t.Fatal(err)
		}
		tokens[crew] = token
	}
	connect := func(crew string) *websocket.Conn {
		t.Helper()
		c, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://example"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		if err = c.WriteJSON(envelope{Type: "pair", Token: tokens[crew], Features: []string{"chat-clients"}}); err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		var e envelope
		if err = c.ReadJSON(&e); err != nil || e.Type != "paired" {
			t.Fatalf("pair: %s %v", e.Type, err)
		}
		return c
	}
	acquire := func(crew, owner string, extension *websocket.Conn) *Client {
		t.Helper()
		type result struct {
			c   *Client
			err error
		}
		resultC := make(chan result, 1)
		go func() {
			c, release, err := live.Load().Lookup("person", crew).AcquireClient(context.Background(), owner, false)
			if release != nil {
				release()
			}
			resultC <- result{c, err}
		}()
		extension.SetReadDeadline(time.Now().Add(3 * time.Second))
		var e envelope
		if err := extension.ReadJSON(&e); err != nil || e.Type != "client-register" {
			t.Fatalf("register: %s %v", e.Type, err)
		}
		if err := extension.WriteJSON(envelope{Type: "client-ready", ClientID: e.ClientID, Tabs: 1}); err != nil {
			t.Fatal(err)
		}
		got := <-resultC
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got.c
	}
	extOne, extTwo := connect("crew-one"), connect("crew-two")
	one := acquire("crew-one", "chat-one", extOne)
	side := acquire("crew-one", "chat-two", extOne)
	two := acquire("crew-two", "chat-one", extTwo)
	if one.Session() == side.Session() || one.Session() == two.Session() || side.Session() == two.Session() {
		t.Fatal("Crews/chats shared a CLI tab namespace")
	}
	old := live.Load()
	next, err := NewPersistent(root)
	if err != nil {
		t.Fatal(err)
	}
	live.Store(next)
	old.Close()
	// Reconnect the other Crew first; registration order must not change identity.
	extTwo, extOne = connect("crew-two"), connect("crew-one")
	for _, item := range []struct {
		crew, owner string
		extension   *websocket.Conn
		before      *Client
	}{
		{"crew-two", "chat-one", extTwo, two}, {"crew-one", "chat-two", extOne, side}, {"crew-one", "chat-one", extOne, one},
	} {
		after := acquire(item.crew, item.owner, item.extension)
		if after.Session() != item.before.Session() {
			t.Fatal("restart lost existing chat tab references")
		}
		if after.capability == item.before.capability || after.Endpoint() == item.before.Endpoint() {
			t.Fatal("restart reused transport authority")
		}
	}
}
