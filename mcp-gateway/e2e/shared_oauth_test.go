package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	gw.SetSharedOAuth(func(ctx context.Context, name, url string) (string, error) {
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
