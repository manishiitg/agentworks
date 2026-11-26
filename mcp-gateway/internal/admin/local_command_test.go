package admin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestLocalCommandFixtureServer is not a test: started by TestLocalCommandConnection as a local command MCP server, it
// serves one tool over stdio that reports the environment it was given.
func TestLocalCommandFixtureServer(t *testing.T) {
	if os.Getenv("LOCAL_COMMAND_FIXTURE") != "1" {
		t.Skip("fixture process only")
	}
	s := server.NewMCPServer("local-fixture", "1")
	s.AddTool(mcp.NewTool("env"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("key=" + os.Getenv("FIXTURE_KEY") + " leak=" + os.Getenv("GATEWAY_LEAK_CHECK")), nil
	})
	_ = server.ServeStdio(s)
	os.Exit(0)
}

// A local command connection runs only on a Vault that allows local commands, gets its own environment but never
// Vault's settings, keeps its launch out of every returned record, and is never copied to a Team.
func TestLocalCommandConnection(t *testing.T) {
	t.Setenv("GATEWAY_LEAK_CHECK", "vault-setting")
	launch, _ := json.Marshal(upstream.LocalCommand{
		Command: os.Args[0], Args: []string{"-test.run=^TestLocalCommandFixtureServer$"},
		Env: map[string]string{"LOCAL_COMMAND_FIXTURE": "1", "FIXTURE_KEY": "secret-value-123"},
	})
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	for _, id := range []string{"alice", "team-1"} {
		st.AddUser(store.User{ID: id, WorkspaceID: "w", Email: id + "@example.com"})
	}
	mine, _ := st.EnsurePersonalVault("w", "alice", "v-alice")
	team, _ := st.EnsurePersonalVault("w", "team-1", "v-team")
	ctx := context.Background()

	off := &Admin{Store: st, Gateway: mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{}), WorkspaceID: "w"}
	if _, err := off.addConnectorCommandFor(ctx, mine, "fixture", "Fixture", "", string(launch)); !errors.Is(err, upstream.ErrLocalCommandsOff) {
		t.Fatalf("a Vault without local commands added one: %v", err)
	}

	g := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowLocalCommands: true})
	a := &Admin{Store: st, Gateway: g, WorkspaceID: "w"}
	if _, err := a.addConnectorCommandFor(ctx, "", "fixture", "Fixture", "", string(launch)); err == nil {
		t.Fatal("a local command went into the company Vault")
	}
	c, err := a.addConnectorCommandFor(ctx, mine, "fixture", "Fixture", "", string(launch))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VaultAttachConnector("alice", mine, c.ID); err != nil {
		t.Fatal(err)
	}
	saved, _ := st.GetConnector(c.ID)
	record, _ := json.Marshal(saved)
	if strings.Contains(string(record), "secret-value-123") || strings.Contains(string(record), "TestLocalCommandFixtureServer") {
		t.Fatalf("the launch leaked into the connection record: %s", record)
	}
	if saved.Transport != upstream.CommandTransport || saved.AuthScheme != upstream.CommandAuthScheme {
		t.Fatalf("saved as %q/%q", saved.Transport, saved.AuthScheme)
	}

	opts := upstream.DialOptions{AllowLocalCommands: true, Transport: upstream.CommandTransport, AuthScheme: upstream.CommandAuthScheme, BearerToken: st.ConnectorBearer(c.ID)}
	up, err := upstream.DialWithOptions(ctx, saved.UpstreamURL, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	result, err := up.Call(ctx, "env", nil)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if text != "key=secret-value-123 leak=" {
		t.Fatalf("program environment = %q", text)
	}

	if _, err := a.copyVaultConnector(ctx, "alice", mine, c.ID, team, "team-1"); err == nil || !strings.Contains(err.Error(), "Mac") {
		t.Fatalf("a local command was copied to a Team: %v", err)
	}

	for _, bad := range []upstream.LocalCommand{{Command: "npx"}, {Command: "/bin/echo", Env: map[string]string{"GATEWAY_STATE_DIR": "x"}}} {
		raw, _ := json.Marshal(bad)
		if _, err := upstream.ParseLocalCommand(string(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
