package browserrelay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Owner, 2026-11-21: one Crew uses several browsers at once. A second browser added to a project connects next to the
// main one instead of replacing it, a command picks it by name, and stopping it leaves the main one connected; a
// browser the project does not use is still refused.
func TestProjectUsesOtherBrowsersNextToItsMainOne(t *testing.T) {
	m, err := NewPersistent(t.TempDir())
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
	mainID, otherID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	mainControl := dial(map[string]any{"type": "pair", "token": token, "browser_id": mainID, "browser": "Chrome", "version": "0.5.0"})
	read(mainControl, "account-paired")
	otherControl := dial(map[string]any{"type": "pair", "token": token, "browser_id": otherID, "browser": "Edge", "version": "0.5.0"})
	read(otherControl, "account-paired")
	if _, err := m.PairForProfile("author", "crew-one", "Crew/one", "work"); err != nil {
		t.Fatal(err)
	}
	if err := m.SelectBrowser("author", "crew-one", mainID); err != nil {
		t.Fatal(err)
	}
	m.RequestProjectConnection("author", "crew-one")
	read(mainControl, "connect-project")
	read(dial(map[string]any{"type": "pair", "token": token, "scope": "crew-one", "browser_id": mainID}), "paired")

	if err := m.AlsoUseBrowser("author", "crew-one", otherID); err != nil {
		t.Fatal(err)
	}
	if got := read(otherControl, "connect-project"); got.Scope != "crew-one" {
		t.Fatalf("other browser asked for the wrong project: %+v", got)
	}
	other := dial(map[string]any{"type": "pair", "token": token, "scope": "crew-one", "browser_id": otherID})
	read(other, "paired")

	main, err := m.LookupNamed("author", "crew-one", "")
	if err != nil || !main.Status().Connected {
		t.Fatalf("main browser replaced by the other one: %v", err)
	}
	picked, err := m.LookupNamed("author", "crew-one", "edge")
	if err != nil || picked == main || !picked.Status().Connected {
		t.Fatalf("other browser by name: %v", err)
	}
	if got := m.CrewBrowsers("author", "crew-one"); len(got) != 2 || !got[0].Main || got[0].ID != mainID || got[1].ID != otherID || !got[1].Connected {
		t.Fatalf("project browsers: %+v", got)
	}
	if _, err := m.LookupNamed("author", "crew-one", "Safari"); err == nil {
		t.Fatal("a browser the project does not use was found")
	}

	if err := m.StopAlsoBrowser("author", "crew-one", otherID); err != nil {
		t.Fatal(err)
	}
	if !main.Status().Connected || picked.Status().Connected {
		t.Fatal("stopping the other browser changed the wrong link")
	}
	read(dial(map[string]any{"type": "pair", "token": token, "scope": "crew-one", "browser_id": otherID}), "error")
}
