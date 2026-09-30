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

// A catalog server without dynamic registration (Google, GitHub) is added to
// a Code as its owner's own connection; the client they enter is kept sealed
// and used for the sign-in and every later refresh, and Google's
// offline-access parameters reach the authorization URL. Someone else cannot
// add to, or resolve, that Code's connection.
func TestCodeConnectionFromCatalogWithOwnClient(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	withMCPConnectionsRoot(t)
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	catalog := `{"mcpServers":{
		"AcmeMail":{"url":"https://mcp.acme.example/mcp","protocol":"http","oauth":{"auth_url":"https://auth.acme.example/oauth/authorize","token_url":"https://auth.acme.example/oauth/token","scopes":["https://www.googleapis.com/auth/gmail.readonly"],"extra_auth_params":{"access_type":"offline","prompt":"consent"}}},
		"Keyed":{"url":"https://mcp.example.com/mcp","headers":{"Authorization":"Bearer ${KEY}"}},
		"Local":{"command":"npx","args":["x"]}}}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = catalogPath
	api.logger = loggerv2.NewNoop()
	const logical = "Chats/Code/projects/app-c0de0001"
	root := codePrivacyOwnerRoot
	store := placeMCPStoreID("owner", root)

	rec := personalRoute(api, (*StreamingAPI).handlePlaceMCPCatalog, http.MethodGet, "/x", "", "owner", nil)
	var listed struct {
		Servers []placeMCPCatalogServer `json:"servers"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Servers) != 1 || listed.Servers[0].Name != "acmemail" || !listed.Servers[0].NeedsClient {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	add := func(user, body string) *httptest.ResponseRecorder {
		return personalRoute(api, (*StreamingAPI).handleAddPlaceMCP, http.MethodPost, "/x", body, user, nil)
	}
	if rec := add("owner", `{"workspace_path":"`+logical+`","catalog":"Keyed"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("header-credential catalog server added: %d", rec.Code)
	}
	// Only the Code's owner connects to it (a logical path is always the
	// caller's own tree, so the owner's Code is named by its physical path).
	if rec := add("other", `{"workspace_path":"`+root+`","catalog":"AcmeMail","name":"gmail"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("another person added to the owner's Code: %d %s", rec.Code, rec.Body.String())
	}
	if rec := add("owner", `{"workspace_path":"`+logical+`","catalog":"AcmeMail","name":"gmail"}`); rec.Code != http.StatusOK {
		t.Fatalf("add from catalog = %d %s", rec.Code, rec.Body.String())
	}
	if servers, _ := listPlaceMCPServers(store); len(servers) != 1 || servers[0].Catalog != "AcmeMail" {
		t.Fatalf("stored servers = %+v", servers)
	}
	if attached, _ := placeMCPAttachmentsFor(root); len(attached) != 1 || attached[0].Owner != "owner" || attached[0].Server != "gmail" {
		t.Fatalf("attachments = %+v", attached)
	}

	connect := func(body string) *httptest.ResponseRecorder {
		return personalRoute(api, (*StreamingAPI).handleConnectPlaceMCP, http.MethodPost, "/x?workspace_path="+logical, body, "owner", map[string]string{"name": "gmail"})
	}
	if rec := connect(`{}`); !strings.Contains(rec.Body.String(), "needs_client_id") || !strings.Contains(rec.Body.String(), "/api/oauth/callback") {
		t.Fatalf("connect without a client = %s", rec.Body.String())
	}
	rec = connect(`{"client_id":"cid.apps.googleusercontent.com","client_secret":"shh-owner"}`)
	var started struct {
		AuthURL string `json:"auth_url"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &started) != nil || !strings.Contains(started.AuthURL, "client_id=cid.apps.googleusercontent.com") || !strings.Contains(started.AuthURL, "access_type=offline") || !strings.Contains(started.AuthURL, "prompt=consent") {
		t.Fatalf("connect with a client = %s", rec.Body.String())
	}

	// The entered client is kept only once the sign-in succeeds.
	if _, cfg, err := placeMCPServerConfig(store, "gmail"); err != nil || cfg.OAuth.ClientID != "" {
		t.Fatalf("client kept before sign-in: %+v, %v", cfg.OAuth, err)
	}
	if err := writePlaceMCPClient(store, "gmail", registeredClient{ClientID: "cid.apps.googleusercontent.com", ClientSecret: "shh-owner"}); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := placeMCPServerConfig(store, "gmail")
	if err != nil || cfg.OAuth == nil || cfg.OAuth.ClientID != "cid.apps.googleusercontent.com" || cfg.OAuth.ClientSecret != "shh-owner" || cfg.OAuth.ExtraAuthParams["access_type"] != "offline" {
		t.Fatalf("runtime config = %+v, %v", cfg.OAuth, err)
	}
	dir, _ := placeMCPDir(store)
	stored, _ := os.ReadFile(filepath.Join(dir, "servers.json"))
	clientFile, _ := os.ReadFile(placeMCPClientFile(dir, store, "gmail"))
	if strings.Contains(string(stored), "shh-owner") || strings.Contains(string(clientFile), "shh-owner") {
		t.Fatalf("client secret stored in the clear")
	}
	if _, other, err := placeMCPServerConfig(placeMCPStoreID("other", root), "gmail"); err == nil {
		t.Fatalf("another store resolved the Code's connection: %+v", other)
	}
	// Adding the name again starts clean: the old client never reaches
	// whatever the name points at now.
	if rec := add("owner", `{"workspace_path":"`+logical+`","catalog":"AcmeMail","name":"gmail"}`); rec.Code != http.StatusOK {
		t.Fatalf("re-add = %d %s", rec.Code, rec.Body.String())
	}
	if _, cfg, err := placeMCPServerConfig(store, "gmail"); err != nil || cfg.OAuth.ClientID != "" {
		t.Fatalf("old client survived a re-add: %+v, %v", cfg.OAuth, err)
	}
}
