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

	// A server Crew's vault (decisions log, 22 Nov): its owners are exactly the Crew's owner and co-owners. An owner copies
	// their own key in; naming yourself as the owner of a personal vault is still refused, and a removed co-owner loses the
	// Crew vault at once.
	crew := "v-crew"
	if _, err := st.EnsureCrewVault("w", crew, store.CrewVaultRootPrefix+"p1", "Crew vault", []string{"alice", "bob"}); err != nil {
		t.Fatal(err)
	}
	inCrew, err := a.copyVaultConnector(ctx, "alice", mine, src.ID, crew, "alice")
	if err != nil {
		t.Fatalf("an owner could not copy their own key into their Crew vault: %v", err)
	}
	// The key stays inside the Crew: it is not copied out into a Team vault, and it is left out of every runtime list
	// that does not name the Crew vault ("use all apps", a personal project), while the Crew's own runtime gets it.
	if _, err := a.copyVaultConnector(ctx, "alice", crew, inCrew.ID, team, "team-1"); err == nil {
		t.Fatal("an app was copied out of a Crew vault into a Team vault")
	}
	a.HumanToken = "service-token"
	inventory := func(scope string) string {
		mux := http.NewServeMux()
		a.runtimeRoutes(mux)
		r := httptest.NewRequest(http.MethodGet, "/api/admin/runtime/servers", nil)
		r.Header.Set("Authorization", "Bearer service-token")
		r.Header.Set("X-CapLayer-Actor", "alice")
		if scope != "" {
			r.Header.Set("X-Vault-Crew-Vault", scope)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Body.String()
	}
	if body := inventory(""); strings.Contains(body, inCrew.ID) || !strings.Contains(body, src.ID) {
		t.Fatalf("a list that did not name the Crew vault showed its app, or lost the person's own: %s", body)
	}
	if !strings.Contains(inventory(crew), inCrew.ID) {
		t.Fatal("the Crew's own runtime did not get its Crew vault's app")
	}
	// Its keys follow the same rule (#3827): a project scope is left out of account-wide lists, so before this the
	// Crew's own runtime never got its vault's keys either and fell back to same-named personal ones.
	if err := st.VaultAddSecret("alice", crew, "VLT_CREW__DEPLOY_KEY"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inventory(""), "VLT_CREW__DEPLOY_KEY") {
		t.Fatal("a list that did not name the Crew vault showed its key")
	}
	if !strings.Contains(inventory(crew), "VLT_CREW__DEPLOY_KEY") {
		t.Fatal("the Crew's own runtime did not get its Crew vault's key")
	}
	if _, err := a.copyVaultConnector(ctx, "alice", mine, src.ID, bobs, "alice"); err == nil {
		t.Fatal("a copy went into another person's vault by naming the copier as its owner")
	}
	if _, err := st.EnsureCrewVault("w", crew, store.CrewVaultRootPrefix+"p1", "Crew vault", []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	if st.VaultOwnedBy("alice", crew) {
		t.Fatal("a removed co-owner still owns the Crew vault")
	}
	if _, err := a.copyVaultConnector(ctx, "alice", mine, src.ID, crew, "alice"); err == nil {
		t.Fatal("someone who no longer owns the Crew vault copied into it")
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
