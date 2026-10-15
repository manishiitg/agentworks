package browserrelay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
)

// Launched by the GitHub browser runner after installing Chromium. This uses
// the real extension worker, popup, local storage and WebSocket manager.
func TestAccountBrowsersChromium(t *testing.T) {
	script := os.Getenv("BROWSER_ACCOUNT_CHECK_SCRIPT")
	if script == "" {
		t.Skip("run through the browser check on GitHub")
	}
	root := t.TempDir()
	m, err := NewPersistent(root)
	if err != nil {
		t.Fatal(err)
	}
	var live atomic.Pointer[Manager]
	live.Store(m)
	defer func() { live.Load().Close() }()
	token, err := m.PairAccount("author", false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/browser/extension/connect", func(w http.ResponseWriter, r *http.Request) { live.Load().ServeExtension(w, r) })
	mux.HandleFunc("/fixture/state", func(w http.ResponseWriter, r *http.Request) {
		current := live.Load()
		json.NewEncoder(w).Encode(map[string]any{"browsers": current.Browsers("author"), "alpha": current.Status("author", "alpha"), "beta": current.Status("author", "beta")})
	})
	mux.HandleFunc("/fixture/select", func(w http.ResponseWriter, r *http.Request) {
		var p struct{ Scope, ID string }
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		if p.Scope != "alpha" && p.Scope != "beta" {
			http.Error(w, "unavailable", 403)
			return
		}
		current := live.Load()
		if _, err := current.PairForProfile("author", p.Scope, "Projects/"+p.Scope, "code"); err != nil {
			t.Error(err)
			http.Error(w, "failed", 500)
			return
		}
		if err := current.SelectBrowser("author", p.Scope, p.ID); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		current.RequestProjectConnection("author", p.Scope)
	})
	mux.HandleFunc("/fixture/disconnect", func(w http.ResponseWriter, r *http.Request) {
		if err := live.Load().DisconnectBrowser("author", r.URL.Query().Get("id")); err != nil {
			http.Error(w, err.Error(), 400)
		}
	})
	mux.HandleFunc("/fixture/restart", func(w http.ResponseWriter, r *http.Request) {
		next, err := NewPersistent(root)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		old := live.Swap(next)
		old.Close()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cmd := exec.Command(os.Getenv("BROWSER_ACCOUNT_NODE"), script)
	cmd.Env = append(os.Environ(), "BROWSER_ACCOUNT_FIXTURE_URL="+srv.URL, "BROWSER_ACCOUNT_FIXTURE_TOKEN="+token)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
