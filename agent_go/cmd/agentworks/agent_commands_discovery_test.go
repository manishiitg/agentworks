package main

import (
	"fmt"
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

// A desktop catalog can hold thousands of threads (twelve thousand on one Mac). Probing them in
// id order ran out of time before reaching the chat that was open, so leased threads come first
// and then only the newest catalog threads, never the whole catalog.
func TestCodexDiscoveryProbesLeasedThenNewestThreadsOnly(t *testing.T) {
	candidates := map[string]string{"leased": "/work"}
	var recent []string
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("thread-%04d", i) // newest first
		candidates[id] = "/work"
		recent = append(recent, id)
	}
	recent = append(recent[:10], append([]string{"leased"}, recent[10:]...)...)
	order := codexProbeOrder(map[string]bool{"leased": true}, candidates, recent)
	if len(order) != 1+codexProbeMax || order[0] != "leased" || order[1] != "thread-0000" || order[len(order)-1] != fmt.Sprintf("thread-%04d", codexProbeMax-1) {
		t.Fatalf("probe order = %d ids, first %q, second %q, last %q", len(order), order[0], order[1], order[len(order)-1])
	}
}
