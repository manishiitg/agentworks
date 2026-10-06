package common

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/remoteplacement"
)

// EnforceRemoteWorkflowToolsMode keeps remote workflow operations on the
// workspace bridge. The CLI's private runtime/scratch directory is local, so
// check the owning workflow and logical working directory as well as the
// construction-time workspace path. Attached folders alone do not make a local
// Crew, Code or Vault session a remote workflow session.
func EnforceRemoteWorkflowToolsMode(sessionID, workspacePath, requested string) string {
	root := fsutil.WorkspaceDocsRoot()
	if remoteplacement.IsRemote(root, workspacePath) {
		return "mcp_only"
	}
	// Placement needs only the stored identity, not live capability resolution
	// (which can fetch a workflow manifest over the workspace API).
	sessionShellConfigsMu.RLock()
	var workflow, workingDir string
	if cfg := sessionShellConfigs[sessionID]; cfg != nil {
		workflow, workingDir = cfg.WorkflowPath, cfg.WorkingDir
	}
	sessionShellConfigsMu.RUnlock()
	if remoteplacement.IsRemote(root, workflow) || remoteplacement.IsRemote(root, workingDir) {
		return "mcp_only"
	}
	return requested
}
