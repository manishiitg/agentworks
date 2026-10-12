package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestServerProductTokenPathsAreInferenceOnly(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{{"GET", "/api/server-products/llm-gateway/v1/models", true}, {"POST", "/api/server-products/llm-gateway/v1/messages", true}, {"POST", "/api/server-products/llm-gateway/v1/responses", true}, {"POST", "/api/server-products/llm-gateway/admin/model-access", false}, {"POST", "/api/server-products/llm-gateway/session", false}, {"GET", "/api/server-products/llm-gateway/management/tools", false}, {"GET", "/api/server-products/llm-gateway/ui/", false}, {"DELETE", "/api/server-products/llm-gateway/v1/models", false}, {"POST", "/api/server-products/vault/v1/messages", false}} {
		if got := serverProductTokenPath(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
func TestOrdinaryLocalRejectsServerProductProxy(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	w := httptest.NewRecorder()
	(&StreamingAPI{}).handleServerProduct(w, httptest.NewRequest("GET", "/api/server-products/llm-gateway/me", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestServerProductProxyUsesCurrentRoleAndReplacesCredentials(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "llm-gateway")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "llm-gateway,mcp-gateway")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin","products":["llm-gateway"]},{"id":"member","username":"member","role":"creator","products":["llm-gateway"]}]}`)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/admin/products/llm-gateway/admin/models" || r.Header.Get("X-AgentWorks-Actor") != "admin" || r.Header.Get("X-AgentWorks-Admin") != "1" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 32) || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("untrusted request forwarded", r.URL, r.Header)
		}
		w.Header().Set("Set-Cookie", "private=secret")
		w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN", strings.Repeat("p", 32))
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	for _, tc := range []struct {
		user string
		want int
	}{{"unknown", 403}, {"member", 403}, {"admin", 200}} {
		req := adminRequest("GET", "/api/server-products/llm-gateway/admin/models", "", &UserClaims{UserID: tc.user, Username: tc.user}, nil)
		req.Header.Set("Authorization", "Bearer browser-secret")
		req.Header.Set("Cookie", "session=browser-secret")
		req.Header.Set("X-AgentWorks-Actor", "spoofed")
		req.Header.Set("X-AgentWorks-Admin", "1")
		req.Header.Set("Origin", "https://browser.example")
		rec := httptest.NewRecorder()
		api.handleServerProduct(rec, req)
		if rec.Code != tc.want || rec.Header().Get("Set-Cookie") != "" {
			t.Fatal(tc, rec.Code, rec.Body)
		}
	}
	*directory = `{"users":[{"id":"admin","username":"admin","role":"viewer","products":["llm-gateway"]}]}`
	invalidateUserDirectoryCache()
	rec := httptest.NewRecorder()
	api.handleServerProduct(rec, adminRequest("GET", "/api/server-products/llm-gateway/admin/models", "", &UserClaims{UserID: "admin", Username: "admin"}, nil))
	if rec.Code != 403 || calls != 1 {
		t.Fatal("revoked admin access retained", rec.Code, calls)
	}
}

func TestServerProductProxyRequiresDedicatedTokenAndSecuresUI(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "llm-gateway")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "llm-gateway,mcp-gateway")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin","products":["llm-gateway"]}]}`)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 32) {
			t.Error("wrong service credential")
		}
		w.Header().Set("Set-Cookie", "private=secret")
		w.Header().Set("Access-Control-Allow-Origin", "https://untrusted.example")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", "*")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("v", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN", "")
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.handleServerProduct(w, adminRequest("GET", "/api/server-products/llm-gateway/ui/", "", &UserClaims{UserID: "admin", Username: "admin"}, nil))
		return w
	}
	if w := request(); w.Code != 503 || calls != 0 {
		t.Fatal("Vault admin token used as fallback", w.Code, calls)
	}
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN", strings.Repeat("p", 32))
	w := request()
	if w.Code != 200 || calls != 1 || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("UI request failed", w.Code, calls)
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, rule := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'self'", "sandbox allow-scripts allow-same-origin"} {
		if !strings.Contains(csp, rule) {
			t.Fatalf("missing UI CSP rule %s", rule)
		}
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("UI response policy missing")
	}
	for key := range w.Header() {
		if strings.HasPrefix(strings.ToLower(key), "access-control-") {
			t.Fatalf("product CORS header escaped: %s", key)
		}
	}
}

