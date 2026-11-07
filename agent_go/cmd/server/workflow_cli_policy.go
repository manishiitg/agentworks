package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Every execution child is confined using its final dedicated session guard.
// PLAT-746-355: Workshop controllers do not carry the parent chat's launch policy.
func (api *StreamingAPI) resolveWorkflowCLIPolicy(ctx context.Context, sessionID, provider, workingDir string) (*llmtypes.CLISecurityPolicy, error) {
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil || !cfg.FolderGuardSet || strings.TrimSpace(cfg.WorkflowPath) == "" {
		log.Printf("[WORKFLOW_CLI] session=%s provider=%s native=false reason=%q", sessionID, provider, "workflow agent has no dedicated folder guard")
		return nil, nil
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return nil, err
	}
	policy, err := api.cliSecurityStore.Resolve(GetUserIDFromContext(ctx), provider, nil, nil)
	if err != nil {
		return nil, err
	}
	// Execution stays with the app account (PLAT-442), as on the direct workflow
	// API path. Do not inherit the parent Crew owner's slot or broad write grants.
	workflowRoot := codingAgentWorkspaceWorkingDir(cfg.WorkflowPath)
	policy.RunAs = llmtypes.RunAs{Declared: true, Root: workflowRoot}
	llmtypes.DeclareRunAs(workflowRoot, policy.RunAs)
	key := sha256.Sum256([]byte(GetUserIDFromContext(ctx) + "\x00" + sessionID))
	runtimeDir := filepath.Join(root, "workflow-cli-runtimes", fmt.Sprintf("%x", key))
	host := cliConfinementHost()
	host.WorkflowExecution = true
	resolved, decision, why := host.Policy(sessionID, provider, workingDir, policy, runtimeDir)
	if decision == cliRunBridgeOnly || decision == cliRunPersonalWindows {
		if decision == cliRunPersonalWindows {
			why = "workflow agents require OS confinement"
		}
		log.Printf("[WORKFLOW_CLI] session=%s provider=%s native=false reason=%q", sessionID, provider, why)
		return nil, nil
	}
	log.Printf("[WORKFLOW_CLI] session=%s provider=%s confinement=%s native=true reads=%d writes=%d blocked=%d+%d run_as=app", sessionID, provider, decision, len(resolved.WorkspaceReadPaths), len(resolved.WorkspaceWritePaths), len(resolved.BlockedPaths), len(resolved.BlockedWritePaths))
	return resolved, nil
}
