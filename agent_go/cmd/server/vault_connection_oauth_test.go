package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	oauth2 "golang.org/x/oauth2"
)

func TestVaultConnectionOAuthAccountsAreIsolated(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", strings.Repeat("k", 32))
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	oauth.SetTokenSealer(platformClientSealer{})
	defer oauth.SetTokenSealer(nil)
	var mu sync.Mutex
	const one = "c-11111111"
	const two = "c-22222222"
	connections := map[string]vaultOAuthConnection{}
	failSync := map[string]bool{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/connectors/"), "/")
		c, ok := connections[parts[0]]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(c)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/oauth/disconnect") {
			c.Status = "authentication_required"
		} else if strings.HasSuffix(r.URL.Path, "/sync") {
			if failSync[c.ID] {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"upstream rejected discovery"}`))
				return
			}
			c.Status = "active"
		} else {
			w.WriteHeader(400)
			return
		}
		connections[c.ID] = c
		w.Write([]byte(`{}`))
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	registrations := 0
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/register" {
			mu.Lock()
			registrations++
			id := registrations
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"client_id":%q,"client_secret":%q}`, fmt.Sprintf("registered-%d", id), fmt.Sprintf("secret-%d", id))
			return
		}
		r.ParseForm()
		code := r.Form.Get("code")
		if r.Form.Get("grant_type") == "refresh_token" {
			code = strings.TrimPrefix(r.Form.Get("refresh_token"), "refresh-")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","refresh_token":%q,"expires_in":3600}`, "account-"+code, "refresh-"+code)
	}))
	defer issuer.Close()
	cfgPath := filepath.Join(t.TempDir(), "mcp.json")
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Test": {URL: "https://example.com/mcp", OAuth: &oauth.OAuthConfig{ClientID: "configured-app", AuthURL: issuer.URL + "/authorize", TokenURL: issuer.URL + "/token", RegistrationEndpoint: issuer.URL + "/register"}}}}
	if err := mcpclient.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{one, two} {
		connections[id] = vaultOAuthConnection{ID: id, OAuthServer: "Test", OAuthCredentialID: id, UpstreamURL: "https://example.com/mcp", Label: id, Status: "authentication_required"}
	}
	eventStore := events.NewEventStore(20)
	defer eventStore.Stop()
	eventStore.SetSessionOwner("vault-fixture-chat", "default")
	api := &StreamingAPI{logger: loggerv2.NewNoop(), mcpConfigPath: cfgPath, eventStore: eventStore}
	signIn := func(id, code string) *OAuthStartResponse {
		t.Helper()
		body, _ := json.Marshal(OAuthLoginRequest{Scope: "vault", ServerName: "Test", ConnectionID: id, SessionID: "vault-fixture-chat"})
		r := httptest.NewRequest("POST", "/api/oauth/start", strings.NewReader(string(body))).WithContext(requestWithUserForSessionAccess("default").Context())
		w := httptest.NewRecorder()
		api.handleOAuthStart(w, r)
		var start OAuthStartResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &start) != nil || start.State == "" {
			t.Fatalf("UI OAuth start: %d %s", w.Code, w.Body.String())
		}
		oauthFlowsMu.RLock()
		flow := oauthFlows[start.State]
		oauthFlowsMu.RUnlock()
		flow.CodeChan <- code
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			oauthFlowsMu.RLock()
			done := flow.Outcome
			oauthFlowsMu.RUnlock()
			if done == "completed" {
				return &start
			}
			if done == "failed" {
				t.Fatal("flow failed")
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("OAuth completion timed out")
		return nil
	}
	signIn(one, "one")
	signIn(two, "two")
	until := time.Now().Add(3 * time.Second)
	for len(eventStore.GetAllEventsRaw("vault-fixture-chat")) < 2 && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	notices := eventStore.GetAllEventsRaw("vault-fixture-chat")
	if len(notices) != 2 || notices[0].Type != "synthetic_turn_ready" || notices[1].Type != "synthetic_turn_ready" || notices[0].ID == notices[1].ID {
		t.Fatalf("OAuth outcomes were not delivered separately to the UI chat: %+v", notices)
	}

	signIn(one, "one")
	until = time.Now().Add(3 * time.Second)
	for len(eventStore.GetAllEventsRaw("vault-fixture-chat")) < 3 && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	repeated := eventStore.GetAllEventsRaw("vault-fixture-chat")
	if len(repeated) != 3 || repeated[0].ID == repeated[2].ID {
		t.Fatal("reconnecting the same account lost its new notification")
	}
	firstCfg, err := api.vaultOAuthConfig(context.Background(), one, "Test", "https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	secondCfg, err := api.vaultOAuthConfig(context.Background(), two, "Test", "https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if firstCfg.OAuth.ClientID == secondCfg.OAuth.ClientID || firstCfg.OAuth.ClientSecret == secondCfg.OAuth.ClientSecret {
		t.Fatal("dynamic client registrations were shared between accounts")
	}

	broker := func(id string) *httptest.ResponseRecorder {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"server_name": "Test", "url": "https://example.com/mcp", "connection_id": id})
		r := httptest.NewRequest("POST", "/internal/caplayer/oauth-token", strings.NewReader(string(payload)))
		r.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		api.handleCapLayerOAuthToken(rec, r)
		return rec
	}
	assertToken := func(id, token string) {
		t.Helper()
		rec := broker(id)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), token) {
			t.Fatalf("isolated token %s: %d", id, rec.Code)
		}
	}
	assertToken(one, "account-one")
	assertToken(two, "account-two")
	status, err := api.capLayerConnectionAccess(context.Background(), "default", "connection_status", json.RawMessage(`{"connection_id":"c-22222222"}`))
	if err != nil || !strings.Contains(status, `"connected":true`) || strings.Contains(status, "account-two") {
		t.Fatal("chat status leaked token or lost account identity", err)
	}
	if _, err := api.capLayerConnectionAccess(context.Background(), "default", "connection_status", json.RawMessage(`{"connection_id":"c-22222222","token_file":"/tmp/other"}`)); err == nil {
		t.Fatal("chat accepted a credential path")
	}

	for _, id := range []string{one, two} {
		for _, path := range []string{vaultCredentialConfigPath(id), getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)), getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id))} {
			data, e := os.ReadFile(path)
			if e != nil || strings.HasPrefix(strings.TrimSpace(string(data)), "{") || strings.Contains(string(data), "account-") {
				t.Fatal("credential was not sealed")
			}
		}
	}
	// Refresh one account; the other account and its refresh identity stay unchanged.
	tokenPath := getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(one))
	if err := oauth.NewTokenStore(tokenPath).Save(&oauth2.Token{AccessToken: "expired", RefreshToken: "refresh-one", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	assertToken(one, "account-one")
	assertToken(two, "account-two")
	t.Setenv("PUBLIC_URL", "http://localhost")
	// Regression: a second click after a finished sign-in must not undo it, and a sign-in whose
	// tools could not be loaded says why instead of only "Needs sign-in".
	strayReply, err := api.capLayerConnectionAccess(context.Background(), "default", "sign_in_connection", json.RawMessage(`{"connection_id":"c-11111111"}`))
	stray := &OAuthStartResponse{}
	if err != nil || json.Unmarshal([]byte(strayReply), stray) != nil || stray.State == "" {
		t.Fatal("second sign-in did not start", err)
	}
	mu.Lock()
	stillActive := connections[one].Status == "active"
	failSync[one] = true
	mu.Unlock()
	if !stillActive {
		t.Fatal("starting a second sign-in signed the finished one out")
	}
	assertToken(one, "account-one")
	oauthFlowsMu.RLock()
	strayFlow := oauthFlows[stray.State]
	oauthFlowsMu.RUnlock()
	strayFlow.CodeChan <- "stray"
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); time.Sleep(5 * time.Millisecond) {
		oauthFlowsMu.RLock()
		done := strayFlow.Outcome
		oauthFlowsMu.RUnlock()
		if done != "" {
			break
		}
	}
	shown := string(withVaultSignInErrors([]byte(`{"connections":[{"id":"c-11111111","status":"authentication_required"}]}`)))
	if !strings.Contains(shown, "could not load its tools") || !strings.Contains(shown, "upstream rejected discovery") {
		t.Fatal("failed post-sign-in sync was not shown with its reason:", shown)
	}
	mu.Lock()
	failSync[one] = false
	mu.Unlock()
	// An older flow left open (the first click's link) that times out after a later success shows no error.
	opened := time.Now()
	recordVaultSignInOutcome("c-44444444", opened, true, "")
	recordVaultSignInOutcome("c-44444444", opened.Add(-time.Second), false, "the user did not complete authorization within 5 minutes")
	if shown := string(withVaultSignInErrors([]byte(`{"connections":[{"id":"c-44444444","status":"authentication_required"}]}`))); strings.Contains(shown, "sign_in_error") {
		t.Fatal("an abandoned older flow overrode a completed sign-in:", shown)
	}
	// Starting a replacement flow must not immediately report the old token valid.
	reply, err := api.capLayerConnectionAccess(context.Background(), "default", "sign_in_connection", json.RawMessage(`{"connection_id":"c-11111111"}`))
	if err != nil {
		t.Fatal(err)
	}
	start := &OAuthStartResponse{}
	if json.Unmarshal([]byte(reply), start) != nil || start.State == "" || start.AuthURL == "" || strings.Contains(reply, "account-one") {
		t.Fatal("chat sign-in did not return a safe link")
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/oauth/status?state="+start.State, nil)
	api.vaultConnectionOAuthStatus(rec, r, one, "Test")
	if !strings.Contains(rec.Body.String(), `"valid":false`) {
		t.Fatal("old token completed a new sign-in")
	}
	if err := api.logoutVaultConnection(context.Background(), "default", one, "Test"); err != nil {
		t.Fatal(err)
	}
	oauthFlowsMu.RLock()
	flow := oauthFlows[start.State]
	oauthFlowsMu.RUnlock()
	flow.CodeChan <- "replacement"
	until = time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		oauthFlowsMu.RLock()
		done := flow.Outcome
		oauthFlowsMu.RUnlock()
		if done == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rec := broker(one); rec.Code == 200 {
		t.Fatal("cancelled sign-in restored a logged-out credential")
	}
	assertToken(two, "account-two")
	// A forged ID never falls back to the provider's legacy/shared account.
	if rec := broker("c-33333333"); rec.Code == 200 {
		t.Fatal("unknown account reused credentials")
	}
	if rec := broker("../Test"); rec.Code == 200 {
		t.Fatal("untrusted credential path accepted")
	}
	mu.Lock()
	delete(connections, two)
	mu.Unlock()
	if rec := broker(two); rec.Code == 200 {
		t.Fatal("deleted connector still returned credentials")
	}
}

func TestVaultConnectionChatRechecksAdministratorAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin","products":[]},{"id":"member","role":"creator","products":["mcp-gateway"]}]}`)
	api := &StreamingAPI{}
	for _, user := range []string{"", "member", "unknown"} {
		for _, op := range []string{"sign_in_connection", "connection_status", "sync_connection", "disconnect_connection"} {
			if _, err := api.capLayerConnectionAccess(context.Background(), user, op, json.RawMessage(`{"connection_id":"c-11111111"}`)); err == nil {
				t.Fatal("non-admin gained connection access", user, op)
			}
		}
	}
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	if _, err := api.capLayerConnectionAccess(context.Background(), "admin", "sign_in_connection", json.RawMessage(`{"connection_id":"c-11111111"}`)); err == nil {
		t.Fatal("revoked admin retained sign-in authority")
	}
}
