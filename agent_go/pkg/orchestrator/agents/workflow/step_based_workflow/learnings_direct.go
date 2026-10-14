package step_based_workflow

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
)

// learningsGlobalFileMutex serializes direct-mode writes to learnings/_global/
// across parallel steps. Parallel sub-agents under a todo_task each have their
// own MCP session + folder guard, but the _global skill file is shared — without
// a mutex they'd race each other's diff_patches. Held for the duration of the
// direct-learnings continuation turn (see controller_execution.go).
//
// Uses a simple in-process mutex since the turn is inline and short.
//
// LIMITATION (intentional for v1): this is an in-process mutex. It does NOT
// serialize writes across multiple orchestrator processes sharing the same
// workspace (e.g. a multi-node deployment). If that topology becomes real,
// this needs to upgrade to a file lock (flock on learnings/_global/SKILL.md)
// or equivalent cross-process primitive. Not addressed in v1.
var learningsGlobalFileMutex sync.Mutex

// prepareDirectLearningTurn temporarily makes the shared learnings folder
// writable for a direct-learning continuation. It intentionally does not change
// the shell working directory; the learning prompt uses explicit absolute paths
// so normal step/runtime cwd behavior stays untouched.
func (hcpo *StepBasedWorkflowOrchestrator) prepareDirectLearningTurn(agent agents.OrchestratorAgent, addedPaths []string) func() {
	if agent == nil {
		return func() {}
	}

	var restoreFns []func()
	if cfg := agent.GetConfig(); cfg != nil {
		prevRead := append([]string{}, cfg.FolderGuardReadPaths...)
		prevWrite := append([]string{}, cfg.FolderGuardWritePaths...)
		cfg.FolderGuardReadPaths = common.DeduplicateStrings(append(cfg.FolderGuardReadPaths, addedPaths...))
		cfg.FolderGuardWritePaths = common.DeduplicateStrings(append(cfg.FolderGuardWritePaths, addedPaths...))
		restoreFns = append(restoreFns, func() {
			cfg.FolderGuardReadPaths = prevRead
			cfg.FolderGuardWritePaths = prevWrite
		})

		subSessionID := strings.TrimSpace(cfg.MCPSessionID)
		if subSessionID != "" {
			prevCfg := common.GetSessionShellConfig(subSessionID)
			hadPrevCfg := prevCfg != nil
			prevSessionRead := []string{}
			prevSessionWrite := []string{}
			prevSessionWorkingDir := ""
			if prevCfg != nil {
				prevSessionRead = append([]string{}, prevCfg.ReadPaths...)
				prevSessionWrite = append([]string{}, prevCfg.WritePaths...)
				prevSessionWorkingDir = prevCfg.WorkingDir
			}
			widenedRead := common.DeduplicateStrings(append(append([]string{}, prevSessionRead...), addedPaths...))
			widenedWrite := common.DeduplicateStrings(append(append([]string{}, prevSessionWrite...), addedPaths...))
			common.SetSessionFolderGuard(subSessionID, widenedRead, widenedWrite)
			hcpo.grantSessionCDPHostDownloadsReadWrite(subSessionID)
			restoreFns = append(restoreFns, func() {
				if hadPrevCfg {
					common.SetSessionFolderGuard(subSessionID, prevSessionRead, prevSessionWrite)
					if prevSessionWorkingDir != "" {
						common.SetSessionWorkingDir(subSessionID, prevSessionWorkingDir)
					}
					hcpo.grantSessionCDPHostDownloadsReadWrite(subSessionID)
				} else {
					common.ClearSessionShellConfig(subSessionID)
				}
			})
		}
	}

	return func() {
		for i := len(restoreFns) - 1; i >= 0; i-- {
			restoreFns[i]()
		}
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) directLearningsPromptTargetPath() string {
	workflowPath := strings.TrimSpace(hcpo.GetWorkspacePath())
	rel := filepath.Join(workflowPath, LearningsFolderName, GlobalLearningID)
	docsRoot := strings.TrimSpace(GetPromptDocsRoot())
	if docsRoot == "" {
		return rel
	}
	return filepath.Join(docsRoot, rel)
}