func TestServerProductLLMOnlyDeploymentAndTokenClientIdentity(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "llm-gateway")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "llm-gateway")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","email":"alice@example.test","role":"admin","products":["llm-gateway"]}]}`)
	clients := []string{}
	bindings := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/users/sync" {
			bindings++
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("v", 32) {
				t.Error("directory sync did not use its separate host credential")
			}
		} else {
			clients = append(clients, r.Header.Get("X-AgentWorks-Client"))
			wantAdmin := ""
			if r.Header.Get("X-AgentWorks-Client") == "platform" {
				wantAdmin = "1"
			}
			if r.Header.Get("X-AgentWorks-Admin") != wantAdmin {
				t.Error("inference token inherited browser administration authority")
			}
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 32) {
				t.Error("product request did not use its product credential")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("v", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN", strings.Repeat("p", 32))
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	for _, id := range []string{"", "one", "two"} {
		claims := &UserClaims{UserID: "alice", Username: "alice"}
		if id != "" {
			claims.AccessToken = &accesstokens.Token{ID: id, Scopes: []string{"llm:use"}}
		}
		w := httptest.NewRecorder()
		api.handleServerProduct(w, adminRequest("GET", "/api/server-products/llm-gateway/v1/models", "", claims, nil))
		if w.Code != 200 {
			t.Fatal("LLM-only discovery unavailable", w.Code, w.Body)
		}
	}
	if strings.Join(clients, ",") != "platform,access-token:one,access-token:two" {
		t.Fatal("token client identity lost", clients)
	}
	w := httptest.NewRecorder()
	api.handleServerProduct(w, adminRequest("GET", "/api/server-products/llm-gateway/admin/users", "", &UserClaims{UserID: "alice", Username: "alice"}, nil))
	if w.Code != 200 || bindings != 1 {
		t.Fatal("LLM-only directory unavailable", w.Code, bindings)
	}
	if _, _, err := capLayerServiceConfig(); err == nil {
		t.Fatal("LLM-only deployment enabled Vault access")
	}
}

func TestServerProductPATUsesUnscopedClaimsWithoutAdminAuthority(t *testing.T) {
	api := tokenTestSetup(t)
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "llm-gateway")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "llm-gateway")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"admin","products":["llm-gateway"]}]}`)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	issued, raw, err := store.Issue(t.Context(), accesstokens.Token{Name: "Inference", UserID: "alice", Scopes: []string{"llm:use"}, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-AgentWorks-Admin") != "" || r.Header.Get("X-AgentWorks-Actor") != "alice" || r.Header.Get("X-AgentWorks-Client") != "access-token:"+issued.ID {
			t.Error("PAT forwarded excessive authority or lost its identity", r.Header)
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN", strings.Repeat("p", 32))
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE", "")
	authenticated := false
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserFromContext(r.Context())
		if claims == nil || claims.Scope != "" || claims.AccessToken == nil || claims.AccessToken.ID != issued.ID {
			t.Fatalf("authenticated PAT did not produce unscoped user claims: %+v", claims)
		}
		authenticated = true
		api.handleServerProduct(w, r)
	}))
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		r.Header.Set("X-AgentWorks-Admin", "1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("/api/server-products/llm-gateway/v1/models"); w.Code != 200 || !authenticated || calls != 1 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("PAT discovery failed or leaked product CORS", w.Code, authenticated, calls, w.Body)
	}
	if w := request("/api/server-products/llm-gateway/admin/models"); w.Code != 403 || calls != 1 {
		t.Fatal("inference PAT reached management", w.Code, calls)
	}
	if err := store.Revoke(t.Context(), issued.ID, "alice", now); err != nil {
		t.Fatal(err)
	}
	if w := request("/api/server-products/llm-gateway/v1/models"); w.Code != 401 || calls != 1 {
		t.Fatal("revoked inference PAT retained access", w.Code, calls)
	}
}
