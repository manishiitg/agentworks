package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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
	for _, tc := range []struct{ name, serverName, wantName string }{
		{"branded", "Example Team", "Example Team"},
		{"unbranded", "", ""},
		{"control-only", "\n\t", ""},
		{"bounded-unicode", "\n" + strings.Repeat("界", 100), strings.Repeat("界", 80)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desktopTestEnvironment(t)
			t.Setenv("AGENTWORKS_APP_NAME", tc.serverName)
			api := &StreamingAPI{}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.handleDesktopConnectExchange(w, r) }))
			defer remote.Close()
			w := httptest.NewRecorder()
			api.handleDesktopConnections(w, desktopTestRequest("POST", "/api/desktop-connections", `{"server":"`+remote.URL+`/crew/projects/example?view=context#details"}`, "owner"))
			if w.Code != 200 {
				t.Fatalf("begin: %d %s", w.Code, w.Body.String())
			}
			var start map[string]string
			_ = json.Unmarshal(w.Body.Bytes(), &start)
			consent, _ := url.Parse(start["url"])
			if consent.Path != "/desktop/connect" || consent.Query().Has("view") || consent.Fragment != "" {
				t.Fatalf("pasted page leaked into sign-in URL: %s", consent)
			}
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
			var listed struct {
				Connections []desktopConnection `json:"connections"`
			}
			if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
				t.Fatal(err)
			}
			wantName := tc.wantName
			if wantName == "" {
				u, _ := url.Parse(remote.URL)
				wantName = u.Hostname()
			}
			if len(listed.Connections) != 1 || listed.Connections[0].Name != wantName || listed.Connections[0].Server != remote.URL {
				t.Fatalf("connection did not derive server name: %+v", listed.Connections)
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
		})
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

// PLAT-746-213: only the Vite origin named in the private bootstrap may use
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

