package main

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A move probes only the Codex threads that already have a lease; ordinary
// discovery still sees the whole catalog.
func TestCodexMoveDiscoveryProbesOnlyLeasedThreads(t *testing.T) {
	leased := codexProbeCandidates([]agentworksclient.AgentAttachment{{Provider: "codex", SessionID: "leased"}, {Provider: "claude", SessionID: "other-provider"}})
	if len(leased) != 1 || leased["leased"] != "" {
		t.Fatalf("lease candidates: %v", leased)
	}
	catalog := map[string]string{"leased": "/work/a", "idle-1": "/work/b", "idle-2": "/work/c"}
	move := map[string]string{"leased": ""}
	mergeCodexCatalog(move, catalog, true)
	if len(move) != 1 || move["leased"] != "/work/a" {
		t.Fatalf("a move must probe only the leased thread, with its working directory: %v", move)
	}
	all := map[string]string{"leased": ""}
	mergeCodexCatalog(all, catalog, false)
	if len(all) != 3 {
		t.Fatalf("ordinary discovery must still list the catalog: %v", all)
	}
}
