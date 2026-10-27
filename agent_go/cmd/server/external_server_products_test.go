package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestExternalServerProductManagementAuthority(t *testing.T) {
	t.Setenv("AUTH_SECRET", strings.Repeat("a", 32))
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "llm-gateway,work")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "llm-gateway,work")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","email":"admin@example.com","role":"admin","products":["llm-gateway"]},{"id":"member","role":"creator","products":["llm-gateway"]}]}`)
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/users/sync" {
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
				t.Error("bad directory credential")
			}
			w.WriteHeader(204)
			return
		}
		calls++
		if r.URL.Path != "/api/admin/products/llm-gateway/management/tools" && r.URL.Path != "/api/admin/products/llm-gateway/management/call" {
			t.Error("arbitrary product route", r.URL)
		}
		if r.Header.Get("X-AgentWorks-Actor") != "admin" || r.Header.Get("X-AgentWorks-Admin") != "1" || r.Header.Get("X-AgentWorks-Client") != "access-token:management" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("p", 32) || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("unverified authority forwarded", r.Header)
		}
		w.Header().Set("Set-Cookie", "bad=cookie")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != `{"arguments":{"expected_revision":1},"name":"set_model_access"}` {
				t.Error("call payload changed", string(raw))
			}
		}
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer up.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", up.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN", strings.Repeat("p", 32))
	t.Setenv("AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	claims := func(user, scope string) *UserClaims {
		return &UserClaims{UserID: user, AccessToken: &accesstokens.Token{ID: "management", Scopes: []string{scope}}}
	}
	call := func(c *UserClaims, args map[string]any) *httptest.ResponseRecorder {
		// The server_product actions are reached through account action=server_tools / server_call (PLAT-825).
		name, input := catalog.Invocation(serverProductManagementTool, args)
		raw, _ := json.Marshal(map[string]any{"name": name, "arguments": input})
		r := adminRequest("POST", "/api/external/v1/call", string(raw), c, nil)
		r.Header.Set("Cookie", "client-cookie")
		r.Header.Set("X-AgentWorks-Actor", "spoofed")
		r.Header.Set("Origin", "https://browser.example")
		w := httptest.NewRecorder()
		api.handleExternalCall(w, r)
		return w
	}
	args := map[string]any{"product": "llm-gateway", "action": "tools"}
	for _, c := range []*UserClaims{claims("admin", "llm:use"), claims("member", "users:manage"), claims("unknown", "users:manage"), {UserID: "admin", Scope: serverProductUIScope("llm-gateway")}} {
		if externalTokenAllows(c, externalTool{Name: serverProductManagementTool}) {
			t.Fatal("management discovered without authority", c)
		}
		if w := call(c, args); w.Code != 403 || calls != 0 {
			t.Fatal("management bypass", w.Code, calls)
		}
	}
	admin := claims("admin", "users:manage")
	if !externalTokenAllows(admin, externalTool{Name: serverProductManagementTool}) {
		t.Fatal("authorized management hidden")
	}
	if w := call(admin, args); w.Code != 200 || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("management discovery failed", w.Code, w.Body)
	}
	if w := call(admin, map[string]any{"product": "llm-gateway", "action": "call", "name": "set_model_access", "arguments": map[string]any{"expected_revision": 1}}); w.Code != 200 || calls != 2 {
		t.Fatal("fixed management call failed", w.Code, w.Body)
	}
	for _, args := range []map[string]any{{"product": "llm-gateway/../../users", "action": "tools"}, {"product": "llm-gateway", "action": "tools", "url": "https://evil.example"}, {"product": "llm-gateway", "action": "call"}, {"product": "llm-gateway", "action": "tools", "name": "get_models"}} {
		if w := call(admin, args); w.Code < 400 || calls != 2 {
			t.Fatal("invalid route reached service", w.Code, calls)
		}
	}
	audit, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	rows, err := audit.db.Query(`SELECT actor_id,client_id,product,tool,status,finished_at FROM server_product_management_audit ORDER BY started_at,id`)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var actor, client, product, tool string
		var status int
		var finished int64
		if err := rows.Scan(&actor, &client, &product, &tool, &status, &finished); err != nil {
			t.Fatal(err)
		}
		if actor != "admin" || client != "access-token:management" || product != "llm-gateway" || (tool != "tools" && tool != "set_model_access") || status != 200 || finished == 0 {
			t.Fatal("bad management receipt", actor, client, product, tool, status, finished)
		}
		count++
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("management calls missing from audit", count)
	}
	// Audit failure must refuse the operation before forwarding.
	stateRoot := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", stateRoot+"/missing-parent-file")
	if err := os.WriteFile(stateRoot+"/missing-parent-file", []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := call(admin, args); w.Code != 503 || calls != 2 {
		t.Fatal("unaudited management forwarded", w.Code, calls)
	}
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["llm-gateway"]}]}`
	invalidateUserDirectoryCache()
	if w := call(admin, args); w.Code != 403 || calls != 2 {
		t.Fatal("revoked role retained", w.Code, calls)
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	if externalTokenAllows(admin, externalTool{Name: serverProductManagementTool}) {
		t.Fatal("disabled local product advertised management")
	}
}
