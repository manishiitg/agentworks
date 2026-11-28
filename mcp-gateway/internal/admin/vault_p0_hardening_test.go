package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestProjectVaultRouteRejectsCrewPrefix(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w", Name: "w"})
	if err := st.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddUser(store.User{ID: "bob", WorkspaceID: "w"})

	root := store.CrewVaultRootPrefix + "proj-1"
	sum := sha256.Sum256([]byte(root))
	crewID := "v-" + hex.EncodeToString(sum[:12])
	if _, err := st.EnsureCrewVault("w", crewID, root, "Crew 1", []string{"alice"}); err != nil {
		t.Fatal(err)
	}

	a := &Admin{Store: st, WorkspaceID: "w", HumanToken: "service-secret"}
	mux := http.NewServeMux()
	a.vaultRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/project-vaults", strings.NewReader(`{"workspace_root":"`+root+`"}`))
	req.Header.Set("Authorization", "Bearer service-secret")
	req.Header.Set("X-CapLayer-Actor", "bob")
	req.Header.Set("X-Vault-Platform-User", "1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/project-vaults with crew: prefix returned %d: %s", rec.Code, rec.Body.String())
	}
	if !st.VaultOwnedBy("alice", crewID) || st.VaultOwnedBy("bob", crewID) {
		t.Fatal("Crew vault ownership was altered via /api/project-vaults")
	}
}

func TestCrewVaultIsolatedOnPolicyAndExternalMCP(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w", Name: "w"})
	if err := st.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddUser(store.User{ID: "bob", WorkspaceID: "w"})

	personalID, err := st.EnsurePersonalVault("w", "alice", "v-personal-alice")
	if err != nil {
		t.Fatal(err)
	}
	crewID := "v-crew-p1"
	if _, err := st.EnsureCrewVault("w", crewID, store.CrewVaultRootPrefix+"p1", "Crew p1", []string{"alice", "bob"}); err != nil {
		t.Fatal(err)
	}

	addTool := func(connID, vaultID, pubName string) {
		st.AddConnector(store.Connector{ID: connID, WorkspaceID: "w", Provider: "notion", Label: connID, Status: store.StatusActive, VaultID: vaultID})
		st.UpsertToolSnapshot(store.ToolSnapshot{
			WorkspaceID:         "w",
			ConnectorID:         connID,
			PublicName:          pubName,
			UpstreamName:        "search",
			Status:              store.StatusActive,
			Fingerprint:         "fp",
			ApprovedFingerprint: "fp",
		})
		if err := st.VaultAttachConnector("alice", vaultID, connID); err != nil {
			t.Fatal(err)
		}
	}
	addTool("c-personal", personalID, "notion_personal__search")
	addTool("c-crew", crewID, "notion_crew__search")

	// Without AllowedCrewVault (external MCP or personal project), co-owners cannot see or invoke Crew vault tools.
	extID := auth.Identity{UserID: "bob", WorkspaceID: "w", ClientID: "mcp_client_" + strings.Repeat("a", 64)}
	if _, err := policy.Authorize(st, extID, "notion_crew__search"); err == nil {
		t.Fatal("external/unscoped identity authorized Crew vault tool")
	}
	if policy.Visible(st, extID, "notion_crew__search") {
		t.Fatal("external/unscoped identity saw Crew vault tool")
	}

	// Wrong Crew vault scope is also denied.
	wrongCrew := auth.Identity{UserID: "bob", WorkspaceID: "w", ClientID: "agentworks", AllowedCrewVault: "v-other-crew"}
	if _, err := policy.Authorize(st, wrongCrew, "notion_crew__search"); err == nil {
		t.Fatal("wrong AllowedCrewVault authorized Crew vault tool")
	}

	// Matching Crew vault scope is allowed for a Crew owner/co-owner.
	matchingCrew := auth.Identity{UserID: "bob", WorkspaceID: "w", ClientID: "agentworks", AllowedCrewVault: crewID}
	if _, err := policy.Authorize(st, matchingCrew, "notion_crew__search"); err != nil {
		t.Fatalf("matching AllowedCrewVault denied Crew vault tool: %v", err)
	}

	// Personal vault tools remain accessible without AllowedCrewVault.
	alicePersonal := auth.Identity{UserID: "alice", WorkspaceID: "w", ClientID: "agentworks"}
	if _, err := policy.Authorize(st, alicePersonal, "notion_personal__search"); err != nil {
		t.Fatalf("personal vault tool denied without AllowedCrewVault: %v", err)
	}

	// Verify /api/admin/runtime/external-mcp never trusts X-Vault-Crew-Vault header during tools/list.
	gw := mcpserver.New(st, nil, nil, nil)
	gw.RestoreTools("w")
	a := &Admin{Store: st, Gateway: gw, WorkspaceID: "w", HumanToken: "service-secret"}
	mux := http.NewServeMux()
	a.runtimeRoutes(mux)

	listBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/runtime/external-mcp", strings.NewReader(listBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer service-secret")
	req.Header.Set("X-CapLayer-Actor", "alice")
	req.Header.Set("X-Vault-Platform-User", "1")
	req.Header.Set("X-Vault-OAuth-Client", "mcp_client_"+strings.Repeat("a", 64))
	req.Header.Set("X-Vault-Crew-Vault", crewID) // must be ignored on external-mcp
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("external-mcp tools/list returned %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "notion_crew__search") {
		t.Fatalf("external-mcp leaked Crew vault tool despite X-Vault-Crew-Vault ignore rule: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "notion_personal__search") {
		t.Fatalf("external-mcp omitted personal vault tool: %s", rec.Body.String())
	}
}
