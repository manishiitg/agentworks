package browserrelay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Real peers pin the reported regression: a second browser must not replace
// the first. Account registration also works before any project exists.
func TestAccountBrowsersRouteIndependentlyAndResume(t *testing.T) {
	root := t.TempDir()
	m, err := NewPersistent(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	token, err := m.PairAccount("author", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(m.ServeExtension))
	defer srv.Close()
	dial := func(hello map[string]any) *websocket.Conn {
		t.Helper()
		c, _, e := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://example"}})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.Close() })
		if e = c.WriteJSON(hello); e != nil {
			t.Fatal(e)
		}
		return c
	}
	read := func(c *websocket.Conn, kind string) envelope {
		t.Helper()
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		var e envelope
		if err := c.ReadJSON(&e); err != nil || e.Type != kind {
			t.Fatalf("want %s: %+v %v", kind, e, err)
		}
		return e
	}
	chromeID, edgeID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	chrome := dial(map[string]any{"type": "pair", "token": token, "browser_id": chromeID, "browser": "Chrome", "version": "0.5.0"})
	read(chrome, "account-paired")
	edge := dial(map[string]any{"type": "pair", "token": token, "browser_id": edgeID, "browser": "Edge", "version": "0.5.0"})
	read(edge, "account-paired")
	if got := m.Browsers("author"); len(got) != 2 || !got[0].Connected || !got[1].Connected || len(m.Browsers("another")) != 0 {
		t.Fatalf("account browsers: %+v", got)
	}
	attach := func(scope, profile, id string, control *websocket.Conn) *websocket.Conn {
		t.Helper()
		if _, e := m.PairForProfile("author", scope, "Projects/"+scope, profile); e != nil {
			t.Fatal(e)
		}
		if e := m.SelectBrowser("author", scope, id); e != nil {
			t.Fatal(e)
		}
		if !m.RequestProjectConnection("author", scope) {
			t.Fatal("routing failed")
		}
		if got := read(control, "connect-project"); got.Scope != scope {
			t.Fatalf("wrong project: %+v", got)
		}
		c := dial(map[string]any{"type": "pair", "token": token, "scope": scope, "browser_id": id})
		read(c, "paired")
		return c
	}
	attach("code-one", "code", chromeID, chrome)
	attach("crew-one", "work", edgeID, edge)
	if !m.Status("author", "code-one").Connected || !m.Status("author", "crew-one").Connected {
		t.Fatal("project replaced another browser")
	}
	if err := m.SelectBrowser("another", "code-one", chromeID); err == nil {
		t.Fatal("cross-account browser selected")
	}
	if err := m.DisconnectBrowser("author", chromeID); err != nil {
		t.Fatal(err)
	}
	read(chrome, "account-disconnected")
	if s := m.Status("author", "code-one"); s.Connected || s.BrowserID != chromeID || !strings.Contains(s.Message, "Chrome") {
		t.Fatalf("offline selection changed: %+v", s)
	}
	if !m.Status("author", "crew-one").Connected {
		t.Fatal("disconnecting Chrome interrupted Edge")
	}
	edge.WriteJSON(envelope{Type: "ping"})
	read(edge, "pong")
	if m.RequestProjectConnection("author", "code-one") {
		t.Fatal("offline Chrome silently fell back to Edge")
	}
	if err := m.Disconnect("author", "crew-one"); err != nil {
		t.Fatal(err)
	}
	if got := m.Browsers("author"); len(got) != 2 || !got[0].Connected && !got[1].Connected {
		t.Fatal("project disconnect lost account connection")
	}
	restored, e := NewPersistent(root)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if s := restored.Status("author", "code-one"); s.Connected || s.BrowserID != chromeID {
		t.Fatalf("restart selection: %+v", s)
	}
	if restored.Status("author", "crew-one").Selected {
		t.Fatal("restart revived a removed project selection")
	}
	denied := dial(map[string]any{"type": "pair", "token": token, "browser_id": chromeID, "browser": "Chrome", "resume": true})
	read(denied, "error")
}
