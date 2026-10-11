package step_based_workflow

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/spf13/viper"
)

func TestAgentCreationFailureRetiresAllocatedSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	previous := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	h := newAgentFactoryTestOrchestrator(t)
	h.SetWorkspacePath("Workflow/testing")
	h.selectedRunFolder = "iteration-0/default"
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "writer"}}

	// Persisted history is not authority to delete somebody else's live state.
	priorID := h.agentSequenceRuntimeSessionID(nil, "step-1", "writer")
	common.SetSessionWorkingDir(priorID, "prior-runtime")
	t.Cleanup(func() { common.ClearSessionShellConfig(priorID) })
	s := &agentSequenceSession{RuntimeSessionID: priorID}
	for attempt := 0; attempt < 2; attempt++ {
		_, _, err := h.getAgentRuntime(context.Background(), step, "step-1", s, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "no valid LLM") {
			t.Fatalf("expected failure before provider launch, got %v", err)
		}
		id := s.RuntimeSessionID
		t.Cleanup(func() {
			common.ClearSessionShellConfig(id)
			mcpagent.CloseSession(id)
			mcpagent.ClearSessionsStopped([]string{id})
		})
		if id == priorID || s.runtime != nil || common.GetSessionShellConfig(id) != nil {
			t.Fatal("failed setup retained an allocation or reclaimed persisted runtime identity")
		}
		if !mcpclient.GetSessionRegistry().IsSessionStopped(id) {
			t.Fatal("failed runtime can still reconnect")
		}
		h.closeAgentRuntime(s, "caller cleanup after failure")
		if common.GetSessionShellConfig(priorID) == nil {
			t.Fatal("failure cleanup retired an unrelated persisted runtime")
		}
	}
}

type runtimeCleanupClient struct {
	mcpclient.ClientInterface
	closes int
}

func (c *runtimeCleanupClient) Close() error { c.closes++; return nil }

func TestAgentCleanupClosesOnlyOwnedMCPConnections(t *testing.T) {
	h := newAgentFactoryTestOrchestrator(t)
	id := h.agentSequenceRuntimeSessionID(nil, "step-1", "writer")
	sharedID := h.agentSequenceRuntimeSessionID(nil, "group", "browser")
	registry := mcpclient.GetSessionRegistry()
	owned, shared := &runtimeCleanupClient{}, &runtimeCleanupClient{}
	registry.StoreConnection(id, "fixture", owned)
	registry.StoreServerConfig(id, "lazy-fixture", mcpclient.MCPServerConfig{})
	registry.StoreConnection(sharedID, "fixture", shared)
	common.SetSessionBrowserSessionID(id, sharedID)
	t.Cleanup(func() {
		mcpagent.CloseSession(id)
		mcpagent.CloseSession(sharedID)
		mcpagent.ClearSessionsStopped([]string{id, sharedID})
		common.ClearSessionShellConfig(id)
	})
	s := &agentSequenceSession{runtime: &agentSequenceRuntime{SessionID: id}}
	h.closeAgentRuntime(s, "completed")
	h.closeAgentRuntime(s, "duplicate cleanup")
	if registry.HasSession(id) || owned.closes != 1 || !registry.IsSessionStopped(id) {
		t.Fatal("owned connections were not retired exactly once")
	}
	if !registry.HasSession(sharedID) || shared.closes != 0 || registry.IsSessionStopped(sharedID) {
		t.Fatal("sequence cleanup interfered with the shared browser/group owner")
	}
}

func TestAgentResumedHistoryDoesNotReclaimOldRuntime(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.selectedRunFolder = "iteration-0/default"
	oldID := hcpo.agentSequenceRuntimeSessionID(nil, "step-1", "writer")
	saved, err := json.Marshal(&agentSequenceSession{RuntimeSessionID: oldID, ExecutionTurnCount: 3})
	if err != nil {
		t.Fatal(err)
	}
	var resumed agentSequenceSession
	if err := json.Unmarshal(saved, &resumed); err != nil {
		t.Fatal(err)
	}
	newID := hcpo.agentSequenceRuntimeSessionID(&resumed, "step-1", "writer")
	if newID == oldID {
		t.Fatal("resumed history reclaimed the prior execution's runtime")
	}
	resumed.runtime = &agentSequenceRuntime{SessionID: newID}
	if got := hcpo.agentSequenceRuntimeSessionID(&resumed, "step-1", "writer"); got != newID {
		t.Fatalf("next turn failed to reuse its live runtime: %q != %q", got, newID)
	}
	if resumed.ExecutionTurnCount != 3 {
		t.Fatal("runtime allocation changed persisted conversation metadata")
	}
}

// A cancelled run may finish unwinding after its replacement has started.
// Exercise the production cleanup path: it must not revoke the replacement's
// cwd, guard, environment, or managed DB capability.
func TestAgentOldRuntimeCleanupPreservesReplacement(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath("Workflow/testing")
	hcpo.selectedRunFolder = "iteration-0/default"
	hcpo.currentGroupName = "default"

	start := func() *agentSequenceSession {
		id := hcpo.agentSequenceRuntimeSessionID(nil, "step-11", "execute-actions")
		cwd := hcpo.workflowStepShellWorkingDir()
		hcpo.configureSubAgentSessionGuard(id, "agent", "execute-actions", []string{cwd}, []string{cwd + "/execute-actions"})
		hcpo.setAgentShellEnv(id, "step-11", "execute-actions")
		t.Cleanup(func() { common.ClearSessionShellConfig(id); mcpagent.ClearSessionsStopped([]string{id}) })
		return &agentSequenceSession{runtime: &agentSequenceRuntime{SessionID: id}}
	}
	old := start()
	replacement := start()
	oldID := old.runtime.SessionID
	newID := replacement.runtime.SessionID
	wantCfg := common.GetSessionShellConfig(newID)
	wantEnv := common.GetSessionShellEnv(newID)
	hcpo.closeAgentRuntime(old, "cancelled execution finished unwinding")

	cfg := common.GetSessionShellConfig(newID)
	if cfg == nil || cfg.WorkingDir != hcpo.workflowStepShellWorkingDir() || !cfg.FolderGuardSet || !reflect.DeepEqual(cfg, wantCfg) {
		t.Fatalf("old runtime cleanup revoked replacement shell configuration: %+v", cfg)
	}
	if common.GetSessionShellConfig(oldID) != nil {
		t.Fatal("finished runtime retained its shell configuration")
	}
	for _, key := range []string{"STEP_OUTPUT_DIR", "STEP_EXECUTION_DIR", workflowDBAccessEnv} {
		if got := common.GetSessionShellEnv(newID)[key]; got == "" || got != wantEnv[key] {
			t.Errorf("replacement lost %s: got %q, want %q", key, got, wantEnv[key])
		}
	}
}
