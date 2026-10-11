package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspaceclient "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func TestDedicatedWorkflowStepSessionsSetExecutionWorkingDirDirectly(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath("Workflow/testing")
	hcpo.selectedRunFolder = "iteration-0/default"

	const want = "Workflow/testing/runs/iteration-0/default/execution"
	for _, agentKind := range []string{"exec", "agent", "todo"} {
		t.Run(agentKind, func(t *testing.T) {
			sessionID := "cwd-" + agentKind
			t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })

			hcpo.configureSubAgentSessionGuard(
				sessionID,
				agentKind,
				"step-one",
				[]string{want},
				[]string{want + "/step-one"},
			)

			cfg := common.GetSessionShellConfig(sessionID)
			if cfg == nil {
				t.Fatal("dedicated step session config was not created")
			}
			if cfg.WorkingDir != want {
				t.Fatalf("WorkingDir = %q, want %q", cfg.WorkingDir, want)
			}
		})
	}
}

// This exercises the same session-aware workspace client path used behind the
// MCP bridge: no context session is supplied, so MCP_SESSION_ID must select the
// dedicated step config and put its cwd on the outgoing shell request.
func TestWorkflowStepWorkingDirReachesShellBridgeRequest(t *testing.T) {
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath("Workflow/testing")
	hcpo.selectedRunFolder = "iteration-0/default"

	const (
		sessionID = "msgseq-cwd-bridge"
		want      = "Workflow/testing/runs/iteration-0/default/execution"
	)
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	hcpo.configureSubAgentSessionGuard(
		sessionID,
		"agent",
		"step-one",
		[]string{want},
		[]string{want + "/step-one"},
	)
	common.SetSessionShellEnv(sessionID, map[string]string{
		"STEP_OUTPUT_DIR": "/workspace-docs/" + want + "/step-one",
	})

	var got workspaceclient.ExecuteShellCommandParams
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode shell request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"stdout": want, "exit_code": 0},
		})
	}))
	defer server.Close()

	client := workspaceclient.NewClient(server.URL, workspaceclient.WithExtraEnv(map[string]string{
		"MCP_SESSION_ID": sessionID,
	}))
	if _, err := client.ExecuteShellCommand(context.Background(), workspaceclient.ExecuteShellCommandParams{Command: "pwd"}); err != nil {
		t.Fatalf("execute shell through bridge client: %v", err)
	}
	if got.WorkingDirectory != want {
		t.Fatalf("bridge working_directory = %q, want %q", got.WorkingDirectory, want)
	}
}
