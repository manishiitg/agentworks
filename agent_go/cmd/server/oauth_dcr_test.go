package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// newDCRServer returns a stub registration endpoint that issues a client_id on
// every call, plus a counter of how many times it was actually called.
func newDCRServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "issued-client-id",
			"client_secret": "issued-secret",
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// newDCRTestAPI points expandPath at a scratch HOME so these tests write their
// client cache there instead of the developer's real config directory.
func newDCRTestAPI(t *testing.T) (*StreamingAPI, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	if _, err := ensureUserTokenDir("user1"); err != nil {
		t.Fatalf("ensureUserTokenDir: %v", err)
	}
	return api, home
}

func TestEnsureRegisteredClientRegistersThenReusesCache(t *testing.T) {
	api, _ := newDCRTestAPI(t)
	srv, calls := newDCRServer(t)

	got, err := api.ensureRegisteredClient("user1", "Demo", srv.URL, "http://127.0.0.1:9/cb")
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	if got.ClientID != "issued-client-id" || got.ClientSecret != "issued-secret" {
		t.Fatalf("unexpected client: %+v", got)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 registration call, got %d", *calls)
	}

	again, err := api.ensureRegisteredClient("user1", "Demo", srv.URL, "http://127.0.0.1:9/cb")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if again.ClientID != got.ClientID {
		t.Fatalf("cached client_id changed: %q vs %q", again.ClientID, got.ClientID)
	}
	if *calls != 1 {
		t.Fatalf("expected a cache hit, but registration ran %d times", *calls)
	}
}

func TestEnsureRegisteredClientReregistersOnRedirectChange(t *testing.T) {
	api, _ := newDCRTestAPI(t)
	srv, calls := newDCRServer(t)

	if _, err := api.ensureRegisteredClient("user1", "Demo", srv.URL, "http://127.0.0.1:9/cb"); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	// A registration is bound to its redirect URI, so a changed callback URL
	// must produce a new one rather than reuse a mismatched client.
	if _, err := api.ensureRegisteredClient("user1", "Demo", srv.URL, "http://127.0.0.1:10/cb"); err != nil {
		t.Fatalf("re-registration failed: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("expected re-registration on redirect change, got %d calls", *calls)
	}
}

func TestEnsureRegisteredClientCacheIsPrivate(t *testing.T) {
	api, home := newDCRTestAPI(t)
	srv, _ := newDCRServer(t)

	if _, err := api.ensureRegisteredClient("user1", "Demo", srv.URL, "http://127.0.0.1:9/cb"); err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	// The record can carry a client_secret, so it must not be world-readable.
	info, err := os.Stat(filepath.Join(home, ".config/mcpagent/tokens/user1/Demo.client.json"))
	if err != nil {
		t.Fatalf("client cache not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("client cache perms = %o, want 600", perm)
	}
}

func TestEnsureRegisteredClientPropagatesFailure(t *testing.T) {
	api, _ := newDCRTestAPI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"registration_not_supported"}`, http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	// A refusing server must surface an error so the caller falls back to
	// prompting for a hand-registered client_id.
	if _, err := api.ensureRegisteredClient("user1", "Demo", srv.URL, "http://127.0.0.1:9/cb"); err == nil {
		t.Fatal("expected registration failure to be reported")
	}
}

func TestEnsureRegisteredClientCacheHonoursXDGConfigHome(t *testing.T) {
	api, home := newDCRTestAPI(t)
	srv, _ := newDCRServer(t)
	xdg := filepath.Join(home, "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	if _, err := api.ensureRegisteredClient("user1", "Demo", srv.URL, "http://127.0.0.1:9/cb"); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	// The record sits beside the token files, which follow XDG_CONFIG_HOME
	// (mcpagentTokensRoot); a literal ~/.config path would be unwritable where
	// that directory is root-owned (server A).
	if _, err := os.Stat(filepath.Join(xdg, "mcpagent/tokens/user1/Demo.client.json")); err != nil {
		t.Fatalf("client cache not written under XDG_CONFIG_HOME: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config/mcpagent/tokens/user1/Demo.client.json")); err == nil {
		t.Fatal("client cache must not be written under ~/.config when XDG_CONFIG_HOME is set")
	}
}
