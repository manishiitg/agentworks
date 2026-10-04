package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mark3labs/mcp-go/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func TestSharedOAuthCatalogConnectionStillRequiresGroupGrants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	original := fakeUpstream(t)
	token := "initial"
	authenticated := 0
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		authenticated++
		original.Config.Handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	file := filepath.Join(t.TempDir(), "catalog.json")
	data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"Test OAuth": map[string]any{"url": endpoint.URL + "/mcp", "oauth": map[string]any{"client_id": "never-exposed"}}}})
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.LoadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "readers", WorkspaceID: "w"})
	st.AddMember("readers", "alice")
	gw := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	gw.SetSharedOAuth(func(ctx context.Context, name, url, connectionID string) (string, error) {
		if name != "Test OAuth" || url != endpoint.URL+"/mcp" {
			t.Fatal("incorrect shared OAuth binding")
		}
		return token, nil
	})
	adm := &admin.Admin{Store: st, Gateway: gw, Catalog: cat, WorkspaceID: "w"}
	c, err := adm.AddConnectorFromCatalog(ctx, "Test OAuth", "", "user")
	if err != nil {
		t.Fatal(err)
	}
	defer gw.RemoveConnector(c.ID)
	if c.OAuthServer != "Test OAuth" {
		t.Fatal("OAuth connection metadata lost")
	}
	if c.Status != store.StatusAuthRequired || c.OAuthCredentialID != c.ID || len(st.ListTools("w")) != 0 {
		t.Fatal("OAuth connection became active before sign-in")
	}
	if err := adm.SyncConnector(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	tools := st.ListTools("w")
	if len(tools) != 2 {
		t.Fatal("tools not discovered")
	}
	alice := auth.Identity{UserID: "alice", WorkspaceID: "w"}
	for _, tool := range tools {
		if tool.Status != store.StatusActive {
			t.Fatal("initial admin connection not approved")
		}
		if _, err := policy.Authorize(st, alice, tool.PublicName); !errors.Is(err, policy.ErrNoGrant) {
			t.Fatal("OAuth connection granted user access", err)
		}
	}
	st.AddGroupGrant(store.GroupGrant{GroupID: "readers", PublicName: tools[0].PublicName})
	beforeSync := authenticated
	token = "refreshed"
	if err := adm.SyncConnector(ctx, c.ID); err != nil {
		t.Fatal("refreshed credential not used", err)
	}
	if beforeSync == 0 || authenticated <= beforeSync {
		t.Fatal("OAuth was not used for discovery and sync")
	}
	if _, err := policy.Authorize(st, alice, tools[0].PublicName); err != nil {
		t.Fatal("refresh changed existing grant", err)
	}
	if _, err := policy.Authorize(st, alice, tools[1].PublicName); !errors.Is(err, policy.ErrNoGrant) {
		t.Fatal("refresh broadened permission", err)
	}
	if err := gw.ReplaceConnectorCredentials(ctx, c, "raw-token"); err == nil {
		t.Fatal("OAuth credential could be replaced by raw token")
	}
}

func TestSharedOAuthTwoConnectionsKeepAccountsAndGrantsSeparate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	original := fakeUpstream(t)
	var mu sync.Mutex
	seen := map[string]int{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get("Authorization")
		if value != "Bearer account-one" && value != "Bearer account-two" {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		seen[value]++
		mu.Unlock()
		original.Config.Handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	file := filepath.Join(t.TempDir(), "catalog.json")
	data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"Test": map[string]any{"url": endpoint.URL + "/mcp", "oauth": map[string]any{"client_id": "app"}}}})
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.LoadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "readers", WorkspaceID: "w"})
	st.AddMember("readers", "alice")
	gw := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	var one, two store.Connector
	gw.SetSharedOAuth(func(_ context.Context, name, url, id string) (string, error) {
		if name != "Test" || url != endpoint.URL+"/mcp" {
			return "", errors.New("wrong provider")
		}
		if id == one.ID {
			return "account-one", nil
		}
		if id == two.ID {
			return "account-two", nil
		}
		return "", errors.New("unknown credential identity")
	})
	adm := &admin.Admin{Store: st, Gateway: gw, Catalog: cat, WorkspaceID: "w"}
	one, err = adm.AddConnectorFromCatalog(ctx, "Test", "Test · Engineering", "")
	if err != nil {
		t.Fatal(err)
	}
	two, err = adm.AddConnectorFromCatalog(ctx, "Test", "Test · Sales", "")
	if err != nil {
		t.Fatal(err)
	}
	defer gw.RemoveConnector(one.ID)
	defer gw.RemoveConnector(two.ID)
	if one.ID == two.ID || one.InstanceSlug == two.InstanceSlug || one.OAuthCredentialID != one.ID || two.OAuthCredentialID != two.ID {
		t.Fatal("instances did not get independent namespaces")
	}
	for _, c := range []store.Connector{one, two} {
		if c.Status != store.StatusAuthRequired || len(st.ListToolsForConnector(c.ID)) != 0 {
			t.Fatal("pending OAuth connected prematurely")
		}
		if err := adm.SyncConnector(ctx, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	first := st.ListToolsForConnector(one.ID)
	second := st.ListToolsForConnector(two.ID)
	if len(first) != 2 || len(second) != 2 {
		t.Fatal("tool discovery missing for an account")
	}
	// Give access to one account's echo tool; the other account stays invisible.
	var allowed string
	for _, tool := range first {
		if tool.UpstreamName == "allowed_tool" {
			allowed = tool.PublicName
		}
	}
	st.AddGroupGrant(store.GroupGrant{GroupID: "readers", PublicName: allowed})
	actor := auth.Identity{UserID: "alice", WorkspaceID: "w"}
	for _, tool := range second {
		if _, err := policy.Authorize(st, actor, tool.PublicName); !errors.Is(err, policy.ErrNoGrant) {
			t.Fatal("second account inherited first account permissions", err)
		}
	}
	host := httptest.NewServer(gw.ExternalProductHandler(func(*http.Request) (auth.Identity, string, bool) { return actor, "", true }))
	defer host.Close()
	client := dialGateway(t, host.URL+"/api/admin/runtime/external-mcp", "trusted-test")
	list, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != allowed {
		t.Fatal("account tool visibility incorrect", err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = allowed
	req.Params.Arguments = map[string]any{"hello": "world"}
	if result, err := client.CallTool(ctx, req); err != nil || result.IsError {
		t.Fatal("granted account call failed", err)
	}
	gw.SuspendOAuth(one)
	if _, err := policy.Authorize(st, actor, allowed); err == nil {
		t.Fatal("suspended account retained live access")
	}
	if err := adm.SyncConnector(ctx, two.ID); err != nil {
		t.Fatal("other account stopped after suspension", err)
	}
	adm.DeleteConnector(one.ID)
	if err := adm.SyncConnector(ctx, two.ID); err != nil {
		t.Fatal("other account stopped after deletion", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["Bearer account-one"] == 0 || seen["Bearer account-two"] == 0 {
		t.Fatal("discovery did not use both account credentials")
	}
}
