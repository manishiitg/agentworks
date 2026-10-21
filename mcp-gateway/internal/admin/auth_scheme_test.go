package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestConnectorCredentialSchemeValidation(t *testing.T) {
	for _, tc := range []struct{ token, scheme string }{
		{"dXNlcjpwYXNz", "Basic"}, {"fixture", "Bearer"}, {"", ""},
	} {
		if err := validateConnectorCredential(tc.token, tc.scheme); err != nil {
			t.Fatal("valid credential rejected", err)
		}
	}
	for _, tc := range []struct{ token, scheme string }{
		{"fixture", "Other"}, {"invalid", "Basic"}, {"bm9jb2xvbg==", "Basic"}, {"value\r\nInjected", "Bearer"},
	} {
		if err := validateConnectorCredential(tc.token, tc.scheme); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}

func TestCustomBasicConnectorPersistsAndRotatesWithoutLeaking(t *testing.T) {
	origin := server.NewMCPServer("basic-fixture", "1")
	origin.AddTool(mcp.NewTool("read"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	handler := server.NewStreamableHTTPServer(origin, server.WithStateLess(true))
	want := "Basic dXNlcjpwYXNz"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	st, err := store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st.AddWorkspace(store.Workspace{ID: "w"})
	g := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	a := &Admin{Store: st, Gateway: g, WorkspaceID: "w"}
	c, err := a.AddConnectorCustomWithCredential(context.Background(), "langfuse", "Langfuse", "", remote.URL+"/mcp", "dXNlcjpwYXNz", "Basic")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	if strings.Contains(string(data), "dXNlcjpwYXNz") {
		t.Fatal("API exposes credential")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, ok := st.GetConnector(c.ID)
	if !ok || c.AuthScheme != "Basic" || st.ConnectorBearer(c.ID) != "dXNlcjpwYXNz" {
		t.Fatal("Basic credential not restored")
	}
	// A real discovery during rotation must also send the preserved scheme.
	want = "Basic dXNlcjpuZXc="
	g = mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	if err := g.ReplaceConnectorCredentials(context.Background(), c, "dXNlcjpuZXc="); err != nil {
		t.Fatal(err)
	}
	if st.ConnectorBearer(c.ID) != "dXNlcjpuZXc=" {
		t.Fatal("rotated credential not stored")
	}
}

// A custom server that needs a sign-in becomes a sign-in connection for its own URL (the product discovers the
// server's published sign-in details); without the product's OAuth service it is refused as before.
func TestCustomConnectorNeedingSignInBecomesSignInConnection(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer remote.Close()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "gateway.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.AddWorkspace(store.Workspace{ID: "w"})
	g := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	a := &Admin{Store: st, Gateway: g, WorkspaceID: "w"}
	if _, err := a.AddConnectorCustom(context.Background(), "jam", "Jam", "", remote.URL+"/mcp"); err == nil {
		t.Fatal("a sign-in server was added without the product's OAuth service")
	}
	g.SetSharedOAuth(func(context.Context, string, string, string) (string, error) { return "", nil })
	c, err := a.AddConnectorCustom(context.Background(), "jam", "Jam", "", remote.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != store.StatusAuthRequired || !strings.HasPrefix(c.OAuthServer, "custom:") || c.OAuthCredentialID != c.ID || c.UpstreamURL != remote.URL+"/mcp" {
		t.Fatalf("not a sign-in connection for its own URL: %+v", c)
	}
}

// Slack is a personal account (a person's DMs and private channels): the company Vault, shared with everyone, refuses
// it by catalog name and by URL. GitHub is a company app and is in the catalog.
func TestCompanyVaultRefusesPersonalSlack(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.Find("GitHub"); !ok {
		t.Fatal("GitHub missing from the Vault catalog")
	}
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "gateway.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.AddWorkspace(store.Workspace{ID: "w"})
	g := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	g.SetSharedOAuth(func(context.Context, string, string, string) (string, error) { return "", nil })
	a := &Admin{Store: st, Gateway: g, Catalog: cat, WorkspaceID: "w"}
	if _, err := a.AddConnectorFromCatalog(context.Background(), "Slack", "Team Slack", ""); err == nil || !strings.Contains(err.Error(), "own vault") {
		t.Fatalf("company Vault accepted Slack: %v", err)
	}
	if _, err := a.AddConnectorCustom(context.Background(), "myslack", "My Slack", "", "https://mcp.slack.com/mcp"); err == nil || !strings.Contains(err.Error(), "own vault") {
		t.Fatalf("company Vault accepted Slack by URL: %v", err)
	}
}
