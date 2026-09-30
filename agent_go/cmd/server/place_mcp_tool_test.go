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

type placeMCPToolRegistrar struct {
	exec func(context.Context, map[string]interface{}) (string, error)
}

func (r *placeMCPToolRegistrar) RegisterCustomTool(_ string, _ string, _ map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), _ string) error {
	r.exec = exec
	return nil
}

func (r *placeMCPToolRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), _ time.Duration, category string) error {
	return r.RegisterCustomTool(name, description, params, exec, category)
}

// The Code agent connects a server to this Code from chat: it is added with
// the owner's login as the Code's own connection, and a provider that needs
// their own OAuth app is sent to the MCP tab (no secret in chat). Only the
// Code's owner connects, and nothing reaches another Code.
func TestManageMyMCPServersActsOnThisCodeOnly(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(catalogPath, []byte(`{"mcpServers":{"AcmeMail":{"url":"https://mcp.acme.example/mcp","protocol":"http","oauth":{"auth_url":"https://auth.acme.example/oauth/authorize","token_url":"https://auth.acme.example/oauth/token"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: catalogPath, logger: loggerv2.NewNoop()}
	codeRoot := "_users/owner/Chats/Code/projects/c0de"
	otherCode := "_users/owner/Chats/Code/projects/other"
	reg := &placeMCPToolRegistrar{}
	if err := api.registerPlaceMCPTool(reg, "owner", codeRoot, "https://agents.example.com/api/oauth/callback"); err != nil {
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
	if out := call(map[string]interface{}{"action": "list"}); !strings.Contains(out, `"catalog":"AcmeMail"`) || !strings.Contains(out, `"this_code_has":[]`) || !strings.Contains(out, `"you_can_connect":true`) {
		t.Fatalf("list = %s", out)
	}
	out := call(map[string]interface{}{"action": "connect", "catalog": "AcmeMail"})
	if !strings.Contains(out, "MCP") || !strings.Contains(out, "api/oauth/callback") {
		t.Fatalf("connect = %s", out)
	}
	if attached, _ := placeMCPAttachmentsFor(codeRoot); len(attached) != 1 || attached[0].Server != "acmemail" || attached[0].Owner != "owner" {
		t.Fatalf("attachments = %+v", attached)
	}
	if attached, _ := placeMCPAttachmentsFor(otherCode); len(attached) != 0 {
		t.Fatalf("connection reached another Code: %+v", attached)
	}
	if servers, _ := listPlaceMCPServers("owner"); len(servers) != 0 {
		t.Fatalf("connection stored in the person's own store: %v", servers)
	}
	if out := call(map[string]interface{}{"action": "list"}); !strings.Contains(out, `"this_code_has":[{"name":"acmemail"`) {
		t.Fatalf("list after connect = %s", out)
	}
	call(map[string]interface{}{"action": "remove", "name": "acmemail"})
	if attached, _ := placeMCPAttachmentsFor(codeRoot); len(attached) != 0 {
		t.Fatalf("not removed: %+v", attached)
	}
	if servers, _ := listPlaceMCPServers(placeMCPStoreID("owner", codeRoot)); len(servers) != 0 {
		t.Fatalf("server not removed: %v", servers)
	}

	// Someone who is not the Code's owner (a participant of a shared Code)
	// can list but not connect.
	guest := &placeMCPToolRegistrar{}
	if err := api.registerPlaceMCPTool(guest, "guest", codeRoot, "https://agents.example.com/api/oauth/callback"); err != nil {
		t.Fatal(err)
	}
	if _, err := guest.exec(context.Background(), map[string]interface{}{"action": "connect", "catalog": "AcmeMail"}); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("a non-owner connected to the Code: %v", err)
	}
}
