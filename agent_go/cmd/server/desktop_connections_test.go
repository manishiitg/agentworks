package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func desktopTestRequest(method, path, body, owner string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:18743"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer local-session")
	r.Header.Set("X-AgentWorks-Desktop", "app-proof")
	return r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: owner, Username: owner}))
}
func desktopTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "desktop-connections-test-signing-secret")
	oldSecret, oldHost := desktopBootstrapSecret, desktopBootstrapHost
	desktopBootstrapSecret, desktopBootstrapHost = "app-proof", "127.0.0.1:18743"
	t.Cleanup(func() { desktopBootstrapSecret, desktopBootstrapHost = oldSecret, oldHost })
}

// PLAT-746: remote credentials must not become local credentials or cross owners.
func TestDesktopConnectionProxyOwnerAndCredentialBoundary(t *testing.T) {
	desktopTestEnvironment(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer remote-session" || r.Header.Get("X-AgentWorks-Desktop") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-User-ID") != "" {
			t.Error("credential boundary crossed")
		}
		if r.Header.Get("X-Session-ID") != "chat" || r.Header.Get("Idempotency-Key") != "submission" {
			t.Error("chat delivery context lost")
		}
		w.Header().Set("X-Renewed-Token", "remote-renewed")
		w.Header().Set("Set-Cookie", "remote-cookie=value")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: done\n\n")
	}))
	defer remote.Close()
	if err := writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: remote.URL, Token: "remote-session"}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	call := func(owner string, change func(*http.Request)) *httptest.ResponseRecorder {
		r := desktopTestRequest("GET", "/api/connections/saved/api/events", "", owner)
		r = mux.SetURLVars(r, map[string]string{"connection": "saved", "path": "api/events"})
		r.Header.Set("X-Session-ID", "chat")
		r.Header.Set("Idempotency-Key", "submission")
		r.Header.Set("Cookie", "local-cookie=value")
		r.Header.Set("X-User-ID", "forged")
		if change != nil {
			change(r)
		}
		w := httptest.NewRecorder()
		api.handleDesktopConnectionProxy(w, r)
		return w
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-AgentWorks-Desktop") },
		func(r *http.Request) { r.Host = "example.com" },
		func(r *http.Request) { r.Header.Set("Origin", "https://example.com") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	} {
		if w := call("owner", change); w.Code != 403 {
			t.Fatalf("gate allowed request: %d", w.Code)
		}
	}
	if w := call("other-owner", nil); w.Code != 404 {
		t.Fatalf("other owner's connection: %d", w.Code)
	}
	w := call("owner", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data: done") {
		t.Fatalf("stream: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Renewed-Token") != "" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("remote response credentials escaped")
	}
	c, err := desktopConnectionFor("owner", "saved")
	if err != nil || c.Token != "remote-renewed" {
		t.Fatal("remote renewal not saved")
	}
	path, _ := desktopConnectionFile("owner")
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "remote-renewed") {
		t.Fatal("unencrypted token on disk")
	}
}

// Exercise the actual code exchange and a real loopback callback. Neither the
// renderer's list nor the browser's callback receives the remote JWT.
func TestDesktopConnectionBrowserHandoffAndOneUseVerifier(t *testing.T) {
	desktopTestEnvironment(t)
	api := &StreamingAPI{}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.handleDesktopConnectExchange(w, r) }))
	defer remote.Close()
	w := httptest.NewRecorder()
	api.handleDesktopConnections(w, desktopTestRequest("POST", "/api/desktop-connections", `{"name":"Team","server":"`+remote.URL+`"}`, "owner"))
	if w.Code != 200 {
		t.Fatalf("begin: %d %s", w.Code, w.Body.String())
	}
	var start map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &start)
	consent, _ := url.Parse(start["url"])
	approved := httptest.NewRecorder()
	api.handleDesktopConnect(approved, desktopTestRequest("POST", "/api/auth/desktop/connect", `{"challenge":"`+consent.Query().Get("challenge")+`"}`, "remote-user"))
	var grant DesktopConnectResponse
	_ = json.Unmarshal(approved.Body.Bytes(), &grant)
	link, _ := url.Parse(grant.ConnectURL)
	callback, _ := url.Parse(consent.Query().Get("callback"))
	query := callback.Query()
	query.Set("state", start["state"])
	query.Set("code", link.Query().Get("code"))
	callback.RawQuery = query.Encode()
	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("callback: %d %s", resp.StatusCode, raw)
	}
	list := httptest.NewRecorder()
	api.handleDesktopConnections(list, desktopTestRequest("GET", "/api/desktop-connections", "", "owner"))
	if strings.Contains(list.Body.String(), "token") || !strings.Contains(list.Body.String(), "remote-user") {
		t.Fatalf("unsafe/missing inventory: %s", list.Body.String())
	}
	hash := sha256.Sum256([]byte("verifier"))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	storeDesktopConnectCode("proof", &UserClaims{UserID: "remote-user"}, challenge)
	if _, ok := consumeDesktopConnectCode("proof", "wrong"); ok {
		t.Fatal("accepted wrong verifier")
	}
	if _, ok := consumeDesktopConnectCode("proof", "verifier"); ok {
		t.Fatal("code reused")
	}
	storeDesktopConnectCode("valid", &UserClaims{UserID: "remote-user"}, challenge)
	if _, ok := consumeDesktopConnectCode("valid", "verifier"); !ok {
		t.Fatal("valid verifier refused")
	}
}

