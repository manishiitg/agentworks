package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/mcpagent/mcpcache/openapi"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestVaultGeneratedBridgePathPreservesProjectSelection(t *testing.T) {
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
	// The HTTP path normalizes punctuation; compare selections with the
	// exact connection recovered from live grants, including per-tool picks.
	for _, selected := range [][]string{nil, {raw}} {
		if _, err = api.resolveScopedGovernedMCP(ctx, nil, selected, []string{raw + ":fetch"}, "alice", path, "fetch"); err != nil {
			t.Fatalf("selected tool on generated path failed: %v", err)
		}
	}
	for _, selected := range [][]string{{"vault_c-other"}, {"vault_c_notion_1"}, {mcpclient.NoServers}} {
		if _, err = api.resolveScopedGovernedMCP(ctx, nil, selected, nil, "alice", path, "fetch"); err == nil {
			t.Fatalf("unselected connection was allowed: %v", selected)
		}
	}
	if _, err = api.resolveScopedGovernedMCP(ctx, nil, []string{raw}, []string{raw + ":other"}, "alice", path, "fetch"); err == nil {
		t.Fatal("unselected tool was allowed")
	}
	// Every name an agent can hold for this tool lands on the one real connection and tool: the short
	// name, the old full name, and the half-converted spelling that failed 22 times in one log (PLAT-856).
	stale := raw + "__scope_" + strings.Repeat("b", 32)
	for _, asked := range [][2]string{
		{"notion", "notion__notion-fetch"},
		{"notion", "notion__notion_fetch"},
		{"NOTION", "Notion__Notion-Fetch"},
		{path, "notion_c_notion_1__notion_fetch"},
		{path, "notion_c_notion_1__notion-fetch"},
		{stale, "notion_c-notion-1__notion_fetch"},
	} {
		_, server, tool, toolErr := api.vaultBridgeToolName(ctx, "crew-test", asked[0], asked[1])
		if toolErr != nil || server != raw || tool != "notion_c-notion-1__notion-fetch" {
			t.Fatalf("%v went to %s %s: %v", asked, server, tool, toolErr)
		}
	}
	_, _, tool, toolErr := api.vaultBridgeToolName(ctx, "crew-test", path, "notion_c-notion-1__notion-get-users")
	if toolErr != nil || tool != "notion_c-notion-1__notion-get-users" {
		t.Fatal("unknown tool was remapped")
	}
	// A name that is not this person's app passes through for the resolver to refuse.
	if _, server, _, toolErr := api.vaultBridgeToolName(ctx, "crew-test", "github", "notion__notion-fetch"); toolErr != nil || server != "github" {
		t.Fatalf("another app's name was remapped: %s %v", server, toolErr)
	}
	if _, _, _, err = api.vaultBridgeToolName(ctx, "unknown-session", path, "fetch"); err == nil {
		t.Fatal("unknown session got an inventory")
	}
	if _, err = api.resolveGovernedMCP(ctx, "alice", "notion"); err != nil {
		t.Fatalf("the short name did not resolve: %v", err)
	}
	if got, _ := api.resolveGovernedMCP(ctx, "alice", raw); got == nil || got.Config.ToolAlias != "notion" {
		t.Fatalf("connection config lost its alias: %+v", got)
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

// The name an agent sees must not depend on the session or token, and two connections of one app stay apart by
// connection, not by a hash of a credential (PLAT-856).
func TestVaultToolAliasIsStableAndRefusesToGuess(t *testing.T) {
	one := vaultRuntimeServer{ID: "c-94acea49e7a07fde", Provider: "notion", Tools: []vaultRuntimeTool{{Name: "notion_c-94acea49e7a07fde__notion-fetch"}}}
	two := vaultRuntimeServer{ID: "c-1b2c3d4e5f60718a", Provider: "notion", Tools: []vaultRuntimeTool{{Name: "notion_c-1b2c3d4e5f60718a__notion-fetch"}}}
	sheets := vaultRuntimeServer{ID: "c-77", Provider: "google-sheets"}
	remote := vaultRuntimeServer{ID: "c-88", Name: "srv_abcd1234_c-88", Provider: "notion"}
	if got := vaultToolAlias([]vaultRuntimeServer{one, sheets}, one); got != "notion" {
		t.Fatalf("alias = %q", got)
	}
	if got := vaultToolAlias([]vaultRuntimeServer{one, sheets}, sheets); got != "google_sheets" {
		t.Fatalf("alias = %q", got)
	}
	if got := vaultToolAlias([]vaultRuntimeServer{remote}, remote); got != "server_notion" {
		t.Fatalf("alias = %q", got)
	}
	rows := []vaultRuntimeServer{one, two}
	if got := vaultToolAlias(rows, two); got != "notion_1b2c3d4e" {
		t.Fatalf("alias = %q", got)
	}
	server, tool, err := vaultBridgeMatch(rows, "notion_1b2c3d4e", "notion_1b2c3d4e__notion_fetch")
	if err != nil || server != vaultServerName(two.ID) || tool != two.Tools[0].Name {
		t.Fatalf("second connection: %s %s %v", server, tool, err)
	}
	// With two connections the bare app name names neither, so it is left for the resolver's plain refusal.
	if server, tool, err = vaultBridgeMatch(rows, "notion", "notion__notion-fetch"); err != nil || server != "" || tool != "" {
		t.Fatalf("bare app name picked a connection: %s %s %v", server, tool, err)
	}
	alike := vaultRuntimeServer{ID: "c-1", Provider: "app", Tools: []vaultRuntimeTool{{Name: "app__get-item"}, {Name: "app__get_item"}}}
	_, _, err = vaultBridgeMatch([]vaultRuntimeServer{alike}, "app", "APP__GET-ITEM")
	if err == nil || !strings.Contains(err.Error(), "app__get-item") || !strings.Contains(err.Error(), "app__get_item") {
		t.Fatalf("different tools must be refused with both names: %v", err)
	}
}
