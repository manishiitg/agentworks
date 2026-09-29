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
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
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

// The operator command reads Google's downloaded client file from stdin and
// stores the app sealed, the same as the admin card, without echoing the secret.
func TestSetMCPAppCommandReadsGoogleClientJSONFromStdin(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withPersonalMCPRoot(t)
	for name, input := range map[string]string{
		"web":       `{"web":{"client_id":"1.apps.googleusercontent.com","client_secret":"GOCSPX-cmd","project_id":"p","redirect_uris":["https://a.example.com/api/oauth/callback"]}}`,
		"installed": `{"installed":{"client_id":"1.apps.googleusercontent.com","client_secret":"GOCSPX-cmd"}}`,
		"plain":     `{"client_id":"1.apps.googleusercontent.com","client_secret":"GOCSPX-cmd"}`,
	} {
		app, err := parseMCPAppJSON([]byte(input))
		if err != nil || app.ClientID != "1.apps.googleusercontent.com" || app.ClientSecret != "GOCSPX-cmd" {
			t.Fatalf("%s: %+v %v", name, app, err)
		}
	}
	for _, bad := range []string{``, `nope`, `{"web":{}}`, `{"web":{"client_id":"a"}}`} {
		if _, err := parseMCPAppJSON([]byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	var out strings.Builder
	setMCPAppCmd.SetIn(strings.NewReader(`{"web":{"client_id":"1.apps.googleusercontent.com","client_secret":"GOCSPX-cmd"}}`))
	setMCPAppCmd.SetOut(&out)
	if err := setMCPAppCmd.Flags().Set("key", "google"); err != nil {
		t.Fatal(err)
	}
	if err := setMCPAppCmd.RunE(setMCPAppCmd, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "GOCSPX") || !strings.Contains(out.String(), "1.apps.googleusercontent.com") {
		t.Fatalf("output = %q", out.String())
	}
	app, err := readMCPApp("google")
	if err != nil || app == nil || app.ClientSecret != "GOCSPX-cmd" || app.UpdatedBy != "operator" {
		t.Fatalf("stored app = %+v %v", app, err)
	}
	path, _ := mcpAppFile("google")
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "GOCSPX") {
		t.Fatalf("stored in the clear: %s", raw)
	}
}

// Review follow-ups (ai-work-38): the provider match is on the parsed host,
// keys shared with a different token URL are refused, older catalog servers
// find the app, a stored app can always be removed, writes are atomic, and the
// operator command refuses root and unknown keys.
func TestSignInAppKeysMatchHostsAndRefuseSharedKeys(t *testing.T) {
	for authURL, want := range map[string]string{
		"https://accounts.google.com/o/oauth2/v2/auth":          "google",
		"https://github.com/login/oauth/authorize":              "github",
		"https://slack.com/oauth/v2_user/authorize":             "slack",
		"https://acme.slack.com/oauth":                          "slack",
		"https://notslack.com/oauth":                            "notslack", // a substring is not the provider
		"https://evil.example/?next=accounts.google.com":        "notslack",
		"http://accounts.google.com/o/oauth2/v2/auth":           "notslack", // not https
		"https://github.com.evil.example/login/oauth/authorize": "notslack",
	} {
		if got := mcpAppKeyFor("NotSlack", &oauth.OAuthConfig{AuthURL: authURL}); got != want {
			t.Fatalf("%s = %q, want %q", authURL, got, want)
		}
	}

	servers := map[string]mcpclient.MCPServerConfig{
		"a-b": {URL: "https://a.example.com/mcp", OAuth: &oauth.OAuthConfig{AuthURL: "https://a.example.com/auth", TokenURL: "https://a.example.com/token"}},
		"a_b": {URL: "https://b.example.com/mcp", OAuth: &oauth.OAuthConfig{AuthURL: "https://b.example.com/auth", TokenURL: "https://b.example.com/token"}},
		"ok":  {URL: "https://c.example.com/mcp", OAuth: &oauth.OAuthConfig{AuthURL: "https://c.example.com/auth", TokenURL: "https://c.example.com/token"}},
	}
	keys := mcpAppKeyIndex(servers)
	if keys["a-b"] != "" || keys["a_b"] != "" || keys["ok"] != "ok" {
		t.Fatalf("two names cleaning to one key at different token URLs kept it: %v", keys)
	}
}

func TestOlderCatalogServersAndRemovalAndAtomicWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withPersonalMCPRoot(t)
	// A catalog server added before apps existed has no app key yet.
	if _, err := addPersonalMCPServer("owner", personalMCPServer{Name: "gmail", URL: "https://gmailmcp.googleapis.com/mcp/v1", Catalog: "GoogleGmail",
		OAuth: &oauth.OAuthConfig{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token"}}); err != nil {
		t.Fatal(err)
	}
	if err := writeMCPApp("google", mcpApp{ClientID: "id.apps.googleusercontent.com", ClientSecret: "GOCSPX-old"}); err != nil {
		t.Fatal(err)
	}
	if _, cfg, err := personalMCPServerConfig("owner", "gmail"); err != nil || cfg.OAuth.ClientID != "id.apps.googleusercontent.com" {
		t.Fatalf("an older catalog server did not find the app: %+v %v", cfg.OAuth, err)
	}
	// Overwrites leave no temp files and never a partial app.
	path, _ := mcpAppFile("google")
	for _, secret := range []string{"GOCSPX-one", "GOCSPX-two"} {
		if err := writeMCPApp("google", mcpApp{ClientID: "id", ClientSecret: secret}); err != nil {
			t.Fatal(err)
		}
		if app, err := readMCPApp("google"); err != nil || app.ClientSecret != secret {
			t.Fatalf("read after write = %+v %v", app, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("app file mode = %v", info.Mode().Perm())
	}
}

func TestStoredAppCanBeRemovedAfterItsProviderLeavesTheCatalog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	api, _ := newCodePrivacyFixture(t)
	withPersonalMCPRoot(t)
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(catalogPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = catalogPath
	api.logger = loggerv2.NewNoop()
	if err := writeMCPApp("google", mcpApp{ClientID: "id", ClientSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	if rec := personalRoute(api, (*StreamingAPI).handlePutMCPApp, http.MethodPut, "/x", `{"client_id":"a","client_secret":"b"}`, "owner", map[string]string{"key": "google"}); rec.Code != http.StatusNotFound {
		t.Fatalf("setting an app nobody needs = %d", rec.Code)
	}
	if rec := personalRoute(api, (*StreamingAPI).handlePutMCPApp, http.MethodDelete, "/x", "", "owner", map[string]string{"key": "google"}); rec.Code != http.StatusOK {
		t.Fatalf("removing the stored app = %d", rec.Code)
	}
	if app, _ := readMCPApp("google"); app != nil {
		t.Fatal("the app is still on disk")
	}
	if rec := personalRoute(api, (*StreamingAPI).handlePutMCPApp, http.MethodDelete, "/x", "", "owner", map[string]string{"key": "../evil"}); rec.Code == http.StatusOK {
		t.Fatal("an invalid key was accepted")
	}
}

func TestSetMCPAppCommandRefusesRootAndUnknownKeys(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withPersonalMCPRoot(t)
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(catalogPath, []byte(`{"mcpServers":{"GoogleGmail":{"url":"https://gmailmcp.googleapis.com/mcp/v1","protocol":"http","oauth":{"auth_url":"https://accounts.google.com/o/oauth2/v2/auth","token_url":"https://oauth2.googleapis.com/token"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(key string, euid int) error {
		prev := processEuid
		processEuid = func() int { return euid }
		defer func() { processEuid = prev }()
		setMCPAppCmd.SetIn(strings.NewReader(`{"client_id":"1","client_secret":"s"}`))
		setMCPAppCmd.SetOut(&strings.Builder{})
		_ = setMCPAppCmd.Flags().Set("key", key)
		_ = setMCPAppCmd.Flags().Set("mcp-config", catalogPath)
		return setMCPAppCmd.RunE(setMCPAppCmd, nil)
	}
	if err := run("google", 0); err == nil || !strings.Contains(err.Error(), "not root") {
		t.Fatalf("root allowed: %v", err)
	}
	if err := run("gogle", 1000); err == nil || !strings.Contains(err.Error(), "google") {
		t.Fatalf("a typo saved: %v", err)
	}
	if err := run("google", 1000); err != nil {
		t.Fatalf("the right key failed: %v", err)
	}
}

// The admin's shared (platform) connect uses the deployment's sign-in app
// too: no client-ID prompt once the app exists.
func TestSharedConnectUsesSignInApp(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	api, _ := newCodePrivacyFixture(t)
	withPersonalMCPRoot(t)
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	catalog := `{"mcpServers":{"GoogleGmail":{"url":"https://gmailmcp.googleapis.com/mcp/v1","protocol":"http","oauth":{"auth_url":"https://accounts.google.com/o/oauth2/v2/auth","token_url":"https://oauth2.googleapis.com/token"}}}}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = catalogPath
	api.logger = loggerv2.NewNoop()

	_, discovery, err := api.beginOAuthFlow("admin", "", "GoogleGmail", "https://example.com/api/oauth/callback", "", "", nil)
	if err != nil || discovery == nil || discovery.Status != "needs_client_id" {
		t.Fatalf("without an app the shared connect asks for a client: %+v %v", discovery, err)
	}
	if err := writeMCPApp("google", mcpApp{ClientID: "app-id", ClientSecret: "app-secret"}); err != nil {
		t.Fatal(err)
	}
	start, discovery, err := api.beginOAuthFlow("admin", "", "GoogleGmail", "https://example.com/api/oauth/callback", "", "", nil)
	if err != nil || discovery != nil || start == nil || !strings.Contains(start.AuthURL, "client_id=app-id") {
		t.Fatalf("with the app the shared connect goes straight to sign-in: start=%+v discovery=%+v err=%v", start, discovery, err)
	}
}
