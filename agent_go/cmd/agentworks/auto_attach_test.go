package main

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A new chat is reconnected on its own only when exactly one earlier agent of the
// folder exists; with several it only hints, and the folder switch and the kill
// switch stop everything. (The one-agent path attaches through the bridge and is
// covered by the live check.)
func TestAutoAttachNeverGuessesBetweenAgents(t *testing.T) {
	root := t.TempDir()
	folder := t.TempDir()
	req := agentworksclient.LocalBridgeRequest{Provider: "claude", SessionID: "s", HostKey: "h"}
	a := agentworksclient.AgentAttachment{AgentName: "one"}
	rememberAutoAttach(root, "claude", folder, a, "/login.json")
	rememberAutoAttach(root, "claude", folder, agentworksclient.AgentAttachment{AgentName: "two"}, "/login.json")
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); !strings.Contains(line, "/agentworks attach") || strings.Contains(line, "reconnected") {
		t.Fatalf("several earlier agents must only produce the attach hint: %q", line)
	}
	setAutoAttach(root, "claude", folder, false)
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); line != "" {
		t.Fatalf("auto off must be silent: %q", line)
	}
	setAutoAttach(root, "claude", folder, true)
	forgetAutoAttach(root, "claude", folder, "")
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); line != "" {
		t.Fatalf("a detached folder has nothing to reconnect: %q", line)
	}
	rememberAutoAttach(root, "claude", folder, a, "/login.json")
	t.Setenv("AGENTWORKS_AUTO_ATTACH", "0")
	if line := autoAttachOnStart(context.Background(), root, &options{}, req, folder); line != "" {
		t.Fatalf("the kill switch must disable it: %q", line)
	}
}
