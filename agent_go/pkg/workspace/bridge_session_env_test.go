package workspace

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// A shell's tool-call token always matches the session it finally runs as.
func TestShellEnvCarriesItsOwnSessionsToken(t *testing.T) {
	common.SetBridgeTokenSecret("server-secret")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })

	// Client built for the parent, session layer switched to a step.
	env := mergeShellCommandEnv(
		map[string]string{"MCP_API_URL": "http://h/s/parent", "MCP_SESSION_ID": "parent", "MCP_API_TOKEN": common.BridgeTokenForSession("parent")},
		map[string]string{"MCP_API_URL": "http://h/s/step-1", "MCP_SESSION_ID": "step-1"},
		nil,
	)
	bindShellBridgeSession(env, "step-1")
	if env["MCP_API_TOKEN"] != common.BridgeTokenForSession("step-1") {
		t.Fatal("the step shell must carry the step session's token")
	}

	// Sessionless client run for a session: scoped like a session client.
	env = map[string]string{"MCP_API_URL": "http://h"}
	bindShellBridgeSession(env, "chat-a")
	if env["MCP_SESSION_ID"] != "chat-a" || env["MCP_API_URL"] != "http://h/s/chat-a" || env["MCP_API_TOKEN"] != common.BridgeTokenForSession("chat-a") {
		t.Fatalf("sessionless client run for a session must be scoped to it: %v", env)
	}

	// No session at all: no token, never a global one.
	env = map[string]string{"MCP_API_URL": "http://h", "MCP_API_TOKEN": "server-secret"}
	bindShellBridgeSession(env, "")
	if _, has := env["MCP_API_TOKEN"]; has {
		t.Fatal("a shell with no session must not carry a token")
	}
}
