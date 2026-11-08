package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

func TestMCPToolIdentityUsesSessionOwnerAndRejectsMissingOrConflictingUsers(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("chat-alice", "alice")
	ctx := executor.WithSessionID(context.Background(), "chat-alice")
	if got, err := api.mcpToolUserID(ctx); err != nil || got != "alice" {
		t.Fatalf("bridge identity = %q, %v", got, err)
	}
	for _, bad := range []context.Context{
		context.Background(),
		executor.WithSessionID(context.Background(), "unknown"),
		context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "bob"}),
		context.WithValue(ctx, common.UserIDKey, "bob"),
	} {
		if _, err := api.mcpToolUserID(bad); err == nil {
			t.Fatal("missing/mismatched identity must not fall back to default")
		}
	}
	for _, direct := range []context.Context{
		context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice"}),
		context.WithValue(context.Background(), common.UserIDKey, "alice"),
	} {
		if got, err := api.mcpToolUserID(direct); err != nil || got != "alice" {
			t.Fatalf("direct execution identity = %q, %v", got, err)
		}
	}
}

// A connector's status comes from the person's own live Vault grants (PLAT-856, ee0e8ef837); the private
// discovery cache it replaced is gone. One person's grant must not show as connected for another.
func TestMCPToolStatusShowsOnlyThePersonsVaultGrants(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows := []map[string]any{}
		if r.Header.Get("X-CapLayer-Actor") == "status-alice" {
			rows = append(rows, map[string]any{"id": "c-status-1", "label": "c-status-1", "provider": "notion", "tools": []map[string]any{{"name": "notion_c-status-1__search"}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": rows})
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	name := vaultServerName("c-status-1")
	cfg := mcpclient.MCPServerConfig{URL: "https://provider.example/mcp", OAuth: &oauth.OAuthConfig{}}
	if status := api.mcpToolStatusForUser(name, "status-alice", cfg); status.Connection != connectionConnected {
		t.Fatalf("granted connection not shown: %+v", status)
	}
	if status := api.mcpToolStatusForUser(name, "status-bob", cfg); status.Connection == connectionConnected {
		t.Fatalf("Alice's grant was shown as connected for Bob: %+v", status)
	}
	if cfg.OAuth.TokenFile != "" {
		t.Fatal("status lookup mutated shared OAuth config")
	}
}

func TestMCPInstallToolRejectsAnonymousBridgeBeforeConfigMutation(t *testing.T) {
	api := &StreamingAPI{}
	registrar := &recordingRegistrar{}
	if err := api.registerMultiAgentMCPServerTools(registrar, nil); err != nil {
		t.Fatal(err)
	}
	_, err := registrar.tools["install_mcp_server"].exec(context.Background(), map[string]interface{}{"name": "Connector", "url": "https://provider.example/mcp"})
	if err == nil || !strings.Contains(err.Error(), "authenticated user") {
		t.Fatalf("anonymous install should fail before touching config: %v", err)
	}
}

// schemaRegistrar keeps each tool's declared parameter schema (the shared recordingRegistrar drops it).
type schemaRegistrar struct {
	params map[string]map[string]interface{}
}

func (r *schemaRegistrar) RegisterCustomTool(name, _ string, params map[string]interface{}, _ func(context.Context, map[string]interface{}) (string, error), _ string) error {
	if r.params == nil {
		r.params = map[string]map[string]interface{}{}
	}
	r.params[name] = params
	return nil
}

// The per-chat executor of trigger_mcp_discovery discovers one connection and requires its name; the
// registered schema must declare that argument, or the call is rejected with or without a name
// ("unknown field(s): name" / "name is required") and a private connection is never discovered.
func TestTriggerMCPDiscoverySchemaDeclaresTheConnectionName(t *testing.T) {
	api := &StreamingAPI{}
	registrar := &schemaRegistrar{}
	if err := api.registerMultiAgentMCPServerTools(registrar, nil); err != nil {
		t.Fatal(err)
	}
	params, ok := registrar.params["trigger_mcp_discovery"]
	if !ok {
		t.Fatal("trigger_mcp_discovery is not registered")
	}
	properties, _ := params["properties"].(map[string]interface{})
	if _, declared := properties["name"]; !declared {
		t.Fatalf("trigger_mcp_discovery declares no name argument: %v", params)
	}
}
