package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Personal Slack is the person's own Slack connection in a vault they OWN (owner, 2026-10-21: one store, Vault only). A
// Slack connection in a vault they are only a member of is not theirs and is never offered as their account.
func TestPersonalSlackIsTheConnectionInAVaultThePersonOwns(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"vaults": []any{
			map[string]any{"group": map[string]any{"ID": "v-own", "Name": personalVaultName}, "role": "owner", "connections": []any{
				map[string]any{"id": "c-own", "label": "My Slack account", "provider": "slack", "status": "active"},
				map[string]any{"id": "c-notion", "label": "Notion", "provider": "notion", "status": "active"}}},
			map[string]any{"group": map[string]any{"ID": "v-team", "Name": "Team"}, "role": "member", "connections": []any{
				map[string]any{"id": "c-other", "label": "Someone else's Slack", "provider": "slack", "status": "active"}}},
		}})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	api := &StreamingAPI{mcpConfigPath: filepath.Join("..", "..", "configs", "mcp_servers_clean.json"), logger: loggerv2.NewNoop()}
	r := httptest.NewRequest(http.MethodGet, "/api/integrations/slack/accounts", nil).WithContext(personContext("alice"))
	out, err := api.personalSlackAccounts(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 1 || out.Accounts[0].ID != "c-own" || !out.Accounts[0].Connected || !out.Accounts[0].Mine || out.Accounts[0].Label != "Your Slack" {
		t.Fatalf("accounts = %+v", out.Accounts)
	}
}
