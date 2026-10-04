package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

func namedMCPFixture(t *testing.T) *StreamingAPI {
	t.Helper()
	api, _ := newCodePrivacyFixture(t)
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", strings.Repeat("s", 32))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PUBLIC_URL", "")
	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{"AcmeMail":{"url":"https://mcp.acme.example/mcp","protocol":"http","oauth":{"auth_url":"https://auth.acme.example/authorize","token_url":"https://auth.acme.example/token"}},"PublicNotes":{"url":"https://mcp.example.com/mcp","protocol":"http"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	api.mcpConfigPath = config
	return api
}

func TestNamedPrivateMCPRouteCredentialsAndRuntimeIsolation(t *testing.T) {
	api := namedMCPFixture(t)
	add := func(label string) placeMCPServer {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"workspace_path": "Chats/Code/projects/app-c0de0001", "catalog": "AcmeMail", "label": label})
		rec := personalRoute(api, (*StreamingAPI).handleAddPlaceMCP, http.MethodPost, "/x", string(body), "owner", nil)
		var row struct {
			Name string `json:"name"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &row) != nil || row.Name == "" {
			t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
		}
		saved, found := personalMCPByCatalog("owner", row.Name)
		if !found || saved.Label != label {
			t.Fatal("named account was not persisted")
		}
		return saved
	}
	a, b := add("Mail · Engineering"), add("Mail · Sales")
	if a.Name == b.Name {
		t.Fatal("accounts reused an ID")
	}
	dir, _ := placeMCPDir("owner")
	for _, s := range []placeMCPServer{a, b} {
		// Even providers using the deployment's grouped OAuth app must isolate named accounts.
		s.AppKey = "google"
		if _, err := addPlaceMCPServer("owner", s); err != nil {
			t.Fatal(err)
		}
		if placeMCPGroupOf(dir, "owner", s) != "" {
			t.Fatal("named account shares provider login")
		}
		if err := writePlaceMCPClient("owner", s.Name, registeredClient{ClientID: s.Name, ClientSecret: "secret-" + s.Name}); err != nil {
			t.Fatal(err)
		}
		if err := oauth.NewTokenStore(placeMCPTokenFile(dir, "owner", s.Name)).Save(&oauth2.Token{AccessToken: "token-" + s.Name}); err != nil {
			t.Fatal(err)
		}
	}
	ra, err := api.resolveGovernedMCP(context.Background(), "owner", a.Name)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := api.resolveGovernedMCP(context.Background(), "owner", b.Name)
	if err != nil {
		t.Fatal(err)
	}
	if ra.Name == rb.Name || ra.Config.OAuth.TokenFile == rb.Config.OAuth.TokenFile || ra.Config.OAuth.ClientID == rb.Config.OAuth.ClientID {
		t.Fatal("runtime or credentials cross accounts")
	}
	selected, overrides, aliases, err := api.scopeAgentMCP(personContext("owner"), "", []string{a.Name, b.Name}, nil)
	if err != nil || len(selected) != 2 || len(overrides) != 2 || aliases[a.Name] == aliases[b.Name] || aliases["acmemail"] != "" {
		t.Fatal("account namespaces or aliases merged")
	}
	catalog := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{}}
	if _, err := api.resolveScopedGovernedMCP(personContext("owner"), catalog, []string{a.Name}, nil, "owner", b.Name, "read"); err == nil {
		t.Fatal("project selection admitted another account")
	}
	if _, err := api.resolveScopedGovernedMCP(personContext("owner"), catalog, []string{a.Name}, nil, "owner", a.Name, "read"); err != nil {
		t.Fatal("selected account rejected")
	}
	if _, err := api.resolveGovernedMCP(context.Background(), "owner", "AcmeMail"); err == nil {
		t.Fatal("ambiguous provider selected an account")
	}
	if _, err := api.resolveGovernedMCP(context.Background(), "other", ra.Name); err == nil {
		t.Fatal("another user resolved an account")
	}
	// Exact reuse does not clear the encrypted client/token.
	reused, _, err := api.ensurePersonalMCP(context.Background(), "owner", placeMCPServer{Name: a.Name}, "")
	if err != nil || reused.Name != a.Name || !fileExists(placeMCPTokenFile(dir, "owner", a.Name)) {
		t.Fatal("exact reuse destroyed login")
	}
	rec := personalRoute(api, (*StreamingAPI).handleListPlaceMCP, http.MethodGet, "/x?workspace_path=Chats/Code/projects/app-c0de0001", "", "owner", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), a.Label) || !strings.Contains(rec.Body.String(), b.Label) {
		t.Fatal("project list lost account labels")
	}
	if err := forgetPlaceMCPLogin("owner", a.Name); err != nil {
		t.Fatal(err)
	}
	if err := removePlaceMCPServer("owner", a.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := api.resolveGovernedMCP(context.Background(), "owner", b.Name); err != nil {
		t.Fatal("removing one account broke the other")
	}
}

func TestNamedPrivateMCPBuilderAndCodeUseSharedSetup(t *testing.T) {
	api := namedMCPFixture(t)
	for _, label := range []string{"Notes · Engineering", "Notes · Sales"} {
		if _, err := api.mcpConnectionTool(context.Background(), "owner", "install_mcp_server", map[string]interface{}{"name": "PublicNotes", "catalog": "PublicNotes", "label": label}); err != nil {
			t.Fatal(err)
		}
	}
	reg := &placeMCPToolRegistrar{}
	if err := api.registerPlaceMCPTool(reg, "owner", codePrivacyOwnerRoot, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.exec(context.Background(), map[string]interface{}{"action": "connect", "catalog": "PublicNotes", "label": "Notes · Support"}); err != nil {
		t.Fatal(err)
	}
	servers, _ := listPlaceMCPServers("owner")
	if len(servers) != 3 {
		t.Fatalf("accounts: %d", len(servers))
	}
	for _, s := range servers {
		if _, err := reg.exec(context.Background(), map[string]interface{}{"action": "connect", "name": s.Name}); err != nil {
			t.Fatal(err)
		}
	}
	if now, _ := listPlaceMCPServers("owner"); len(now) != 3 {
		t.Fatal("reusing exact connection created another account")
	}
	if _, err := api.mcpConnectionTool(context.Background(), "owner", "install_mcp_server", map[string]interface{}{"name": "PublicNotes"}); err == nil {
		t.Fatal("builder guessed an ambiguous account")
	}
	if _, err := api.mcpConnectionTool(context.Background(), "owner", "remove_mcp_server", map[string]interface{}{"name": "PublicNotes"}); err == nil {
		t.Fatal("builder removed an ambiguous account")
	}
	for _, s := range servers {
		if _, err := api.mcpConnectionTool(context.Background(), "owner", "install_mcp_server", map[string]interface{}{"name": s.Name}); err != nil {
			t.Fatal(err)
		}
	}
	attached, _ := placeMCPAttachmentsFor(codePrivacyOwnerRoot)
	if len(attached) != 3 {
		t.Fatal("Code did not attach exact accounts")
	}
}
