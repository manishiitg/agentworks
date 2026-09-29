package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type personalMCPToolRegistrar struct {
	exec func(context.Context, map[string]interface{}) (string, error)
}

func (r *personalMCPToolRegistrar) RegisterCustomTool(_ string, _ string, _ map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), _ string) error {
	r.exec = exec
	return nil
}

func (r *personalMCPToolRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), _ time.Duration, category string) error {
	return r.RegisterCustomTool(name, description, params, exec, category)
}

// The Code agent connects the person's own server from chat: it is added as
// theirs, switched on in this Code, and a provider that needs their own OAuth
// app is sent to the MCPs tab (no secret in chat). Nothing reaches another
// person.
func TestManageMyMCPServersActsForThePinnedPersonOnly(t *testing.T) {
	withPersonalMCPRoot(t)
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(catalogPath, []byte(`{"mcpServers":{"GoogleGmail":{"url":"https://gmailmcp.googleapis.com/mcp/v1","protocol":"http","oauth":{"auth_url":"https://accounts.google.com/o/oauth2/v2/auth","token_url":"https://oauth2.googleapis.com/token"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: catalogPath, logger: loggerv2.NewNoop()}
	codeRoot := "_users/owner/Chats/Code/projects/c0de"
	reg := &personalMCPToolRegistrar{}
	if err := api.registerPersonalMCPTool(reg, "owner", codeRoot, "https://agents.example.com/api/oauth/callback"); err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]interface{}) string {
		t.Helper()
		out, err := reg.exec(context.Background(), args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	if out := call(map[string]interface{}{"action": "list"}); !strings.Contains(out, `"catalog":"GoogleGmail"`) || !strings.Contains(out, `"your_servers":[]`) {
		t.Fatalf("list = %s", out)
	}
	out := call(map[string]interface{}{"action": "connect", "catalog": "GoogleGmail"})
	if !strings.Contains(out, "MCPs") || !strings.Contains(out, "api/oauth/callback") {
		t.Fatalf("connect = %s", out)
	}
	if enabled, _ := personalMCPEnabled("owner", codeRoot); len(enabled) != 1 || enabled[0] != "googlegmail" {
		t.Fatalf("enabled = %v", enabled)
	}
	if servers, _ := listPersonalMCPServers("other"); len(servers) != 0 {
		t.Fatalf("other got servers: %v", servers)
	}
	call(map[string]interface{}{"action": "disable", "name": "googlegmail"})
	if enabled, _ := personalMCPEnabled("owner", codeRoot); len(enabled) != 0 {
		t.Fatalf("still enabled: %v", enabled)
	}
	call(map[string]interface{}{"action": "remove", "name": "googlegmail"})
	if servers, _ := listPersonalMCPServers("owner"); len(servers) != 0 {
		t.Fatalf("not removed: %v", servers)
	}
}
