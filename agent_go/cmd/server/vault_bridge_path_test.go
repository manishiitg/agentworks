package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/mcpagent/mcpcache/openapi"
)

func TestVaultGeneratedBridgePathUsesLiveGrantsWithoutProjectSelection(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	ids := []string{"c-notion-1"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CapLayer-Actor") != "alice" {
			t.Error("caller changed")
		}
		rows := []map[string]any{}
		for _, id := range ids {
			rows = append(rows, map[string]any{"id": id, "label": id, "provider": "notion", "tools": []map[string]any{{"name": "notion_c-notion-1__notion-fetch"}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": rows})
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("crew-test", "alice")
	ctx := personContext("alice")
	raw := vaultServerName("c-notion-1")
	path := openapi.SanitizePathSegment(raw + "__scope_" + strings.Repeat("a", 32))
	got, err := api.resolveGovernedMCP(ctx, "alice", path)
	if err != nil || vaultSelectionName(got.Name) != raw {
		t.Fatalf("generated path failed: %v", err)
	}
	got, err = api.resolveScopedGovernedMCP(ctx, nil, []string{raw}, nil, "alice", path, "fetch")
	if err != nil || vaultSelectionName(got.Name) != raw {
		t.Fatalf("selected generated path failed: %v", err)
	}
	for _, selected := range [][]string{nil, {"vault_c-other"}, {"vault_c_notion_1"}} {
		if _, err = api.resolveScopedGovernedMCP(ctx, nil, selected, []string{"private:other"}, "alice", path, "fetch"); err != nil {
			t.Fatalf("authorized Vault access was gated by project selection: %v", selected)
		}
	}
	_, tool, toolErr := api.vaultBridgeToolName(ctx, "crew-test", path, "notion_c_notion_1__notion_fetch")
	if toolErr != nil || tool != "notion_c-notion-1__notion-fetch" {
		t.Fatalf("tool path failed: %s %v", tool, toolErr)
	}
	_, tool, toolErr = api.vaultBridgeToolName(ctx, "crew-test", path, "notion_c-notion-1__notion-get-users")
	if toolErr != nil || tool != "notion_c-notion-1__notion-get-users" {
		t.Fatal("unknown tool was remapped")
	}
	if _, _, err = api.vaultBridgeToolName(ctx, "unknown-session", path, "fetch"); err == nil {
		t.Fatal("unknown session got an inventory")
	}
	ids = []string{"c-notion-1", "c_notion_1"}
	if _, err = api.resolveGovernedMCP(ctx, "alice", path); err == nil {
		t.Fatal("ambiguous normalized IDs accepted")
	}
	ids = nil
	if _, err = api.resolveGovernedMCP(ctx, "alice", path); err == nil {
		t.Fatal("retained bridge path bypassed revoked inventory")
	}
}
