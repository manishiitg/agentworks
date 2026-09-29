package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func personalRoute(api *StreamingAPI, handler func(*StreamingAPI, http.ResponseWriter, *http.Request), method, target, body, user string, vars map[string]string) *httptest.ResponseRecorder {
	req := mux.SetURLVars(profileRouteRequest(method, target, []byte(body), user), vars)
	rec := httptest.NewRecorder()
	handler(api, rec, req)
	return rec
}

// A person manages only their own servers and secrets; switching one on
// needs access to the Code; listed URLs drop their query string.
func TestPersonalMCPRoutesActOnTheCallerOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	withPersonalMCPRoot(t)
	project := "c0de0001-0000"

	sealed, err := encryptSecretValueWithAAD("sk-owner", []byte("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if rec := personalRoute(api, (*StreamingAPI).handlePutPersonalSecret, http.MethodPut, "/x", `{"encrypted_value":"`+sealed+`"}`, "owner", map[string]string{"name": "SVC_KEY"}); rec.Code != http.StatusOK {
		t.Fatalf("set secret = %d %s", rec.Code, rec.Body.String())
	}
	// A value sealed for someone else does not decrypt for this caller.
	if rec := personalRoute(api, (*StreamingAPI).handlePutPersonalSecret, http.MethodPut, "/x", `{"encrypted_value":"`+sealed+`"}`, "other", map[string]string{"name": "SVC_KEY"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("other stored owner's sealed value: %d", rec.Code)
	}
	add := `{"name":"svc","url":"https://mcp.example.com/mcp?token=abc","headers":{"Authorization":{"secret":"SVC_KEY","format":"Bearer {}"}}}`
	if rec := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", add, "owner", nil); rec.Code != http.StatusOK {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", `{"name":"bad","url":"https://10.0.0.5/mcp"}`, "owner", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("private address accepted: %d", rec.Code)
	}

	switchOn := `{"enabled":true}`
	if rec := personalRoute(api, (*StreamingAPI).handleSwitchPersonalMCP, http.MethodPut, "/x", switchOn, "owner", map[string]string{"name": "svc", "project_id": project}); rec.Code != http.StatusOK {
		t.Fatalf("switch on = %d %s", rec.Code, rec.Body.String())
	}
	// "other" has no access to the owner's Code, and no server named svc.
	if rec := personalRoute(api, (*StreamingAPI).handleSwitchPersonalMCP, http.MethodPut, "/x", switchOn, "other", map[string]string{"name": "svc", "project_id": project}); rec.Code != http.StatusNotFound {
		t.Fatalf("other switched a server on in owner's Code: %d", rec.Code)
	}

	rec := personalRoute(api, (*StreamingAPI).handleListPersonalMCP, http.MethodGet, "/x?code="+project, "", "owner", nil)
	var listed struct {
		Servers []personalMCPServerView `json:"servers"`
		Secrets []string                `json:"secrets"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Servers) != 1 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	got := listed.Servers[0]
	if !got.Enabled || strings.Contains(got.URL, "token") || strings.Contains(rec.Body.String(), "sk-owner") || len(listed.Secrets) != 1 {
		t.Fatalf("listed = %+v %s", got, rec.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleListPersonalMCP, http.MethodGet, "/x", "", "other", nil); !strings.Contains(rec.Body.String(), `"servers":[]`) {
		t.Fatalf("other sees servers: %s", rec.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleRemovePersonalMCP, http.MethodDelete, "/x", "", "other", map[string]string{"name": "svc"}); rec.Code != http.StatusNotFound {
		t.Fatalf("other removed owner's server: %d", rec.Code)
	}
	if rec := personalRoute(api, (*StreamingAPI).handleRemovePersonalMCP, http.MethodDelete, "/x", "", "owner", map[string]string{"name": "svc"}); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d", rec.Code)
	}
}

// A catalog server without dynamic registration (Google, GitHub) is added as
// the person's own; the client they enter is kept sealed and used for the
// sign-in and every later refresh, and Google's offline-access parameters
// reach the authorization URL.
func TestPersonalMCPFromCatalogWithOwnClient(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	withPersonalMCPRoot(t)
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	catalog := `{"mcpServers":{
		"GoogleGmail":{"url":"https://gmailmcp.googleapis.com/mcp/v1","protocol":"http","oauth":{"auth_url":"https://accounts.google.com/o/oauth2/v2/auth","token_url":"https://oauth2.googleapis.com/token","scopes":["https://www.googleapis.com/auth/gmail.readonly"],"extra_auth_params":{"access_type":"offline","prompt":"consent"}}},
		"Keyed":{"url":"https://mcp.example.com/mcp","headers":{"Authorization":"Bearer ${KEY}"}},
		"Local":{"command":"npx","args":["x"]}}}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = catalogPath
	api.logger = loggerv2.NewNoop()

	rec := personalRoute(api, (*StreamingAPI).handlePersonalMCPCatalog, http.MethodGet, "/x", "", "owner", nil)
	var listed struct {
		Servers []personalMCPCatalogServer `json:"servers"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Servers) != 1 || listed.Servers[0].Name != "googlegmail" || !listed.Servers[0].NeedsClient {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", `{"catalog":"Keyed"}`, "owner", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("header-credential catalog server added: %d", rec.Code)
	}
	if rec := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", `{"catalog":"GoogleGmail","name":"gmail"}`, "owner", nil); rec.Code != http.StatusOK {
		t.Fatalf("add from catalog = %d %s", rec.Code, rec.Body.String())
	}

	vars := map[string]string{"name": "gmail"}
	rec = personalRoute(api, (*StreamingAPI).handleConnectPersonalMCP, http.MethodPost, "/x", `{}`, "owner", vars)
	if !strings.Contains(rec.Body.String(), "needs_client_id") || !strings.Contains(rec.Body.String(), "/api/oauth/callback") {
		t.Fatalf("connect without a client = %s", rec.Body.String())
	}
	rec = personalRoute(api, (*StreamingAPI).handleConnectPersonalMCP, http.MethodPost, "/x", `{"client_id":"cid.apps.googleusercontent.com","client_secret":"shh-owner"}`, "owner", vars)
	var started struct {
		AuthURL string `json:"auth_url"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &started) != nil || !strings.Contains(started.AuthURL, "client_id=cid.apps.googleusercontent.com") || !strings.Contains(started.AuthURL, "access_type=offline") || !strings.Contains(started.AuthURL, "prompt=consent") {
		t.Fatalf("connect with a client = %s", rec.Body.String())
	}

	// The entered client is kept only once the sign-in succeeds.
	if _, cfg, err := personalMCPServerConfig("owner", "gmail"); err != nil || cfg.OAuth.ClientID != "" {
		t.Fatalf("client kept before sign-in: %+v, %v", cfg.OAuth, err)
	}
	if err := writePersonalMCPClient("owner", "gmail", registeredClient{ClientID: "cid.apps.googleusercontent.com", ClientSecret: "shh-owner"}); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := personalMCPServerConfig("owner", "gmail")
	if err != nil || cfg.OAuth == nil || cfg.OAuth.ClientID != "cid.apps.googleusercontent.com" || cfg.OAuth.ClientSecret != "shh-owner" || cfg.OAuth.ExtraAuthParams["access_type"] != "offline" {
		t.Fatalf("runtime config = %+v, %v", cfg.OAuth, err)
	}
	dir, _ := personalMCPDir("owner")
	stored, _ := os.ReadFile(filepath.Join(dir, "servers.json"))
	clientFile, _ := os.ReadFile(personalMCPClientFile(dir, "owner", "gmail"))
	if strings.Contains(string(stored), "shh-owner") || strings.Contains(string(clientFile), "shh-owner") {
		t.Fatalf("client secret stored in the clear")
	}
	if _, other, err := personalMCPServerConfig("other", "gmail"); err == nil {
		t.Fatalf("other resolved owner's server: %+v", other)
	}
	// Adding the name again starts clean: the old client never reaches
	// whatever the name points at now.
	if rec := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", `{"catalog":"GoogleGmail","name":"gmail"}`, "owner", nil); rec.Code != http.StatusOK {
		t.Fatalf("re-add = %d %s", rec.Code, rec.Body.String())
	}
	if _, cfg, err := personalMCPServerConfig("owner", "gmail"); err != nil || cfg.OAuth.ClientID != "" {
		t.Fatalf("old client survived a re-add: %+v, %v", cfg.OAuth, err)
	}
}
