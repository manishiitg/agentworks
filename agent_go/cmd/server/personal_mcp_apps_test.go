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

// An admin sets up the deployment's Google app once. People then connect any
// Google server with no client ID or secret of their own: the app is read
// live (a rotation reaches everyone), a client a person entered for their own
// app wins, the secret is sealed at rest and never returned, and only admins
// can touch the app.
func TestAdminSignInAppServesEveryonesGoogleConnect(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	api, _ := newCodePrivacyFixture(t)
	withPersonalMCPRoot(t)
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	google := `"oauth":{"auth_url":"https://accounts.google.com/o/oauth2/v2/auth","token_url":"https://oauth2.googleapis.com/token","extra_auth_params":{"access_type":"offline","prompt":"consent"}}`
	catalog := `{"mcpServers":{
		"GoogleGmail":{"url":"https://gmailmcp.googleapis.com/mcp/v1","protocol":"http",` + google + `},
		"GoogleDrive":{"url":"https://drivemcp.googleapis.com/mcp/v1","protocol":"http",` + google + `},
		"Linear":{"url":"https://mcp.linear.app/mcp","oauth":{"auth_url":"https://mcp.linear.app/authorize","token_url":"https://mcp.linear.app/token","registration_endpoint":"https://mcp.linear.app/register"}}}}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = catalogPath
	api.logger = loggerv2.NewNoop()

	list := func() (groups []mcpAppGroup, body string) {
		t.Helper()
		rec := personalRoute(api, (*StreamingAPI).handleListMCPApps, http.MethodGet, "/x", "", "owner", nil)
		var out struct {
			Apps        []mcpAppGroup `json:"apps"`
			RedirectURI string        `json:"redirect_uri"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
		}
		return out.Apps, rec.Body.String()
	}
	put := func(key, body string) int {
		return personalRoute(api, (*StreamingAPI).handlePutMCPApp, http.MethodPut, "/x", body, "owner", map[string]string{"key": key}).Code
	}

	// One Google app for every Google server; a self-registering server needs none.
	groups, _ := list()
	if len(groups) != 1 || groups[0].Key != "google" || groups[0].Configured || strings.Join(groups[0].Servers, ",") != "GoogleDrive,GoogleGmail" {
		t.Fatalf("groups = %+v", groups)
	}
	if code := put("linear", `{"client_id":"x","client_secret":"y"}`); code != http.StatusNotFound {
		t.Fatalf("app for a self-registering server = %d", code)
	}
	if code := put("google", `{"client_id":"","client_secret":"s"}`); code != http.StatusBadRequest {
		t.Fatalf("empty client id = %d", code)
	}
	if code := put("google", `{"client_id":"id","client_secret":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty client secret = %d", code)
	}
	// Only an admin: the directory here has no admin, so the guard refuses.
	guarded := requireAdmin(api.handlePutMCPApp)
	guardedReq := mux.SetURLVars(profileRouteRequest(http.MethodPut, "/x", []byte(`{"client_id":"id","client_secret":"s"}`), "other"), map[string]string{"key": "google"})
	guardedRec := httptest.NewRecorder()
	guarded(guardedRec, guardedReq)
	if guardedRec.Code != http.StatusForbidden {
		t.Fatalf("non-admin set the app: %d", guardedRec.Code)
	}
	if path, _ := mcpAppFile("google"); path != "" {
		if _, err := os.Stat(path); err == nil {
			t.Fatal("a non-admin's request stored an app")
		}
	}

	// Before the app exists, Connect still asks for a client.
	before := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", `{"catalog":"GoogleGmail","name":"gmail"}`, "owner", nil)
	if before.Code != http.StatusOK {
		t.Fatalf("add = %d %s", before.Code, before.Body.String())
	}
	if rec := personalRoute(api, (*StreamingAPI).handleConnectPersonalMCP, http.MethodPost, "/x", `{}`, "owner", map[string]string{"name": "gmail"}); !strings.Contains(rec.Body.String(), "needs_client_id") {
		t.Fatalf("connect without an app = %s", rec.Body.String())
	}

	// The admin sets the app once.
	if code := put("google", `{"client_id":"965.apps.googleusercontent.com","client_secret":"GOCSPX-shared-secret"}`); code != http.StatusOK {
		t.Fatalf("set app = %d", code)
	}
	groups, listBody := list()
	if !groups[0].Configured || groups[0].ClientID != "965.apps.googleusercontent.com" || strings.Contains(listBody, "shared-secret") {
		t.Fatalf("configured group = %+v body %s", groups[0], listBody)
	}
	path, _ := mcpAppFile("google")
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "shared-secret") || strings.Contains(string(raw), "965.apps") {
		t.Fatalf("the app is stored in the clear: %s", raw)
	}
	catalogRec := personalRoute(api, (*StreamingAPI).handlePersonalMCPCatalog, http.MethodGet, "/x", "", "owner", nil)
	if strings.Contains(catalogRec.Body.String(), `"needs_client":true`) {
		t.Fatalf("a configured app still needs a client: %s", catalogRec.Body.String())
	}

	// The server added before the app existed now connects with no client
	// entered: it reads the app live.
	rec := personalRoute(api, (*StreamingAPI).handleConnectPersonalMCP, http.MethodPost, "/x", `{}`, "owner", map[string]string{"name": "gmail"})
	if !strings.Contains(rec.Body.String(), "client_id=965.apps.googleusercontent.com") || !strings.Contains(rec.Body.String(), "access_type=offline") {
		t.Fatalf("connect with the app = %s", rec.Body.String())
	}
	// A new server records the app, and another person's Connect works too.
	if r := personalRoute(api, (*StreamingAPI).handleAddPersonalMCP, http.MethodPost, "/x", `{"catalog":"GoogleDrive","name":"drive"}`, "other", nil); r.Code != http.StatusOK {
		t.Fatalf("other add = %d %s", r.Code, r.Body.String())
	}
	if _, cfg, err := personalMCPServerConfig("other", "drive"); err != nil || cfg.OAuth.ClientID != "965.apps.googleusercontent.com" || cfg.OAuth.ClientSecret != "GOCSPX-shared-secret" {
		t.Fatalf("other's config = %+v %v", cfg.OAuth, err)
	}

	// Rotation reaches everyone at once.
	if code := put("google", `{"client_id":"new.apps.googleusercontent.com","client_secret":"GOCSPX-rotated"}`); code != http.StatusOK {
		t.Fatalf("rotate = %d", code)
	}
	if _, cfg, _ := personalMCPServerConfig("other", "drive"); cfg.OAuth.ClientID != "new.apps.googleusercontent.com" || cfg.OAuth.ClientSecret != "GOCSPX-rotated" {
		t.Fatalf("rotation did not reach the person: %+v", cfg.OAuth)
	}
	// A client the person entered for their own app wins.
	if err := writePersonalMCPClient("owner", "gmail", registeredClient{ClientID: "mine.apps.googleusercontent.com", ClientSecret: "GOCSPX-mine"}); err != nil {
		t.Fatal(err)
	}
	if _, cfg, _ := personalMCPServerConfig("owner", "gmail"); cfg.OAuth.ClientID != "mine.apps.googleusercontent.com" {
		t.Fatalf("own client lost to the deployment app: %+v", cfg.OAuth)
	}

	// Removing the app takes it away again.
	if code := personalRoute(api, (*StreamingAPI).handlePutMCPApp, http.MethodDelete, "/x", "", "owner", map[string]string{"key": "google"}).Code; code != http.StatusOK {
		t.Fatalf("delete = %d", code)
	}
	if _, cfg, _ := personalMCPServerConfig("other", "drive"); cfg.OAuth.ClientID != "" {
		t.Fatalf("removed app still resolves: %+v", cfg.OAuth)
	}
}
