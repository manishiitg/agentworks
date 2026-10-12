package server

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestServerProductBrowserSessionPurposeAndRevocation(t *testing.T) {
	t.Setenv("AUTH_SECRET", strings.Repeat("j", 32))
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "llm-gateway")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "llm-gateway")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"admin","products":["llm-gateway"]}]}`)
	calls := 0
	forwardedAdmin := ""
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		forwardedAdmin = r.Header.Get("X-AgentWorks-Admin")
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 32) || r.Header.Get("Cookie") != "" || r.Header.Get("X-AgentWorks-Actor") != "alice" {
			t.Error("browser authority escaped product proxy", r.Header)
		}
		w.Write([]byte(`{}`))
	}))
	defer up.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", up.URL)
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN", strings.Repeat("p", 32))
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/logout" {
			api.handleLogout(w, r)
			return
		}
		api.handleServerProduct(w, r)
	}))
	mint := func(exp time.Time, scope string) string {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &UserClaims{UserID: "alice", Username: "alice", Scope: scope, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(exp)}}).SignedString(GetAuthSecret())
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	expiry := time.Now().Add(5 * time.Minute)
	parent := mint(expiry, "")
	defer revokeServerProductUISessions(parent)
	request := func(method, path, bearer string, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	bootstrap := func(token string) *http.Cookie {
		w := request("POST", "/api/server-products/llm-gateway/session", token, nil, map[string]string{"X-AgentWorks-Product-Session": "1"})
		if w.Code != 204 || len(w.Result().Cookies()) != 1 {
			t.Fatalf("bootstrap %d %s", w.Code, w.Body)
		}
		cookie := w.Result().Cookies()[0]
		if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/server-products/llm-gateway/" || cookie.Expires.After(expiry) || cookie.Value == token {
			t.Fatal("unconfined browser session", cookie)
		}
		return cookie
	}
	cookie := bootstrap(parent)
	for _, path := range []string{"ui/", "ui/app.js", "me", "models", "usage", "admin/models", "admin/audit"} {
		if w := request("GET", "/api/server-products/llm-gateway/"+path, "", cookie, nil); w.Code != 200 {
			t.Fatalf("read %s: %d %s", path, w.Code, w.Body)
		}
	}
	if forwardedAdmin != "1" {
		t.Fatal("current product read role lost")
	}
	before := calls
	for _, path := range []string{"/api/users", externalMCPPath, "/api/server-products/other/ui/", "/api/server-products/llm-gateway/v1/models", "/api/server-products/llm-gateway/admin/users", "/api/server-products/llm-gateway/management/tools", "/api/server-products/llm-gateway/session"} {
		if w := request("GET", path, "", cookie, nil); w.Code < 400 {
			t.Fatal("cookie escaped purpose", path, w.Code)
		}
	}
	if w := request("POST", "/api/server-products/llm-gateway/admin/model-access", "", cookie, nil); w.Code < 400 {
		t.Fatal("cookie mutated permissions")
	}
	if w := request("POST", "/api/server-products/llm-gateway/models", "", cookie, map[string]string{"Sec-Fetch-Site": "same-origin"}); w.Code < 400 {
		t.Fatal("cookie accepted mutation method")
	}
	for _, headers := range []map[string]string{{"Sec-Fetch-Site": "cross-site"}, {"Sec-Fetch-Site": "same-site"}, {"Origin": "https://evil.example"}} {
		if w := request("GET", "/api/server-products/llm-gateway/usage", "", cookie, headers); w.Code != 403 {
			t.Fatal("cross-origin cookie read", w.Code)
		}
	}
	if w := request("GET", "/api/server-products/llm-gateway/me", cookie.Value, nil, nil); w.Code != 401 {
		t.Fatal("opaque cookie accepted as bearer")
	}
	if w := request("GET", "/api/server-products/llm-gateway/me", mint(expiry, serverProductUIScope("llm-gateway")), nil, nil); w.Code != 403 {
		t.Fatal("signed scope accepted as server session")
	}
	if w := request("POST", "/api/server-products/llm-gateway/session?token="+parent, "", nil, map[string]string{"X-AgentWorks-Product-Session": "1"}); w.Code < 400 || len(w.Result().Cookies()) != 0 {
		t.Fatal("query token bootstrapped cookie")
	}
	if calls != before {
		t.Fatal("rejected session reached private service")
	}
	*directory = `{"users":[{"id":"alice","username":"alice","role":"viewer","products":["llm-gateway"]}]}`
	invalidateUserDirectoryCache()
	if w := request("GET", "/api/server-products/llm-gateway/admin/audit", "", cookie, nil); w.Code != 403 {
		t.Fatal("revoked role retained")
	}
	if w := request("GET", "/api/server-products/llm-gateway/models", "", cookie, nil); w.Code != 200 || forwardedAdmin != "" {
		t.Fatal("read-only account retained admin assertion")
	}
	*directory = `{"users":[{"id":"alice","username":"alice","role":"viewer","products":["work"]}]}`
	invalidateUserDirectoryCache()
	if w := request("GET", "/api/server-products/llm-gateway/models", "", cookie, nil); w.Code != 403 {
		t.Fatal("revoked product retained")
	}
	*directory = `{"users":[{"id":"alice","username":"alice","role":"admin","products":["llm-gateway"],"disabled":true}]}`
	invalidateUserDirectoryCache()
	if w := request("GET", "/api/server-products/llm-gateway/me", "", cookie, nil); w.Code != 403 {
		t.Fatal("disabled person retained session")
	}
	*directory = `{"users":[{"id":"alice","username":"alice","role":"admin","products":["llm-gateway"]}]}`
	invalidateUserDirectoryCache()
	request("POST", "/api/auth/logout", parent, nil, nil)
	if w := request("GET", "/api/server-products/llm-gateway/me", "", cookie, nil); w.Code != 401 {
		t.Fatal("logout retained cookie")
	}
	cookie = bootstrap(parent)
	key := sha256.Sum256([]byte(cookie.Value))
	serverProductBrowserSessions.Lock()
	entry := serverProductBrowserSessions.entries[key]
	entry.expires = time.Now().Add(-time.Second)
	serverProductBrowserSessions.entries[key] = entry
	serverProductBrowserSessions.Unlock()
	if w := request("GET", "/api/server-products/llm-gateway/me", "", cookie, nil); w.Code != 401 {
		t.Fatal("expired cookie retained")
	}
	cookie = bootstrap(parent)
	serverProductBrowserSessions.Lock()
	entry = serverProductBrowserSessions.entries[sha256.Sum256([]byte(cookie.Value))]
	entry.parentToken = mint(time.Now().Add(-time.Second), "")
	serverProductBrowserSessions.entries[sha256.Sum256([]byte(cookie.Value))] = entry
	serverProductBrowserSessions.Unlock()
	if w := request("GET", "/api/server-products/llm-gateway/me", "", cookie, nil); w.Code != 401 {
		t.Fatal("expired parent retained")
	}
}
