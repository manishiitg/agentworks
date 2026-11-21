package browserrelay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// PLAT-746-962: each project connection/heartbeat reread every saved project's
// manifests. Account discovery owns that directory; scoped sockets check one.
func TestProjectBrowserConnectionChecksOnlySelectedTargetAndRevocation(t *testing.T) {
	m, err := NewPersistent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	root := t.TempDir()
	var token string
	const selected = "crew-0"
	for i := 0; i < 32; i++ {
		scope := fmt.Sprintf("crew-%d", i)
		token, err = m.PairForProfile("person", scope, scope, "work")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, scope+".json"), []byte(`{"owner":"person","kind":"crew"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(user, scope, workspace, profile string) error {
		data, err := os.ReadFile(filepath.Join(root, workspace+".json"))
		if err != nil {
			return err
		}
		var manifest struct {
			Owner string `json:"owner"`
		}
		if err = json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		if manifest.Owner != user {
			return fmt.Errorf("not owner")
		}
		return nil
	}
	before := time.Now()
	available := m.availableProjects(token, check)
	beforeDuration := time.Since(before)
	if len(available) != 32 {
		t.Fatal("directory fixture unavailable")
	}
	var selectedCalls, unrelatedCalls atomic.Int32
	var selectedNS atomic.Int64
	var revoked atomic.Bool
	authorize := func(user, scope, workspace, profile string) error {
		if scope != selected {
			unrelatedCalls.Add(1)
		} else {
			selectedCalls.Add(1)
		}
		start := time.Now()
		err := check(user, scope, workspace, profile)
		selectedNS.Add(time.Since(start).Nanoseconds())
		if revoked.Load() {
			return fmt.Errorf("owner revoked access")
		}
		return err
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.ServeExtensionAuthorizedWithNames(w, r, authorize, func(user, workspace, profile string) string { return "Selected Crew" })
	}))
	defer srv.Close()
	c, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http", "ws", 1), http.Header{"Origin": []string{"chrome-extension://example"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err = c.WriteJSON(envelope{Type: "pair", Token: token, Scope: selected, Version: "0.6.0"}); err != nil {
		t.Fatal(err)
	}
	var reply envelope
	if err = c.ReadJSON(&reply); err != nil || reply.Type != "paired" || reply.Name != "Selected Crew" {
		t.Fatalf("selected connection: %s %v", reply.Type, err)
	}
	if selectedCalls.Load() != 1 || unrelatedCalls.Load() != 0 || len(reply.Projects) != 0 {
		t.Fatal("project connect scanned or published unrelated projects")
	}
	t.Logf("file-read authorization fixture: before32 manifests %s; after1 manifest %s (not whole-app CPU)", beforeDuration, time.Duration(selectedNS.Load()))
	if err = c.WriteJSON(envelope{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	reply = envelope{}
	if err = c.ReadJSON(&reply); err != nil || reply.Type != "pong" || len(reply.Projects) != 0 {
		t.Fatalf("ping: %s %v", reply.Type, err)
	}
	if selectedCalls.Load() != 2 || unrelatedCalls.Load() != 0 {
		t.Fatal("heartbeat did not check exactly the selected target")
	}
	revoked.Store(true)
	if err = c.WriteJSON(envelope{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	reply = envelope{}
	err = c.ReadJSON(&reply)
	if selectedCalls.Load() != 3 || unrelatedCalls.Load() != 0 {
		t.Fatal("revocation check did not run on the selected target")
	}
	if err == nil && reply.Type != "error" {
		t.Fatal("revoked target remained admitted")
	}
}
