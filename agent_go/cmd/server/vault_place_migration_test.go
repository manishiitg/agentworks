package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/oauth"
)

// The one-off move of the old project-connection store into Vault: a dry run says what each attachment would do, and
// changes nothing. Catalog/custom OAuth, key/header and no-auth records all move.
func TestPlaceMigrationDryRunClassifiesAndChangesNothing(t *testing.T) {
	withMCPConnectionsRoot(t)
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "place-migration-test-secret-with-enough-entropy")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/connectors" {
			json.NewEncoder(w).Encode(map[string]any{"connectors": []any{}})
			return
		}
		if r.URL.Path != "/api/admin/catalog" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"providers": []any{
			map[string]any{"Name": "Slack", "URL": "https://mcp.slack.com/mcp"},
			map[string]any{"Name": "Notion", "URL": "https://mcp.notion.com/mcp"},
			map[string]any{"Name": "Context7", "URL": "https://mcp.context7.com/mcp"},
		}})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	signIn := &oauth.OAuthConfig{AuthURL: "https://idp.example.com/authorize", TokenURL: "https://idp.example.com/token"}
	servers := []placeMCPServer{
		{Name: "slack_work", Label: "Work Slack", Catalog: "Slack", URL: "https://mcp.slack.com/mcp", Transport: "http", OAuth: &oauth.OAuthConfig{AuthURL: "https://slack.com/oauth/v2_user/authorize", TokenURL: "https://slack.com/api/oauth.v2.user.access"}},
		{Name: "notion", Label: "Notion", Catalog: "Notion", URL: "https://mcp.notion.com/mcp", Transport: "http", OAuth: signIn},
		{Name: "jam", Label: "Jam", URL: "https://mcp.jam.example.com/mcp", Transport: "http", OAuth: signIn},
		{Name: "crm", Label: "CRM", URL: "https://crm.example.com/mcp", Transport: "http", Headers: map[string]placeMCPHeader{"Authorization": {Secret: "CRM_TOKEN", Format: "Bearer {}"}}},
		{Name: "context7", Label: "Context7", URL: "https://mcp.context7.com/mcp", Transport: "http"},
		{Name: "gmail", Label: "Gmail", Catalog: "GoogleGmail", URL: "https://gmailmcp.googleapis.com/mcp/v1", Transport: "http", OAuth: signIn},
	}
	for _, server := range servers {
		if _, err := addPlaceMCPServer("alice", server); err != nil {
			t.Fatalf("%s: %v", server.Name, err)
		}
		if err := attachPersonalLoginToPlace("alice", server.Name, "Workflow/orders"); err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	rows, err := api.migratePlaceConnections(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, row := range rows {
		got[row.Server] = row.Kind + ":" + row.Result
	}
	want := map[string]string{"slack_work": "slack:would move", "notion": "oauth:would move", "jam": "oauth:would move", "crm": "key:would move", "context7": "noauth:would move", "gmail": "oauth:would move"}
	for server, result := range want {
		if got[server] != result {
			t.Fatalf("%s = %q, want %q (all: %v)", server, got[server], result, got)
		}
	}
	if attached, _ := placeMCPAttachmentsFor("Workflow/orders"); len(attached) != len(servers) {
		t.Fatalf("a dry run changed the attachments: %+v", attached)
	}
}
