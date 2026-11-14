package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// PLAT-746-1026: a Team gets a copy of a member's API-key connection inside the Vault service. Only the source vault's
// owner can copy, only into a vault the named Team owner owns, and a personal sign-in is never copied.
func TestCopyVaultConnectorIntoTeamVault(t *testing.T) {
	origin := server.NewMCPServer("copy-fixture", "1")
	origin.AddTool(mcp.NewTool("read"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	handler := server.NewStreamableHTTPServer(origin, server.WithStateLess(true))
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer team-key" {
			w.WriteHeader(401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	for _, id := range []string{"alice", "bob", "team-1"} {
		st.AddUser(store.User{ID: id, WorkspaceID: "w", Email: id + "@example.com"})
	}
	mine, _ := st.EnsurePersonalVault("w", "alice", "v-alice")
	bobs, _ := st.EnsurePersonalVault("w", "bob", "v-bob")
	team, _ := st.EnsurePersonalVault("w", "team-1", "v-team")
	g := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	a := &Admin{Store: st, Gateway: g, WorkspaceID: "w"}
	ctx := context.Background()
	src, err := a.addConnectorCustomFor(ctx, mine, "langfuse", "Langfuse", "", remote.URL+"/mcp", "team-key", "Bearer")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VaultAttachConnector("alice", mine, src.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := a.copyVaultConnector(ctx, "bob", mine, src.ID, team, "team-1"); err == nil {
		t.Fatal("someone who does not own the source vault copied its connection")
	}
	if _, err := a.copyVaultConnector(ctx, "alice", mine, src.ID, bobs, "bob"); err == nil {
		t.Fatal("a copy went into another person's vault: only a team-<project> account may receive one")
	}
	if _, err := a.copyVaultConnector(ctx, "alice", mine, src.ID, bobs, "team-1"); err == nil {
		t.Fatal("a copy went into a vault the named team account does not own")
	}
	copied, err := a.copyVaultConnector(ctx, "alice", mine, src.ID, team, "team-1")
	if err != nil {
		t.Fatal(err)
	}
	if copied.ID == src.ID || copied.VaultID != team || st.ConnectorBearer(copied.ID) != "team-key" || !st.VaultConnectorOwnedBy("team-1", team, copied.ID) || !st.VaultConnectorOwnedBy("alice", mine, src.ID) {
		t.Fatalf("the Team must hold its own copy and the member keeps theirs: %+v", copied)
	}

	// Only the host's service credential reaches the route: a user or MCP token, a missing credential, or the
	// service credential without a validated person is refused before anything is copied.
	a.HumanToken = "service-token"
	mux := http.NewServeMux()
	a.vaultRoutes(mux)
	call := func(bearer, platform, actor string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/vaults/"+mine+"/connectors/"+src.ID+"/copy", strings.NewReader(`{"to_vault":"`+team+`","to_owner":"team-1"}`))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if platform != "" {
			req.Header.Set("X-Vault-Platform-User", platform)
		}
		if actor != "" {
			req.Header.Set("X-CapLayer-Actor", actor)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	for name, status := range map[string]int{
		"no credential":         call("", "1", "alice"),
		"a user token":          call("user-or-mcp-token", "1", "alice"),
		"no validated person":   call("service-token", "", "alice"),
		"someone not the owner": call("service-token", "1", "bob"),
	} {
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Fatalf("%s reached the copy route: %d", name, status)
		}
	}

	odd := store.Connector{ID: "c-odd", WorkspaceID: "w", Provider: "langfuse", UpstreamURL: remote.URL + "/mcp", AuthScheme: "Query", Status: store.StatusActive, VaultID: mine}
	st.AddConnector(odd)
	if err := st.VaultAttachConnector("alice", mine, odd.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.copyVaultConnector(ctx, "alice", mine, odd.ID, team, "team-1"); err == nil {
		t.Fatal("a sign-in type the copy cannot carry was copied")
	}

	oauth := store.Connector{ID: "c-oauth", WorkspaceID: "w", Provider: "github", OAuthServer: "github", OAuthCredentialID: "c-oauth", Status: store.StatusActive, VaultID: mine}
	st.AddConnector(oauth)
	if err := st.VaultAttachConnector("alice", mine, oauth.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.copyVaultConnector(ctx, "alice", mine, oauth.ID, team, "team-1"); err == nil {
		t.Fatal("a personal sign-in was copied to the Team")
	}
}