func TestDesktopConnectionSSEFlushesBeforeCompletion(t *testing.T) {
	desktopTestEnvironment(t)
	finish := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: started\n\n")
		w.(http.Flusher).Flush()
		<-finish
	}))
	defer remote.Close()
	_ = writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: remote.URL, Token: "remote-session"}})
	api := &StreamingAPI{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "127.0.0.1:18743"
		r.Header.Set("Authorization", "Bearer local-session")
		r.Header.Set("X-AgentWorks-Desktop", "app-proof")
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
		api.handleDesktopConnectionProxy(w, mux.SetURLVars(r, map[string]string{"connection": "saved", "path": "api/events"}))
	}))
	defer func() { close(finish); proxy.Close() }()
	resp, err := desktopHTTPClient().Get(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: started\n" {
		t.Fatalf("first event was buffered: %q %v", line, err)
	}
}

// PLAT-746-207: exercise package startup over a real inherited pipe. Unsetenv
// alone cannot hide the initial environment from ps on macOS.
func TestDesktopConnectionProofNeverReachesChildProcesses(t *testing.T) {
	if os.Getenv("DESKTOP_BOOTSTRAP_TEST_CHILD") == "1" {
		if desktopBootstrapSecret != "pipe-proof" || desktopBootstrapHost != "127.0.0.1:18743" || string(GetAuthSecret()) != "pipe-signing-secret" {
			t.Fatal("startup lost private bootstrap")
		}
		InitAuthSecretCache()
		if string(GetAuthSecret()) != "pipe-signing-secret" {
			t.Fatal("cache lost pipe secret")
		}
		for _, value := range os.Environ() {
			if strings.HasPrefix(value, "AGENTWORKS_DESKTOP_") || strings.HasPrefix(value, "AUTH_SECRET=") || strings.HasPrefix(value, "AGENTWORKS_BOOTSTRAP_FD=") {
				t.Fatal("bootstrap inherited by child")
			}
		}
		return
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDesktopConnectionProofNeverReachesChildProcesses$")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "AUTH_SECRET=") && !strings.HasPrefix(value, "AGENTWORKS_DESKTOP_") && !strings.HasPrefix(value, "AGENTWORKS_BOOTSTRAP_FD=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "AGENTWORKS_BOOTSTRAP_FD=3", "DESKTOP_BOOTSTRAP_TEST_CHILD=1")
	cmd.ExtraFiles = []*os.File{read}
	if _, err := io.WriteString(write, `{"secret":"pipe-proof","host":"127.0.0.1:18743","auth_secret":"pipe-signing-secret"}`); err != nil {
		t.Fatal(err)
	}
	write.Close()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap child: %v %s", err, output)
	}
}

// PLAT-746-211: only the Vite origin named in the private bootstrap may use
// saved accounts across origins; another loopback app and missing proof fail.
func TestDesktopConnectionDevelopmentOrigin(t *testing.T) {
	desktopTestEnvironment(t)
	previous := desktopStartup.AppOrigin
	desktopStartup.AppOrigin = "http://127.0.0.1:51733"
	defer func() { desktopStartup.AppOrigin = previous }()
	for _, item := range []struct {
		origin, proof string
		allowed       bool
	}{
		{desktopStartup.AppOrigin, "app-proof", true},
		{"http://127.0.0.1:51734", "app-proof", false},
		{desktopStartup.AppOrigin, "", false},
	} {
		request := desktopTestRequest("GET", "/api/desktop-connections", "", "owner")
		request.Header.Set("Origin", item.origin)
		request.Header.Set("Sec-Fetch-Site", "same-site")
		request.Header.Set("X-AgentWorks-Desktop", item.proof)
		response := httptest.NewRecorder()
		if desktopConnectionGate(response, request) != item.allowed {
			t.Fatal("development origin/proof boundary failed")
		}
	}
}