// PLAT-746: pasted page URLs connect to the origin; credentials and insecure remote URLs stay refused.
func TestDesktopServerURLAcceptsBrowserLinks(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{" https://example.com/crew/projects/example?view=context#details ", "https://example.com"},
		{"https://example.com:8443/path%2Ffile?", "https://example.com:8443"},
		{"https://example.com/", "https://example.com"},
		{"http://localhost:18743/crew?view=context", "http://localhost:18743"},
		{"https://person:password@example.com/crew", ""},
		{"http://example.com/crew", ""},
		{"javascript:alert(1)", ""},
		{"https://:443/crew", ""},
	} {
		got, err := desktopServerURL(tc.input)
		if tc.want == "" {
			if err == nil {
				t.Errorf("accepted invalid server %q", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("refused browser link %q: %v", tc.input, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("origin = %q, want %q", got.String(), tc.want)
		}
	}
}

// PLAT-746-865: server Crew links must open the member's active Mac copy,
// without resolving another person's copy or a different saved connection.
func TestDesktopMemberCrewResolvesOnlyOwnActiveCopy(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	const local, remote, owner = "member-local", "team-online", "member"
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: local, ProjectID: local, OwnerID: owner}); err != nil {
		t.Fatal(err)
	}
	if err := writeDesktopConnections(owner, []desktopConnection{{ID: "saved", Server: "https://example.com", User: UserInfo{ID: "remote-member"}}}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeamState(local, func(s *crewTeamLocalState) { s.MemberOf = remote }); err != nil {
		t.Fatal(err)
	}
	binding := crewroot.SyncBinding{Mode: "relay", PairID: "member-pair", Side: "local", Editing: "local", ConnectionID: "saved", PeerProjectID: remote}
	if err := crewroot.WriteSync(local, binding); err != nil {
		t.Fatal(err)
	}
	// A real join receives a pair ID from the server. ReadSync rejects missing
	// pairs, so verify the fixture before exercising route admission.
	if got, err := crewroot.ReadSync(local); err != nil || got == nil {
		t.Fatalf("member pairing fixture unreadable: %v", err)
	}
	api := &StreamingAPI{}
	call := func(person, connection, project string, proof bool) *httptest.ResponseRecorder {
		r := desktopTestRequest("GET", "/api/desktop-connections/"+connection+"/member-crew/"+project, "", person)
		r = mux.SetURLVars(r, map[string]string{"connection": connection, "project": project})
		if !proof {
			r.Header.Del("X-AgentWorks-Desktop")
		}
		w := httptest.NewRecorder()
		api.handleDesktopMemberCrew(w, r)
		return w
	}
	for _, c := range []struct{ person, connection, project, want string }{
		{owner, "saved", remote, local}, {owner, "local", remote, local},
		{"outsider", "saved", remote, ""}, {owner, "another", remote, ""}, {owner, "saved", local, ""},
	} {
		w := call(c.person, c.connection, c.project, true)
		var got map[string]string
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got["project_id"] != c.want {
			t.Fatalf("resolve %+v: %d %s", c, w.Code, w.Body.String())
		}
	}
	if w := call(owner, "saved", remote, false); w.Code != 403 {
		t.Fatalf("non-desktop caller admitted: %d", w.Code)
	}

	binding.Editing = "server"
	if err := crewroot.ReplaceSync(local, binding); err != nil {
		t.Fatal(err)
	}
	if got := desktopMemberCrew(owner, "saved", remote); got != "" {
		t.Fatalf("web copy redirected to Mac: %s", got)
	}
}

// PLAT-746-905: the quick switcher shows one row per Crew. A server Crew that
// is the online copy of one of this person's own paired Mac Crews is skipped.
func TestDesktopInventorySkipsOwnOnlineCopy(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	const macLocal, online, other, owner = "own-mac-crew", "online-copy", "other-crew", "owner"
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: macLocal, ProjectID: macLocal, OwnerID: owner}); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/workspaces") {
			_, _ = io.WriteString(w, `{"workspaces":[{"id":"`+online+`","name":"My Crew"},{"id":"`+other+`","name":"Other Crew"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"workflows":[]}`)
	}))
	defer remote.Close()
	if err := writeDesktopConnections(owner, []desktopConnection{{ID: "saved", Server: remote.URL, Token: "remote-session"}}); err != nil {
		t.Fatal(err)
	}
	if err := crewroot.WriteSync(macLocal, crewroot.SyncBinding{Mode: "relay", PairID: "own-pair", Side: "local", Editing: "local", ConnectionID: "saved", PeerProjectID: online}); err != nil {
		t.Fatal(err)
	}
	if got, err := crewroot.ReadSync(macLocal); err != nil || got == nil {
		t.Fatalf("own pairing fixture unreadable: %v", err)
	}
	api := &StreamingAPI{}
	r := desktopTestRequest("GET", "/api/desktop-connections/saved/inventory", "", owner)
	r = mux.SetURLVars(r, map[string]string{"connection": "saved"})
	w := httptest.NewRecorder()
	api.handleDesktopConnectionInventory(w, r)
	if w.Code != 200 {
		t.Fatalf("inventory: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Projects []desktopProject `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Projects {
		if p.ID == online {
			t.Fatalf("online copy listed alongside own Mac Crew: %s", w.Body.String())
		}
	}
	found := false
	for _, p := range got.Projects {
		if p.ID == other {
			found = true
		}
	}
	if !found {
		t.Fatalf("unpaired Crew missing: %s", w.Body.String())
	}
}

