package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerProductTokenPathsAreInferenceOnly(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{{"GET", "/api/server-products/llm-gateway/v1/models", true}, {"POST", "/api/server-products/llm-gateway/v1/messages", true}, {"POST", "/api/server-products/llm-gateway/v1/responses", true}, {"POST", "/api/server-products/llm-gateway/admin/model-access", false}, {"GET", "/api/server-products/llm-gateway/ui/", false}, {"DELETE", "/api/server-products/llm-gateway/v1/models", false}, {"POST", "/api/server-products/vault/v1/messages", false}} {
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
}