// #2831: the real saved-connection proxy must upgrade and tunnel binary/control
// frames without forwarding the local JWT, app proof, cookies or identity.
func TestDesktopProjectTerminalWebSocketKeepsRemoteRouteAndCredentials(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("MULTI_USER_MODE", "false")
	localToken, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent-profiles/work/projects/project/shell/stream" || r.URL.Query().Has("token") || r.Header.Get("Authorization") != "Bearer remote-session" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || r.Header.Get("X-AgentWorks-Desktop") != "" || r.Header.Get("X-User-ID") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			t.Error("desktop credential boundary or selected route lost")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		pong := false
		conn.SetPongHandler(func(string) error { pong = true; return nil })
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
			t.Error(err)
			return
		}
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		if !pong || kind != websocket.BinaryMessage || string(data) != "terminal input" {
			t.Error("WebSocket data/control frames did not survive the proxy")
		}
		_ = conn.WriteMessage(kind, data)
	}))
	defer remote.Close()
	if err := writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: remote.URL, Token: "remote-session"}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	local := httptest.NewServer(AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the bound loopback port is substituted in this fixture. JWT
		// authentication, app proof, source selection and upgrade all run.
		r.Host = desktopBootstrapHost
		r = mux.SetURLVars(r, map[string]string{"connection": "saved", "path": strings.TrimPrefix(r.URL.Path, "/api/connections/saved/")})
		api.handleDesktopConnectionProxy(w, r)
	})))
	defer local.Close()
	target := "ws" + strings.TrimPrefix(local.URL, "http") + "/api/connections/saved/api/agent-profiles/work/projects/project/shell/stream?token=" + url.QueryEscape(localToken)
	header := make(http.Header)
	for key, value := range map[string]string{"X-AgentWorks-Desktop": "app-proof", "Origin": "http://" + desktopBootstrapHost, "Cookie": "local-cookie=private", "X-User-ID": "forged", "Sec-Fetch-Site": "same-origin"} {
		header.Set(key, value)
	}
	for _, change := range []func(http.Header){
		func(h http.Header) { h.Del("X-AgentWorks-Desktop") },
		func(h http.Header) { h.Set("Origin", "https://example.com") },
	} {
		h := header.Clone()
		change(h)
		conn, response, err := websocket.DefaultDialer.Dial(target, h)
		if conn != nil {
			conn.Close()
		}
		if err == nil || response == nil || response.StatusCode != 403 {
			t.Fatal("socket bypassed the app-frame proof gate")
		}
		response.Body.Close()
	}
	for _, identity := range []struct {
		token  string
		status int
	}{
		{"invalid-token", http.StatusUnauthorized},
	} {
		badTarget := strings.Replace(target, url.QueryEscape(localToken), url.QueryEscape(identity.token), 1)
		conn, response, err := websocket.DefaultDialer.Dial(badTarget, header)
		if conn != nil {
			conn.Close()
		}
		if err == nil || response == nil || response.StatusCode != identity.status {
			t.Fatal("invalid query credential reached the saved session")
		}
		response.Body.Close()
	}
	otherToken, err := GenerateJWT("other-owner", "other", "")
	if err != nil {
		t.Fatal(err)
	}
	otherTarget := strings.Replace(target, url.QueryEscape(localToken), url.QueryEscape(otherToken), 1)
	other, denied, err := websocket.DefaultDialer.Dial(otherTarget, header)
	if other != nil {
		other.Close()
	}
	if err == nil || denied == nil || denied.StatusCode != http.StatusNotFound {
		t.Fatal("another owner reached the saved server")
	}
	denied.Body.Close()
	conn, response, err := websocket.DefaultDialer.Dial(target, header)
	if err != nil {
		if response != nil {
			t.Fatalf("upgrade status %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	// Read processes the tunneled ping before writing, so the upstream can
	// assert it received the browser's automatic pong before the binary frame.
	conn.SetPingHandler(func(data string) error {
		if err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second)); err != nil {
			return err
		}
		return conn.WriteMessage(websocket.BinaryMessage, []byte("terminal input"))
	})
	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(data) != "terminal input" {
		t.Fatalf("terminal echo: kind=%d err=%v", kind, err)
	}
}

// #3383: the relay passes query sign-in only for a server project's Terminal and live browser view streams; any
// other socket or a plain request to the same path is refused it.
func TestDesktopRelayAdmitsOnlyProjectStreams(t *testing.T) {
	for path, want := range map[string]bool{
		"api/agent-profiles/work/projects/project/shell/stream": true,
		"api/browser/live/session/stream":                       true,
		"api/browser/live/session/recording":                    false,
		"api/browser/live/a/b/stream":                           false,
		"api/sessions/session/stream":                           false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/connections/saved/"+path, nil)
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		if got := desktopProjectStreamUpgrade(mux.SetURLVars(r, map[string]string{"path": path})); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
	plain := httptest.NewRequest(http.MethodGet, "/api/connections/saved/api/browser/live/session/stream", nil)
	if desktopProjectStreamUpgrade(mux.SetURLVars(plain, map[string]string{"path": "api/browser/live/session/stream"})) {
		t.Error("a plain request was treated as a stream")
	}
}
