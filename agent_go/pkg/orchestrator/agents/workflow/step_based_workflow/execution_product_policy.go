package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The validated workflow kind selects a product policy. Steps cannot override it.
// loadCodeLayout applies it at every plan-read/direct-execution entry point,
// including immutable Relay release workspaces.
func (hcpo *StepBasedWorkflowOrchestrator) configureExecutionProduct(kind string) error {
	enabled, err := platformStoresForProduct(kind)
	hcpo.platformStoresDisabled.Store(!enabled)
	return err
}

func platformStoresForProduct(kind string) (bool, error) {
	if strings.TrimSpace(kind) != "relay" {
		return true, nil
	}
	enabled, err := relayproduct.PlatformStoresEnabled()
	if err != nil {
		return false, fmt.Errorf("load Relay execution policy: %w", err)
	}
	return enabled, nil
}

// Managed Builder sessions are set up outside the controller. Use the same
// product policy on setup and restore, without depending on a step config.
func workspacePlatformStoresEnabled(workspacePath string) bool {
	raw, err := os.ReadFile(filepath.Join(GetPromptDocsRoot(), workspacePath, "workflow.json"))
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	var m struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	enabled, err := platformStoresForProduct(m.Kind)
	return err == nil && enabled
}

func (hcpo *StepBasedWorkflowOrchestrator) platformStoresEnabled() bool {
	return !hcpo.platformStoresDisabled.Load()
}
func (hcpo *StepBasedWorkflowOrchestrator) resolveDBAccess(cfg *AgentConfigs) string {
	if !hcpo.platformStoresEnabled() {
		return DBAccessNone
	}
	return resolveDBAccess(cfg)
}
func (hcpo *StepBasedWorkflowOrchestrator) resolveLearningsAccess(cfg *AgentConfigs) string {
	if !hcpo.platformStoresEnabled() {
		return LearningsAccessNone
	}
	return resolveLearningsAccess(cfg)
}
func (hcpo *StepBasedWorkflowOrchestrator) canReadLearnings(cfg *AgentConfigs, step PlanStepInterface) bool {
	return hcpo.platformStoresEnabled() && canReadLearnings(cfg, step)
}
func (hcpo *StepBasedWorkflowOrchestrator) canWriteLearnings(cfg *AgentConfigs, step PlanStepInterface) bool {
	return hcpo.platformStoresEnabled() && canWriteLearnings(cfg, step)
}
func (hcpo *StepBasedWorkflowOrchestrator) shouldDirectWriteLearnings(cfg *AgentConfigs, step PlanStepInterface) bool {
	return hcpo.platformStoresEnabled() && shouldDirectWriteLearnings(cfg, step)
}
func (hcpo *StepBasedWorkflowOrchestrator) resolveExecutionLearningsAccess(cfg *AgentConfigs, step PlanStepInterface) string {
	if !hcpo.platformStoresEnabled() {
		return LearningsAccessNone
	}
	return resolveExecutionLearningsAccess(cfg, step)
}

func platformStoreBlockedPaths(workspacePath string) []string {
	blocked := []string{filepath.Join(workspacePath, DBFolderName), filepath.Join(workspacePath, KnowledgebaseFolderName), filepath.Join(workspacePath, LearningsFolderName)}
	if sources, err := workflowkb.Resolve(GetPromptDocsRoot(), workspacePath, nil); err == nil {
		for _, source := range sources {
			if source.Available {
				blocked = append(blocked, source.Path)
			}
		}
	}
	return common.DeduplicateStrings(blocked)
}
func configureNoPlatformStoresSession(sessionID, workspacePath string) {
	blocked := platformStoreBlockedPaths(workspacePath)
	if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
		blocked = append(blocked, cfg.BlockedPaths...)
	}
	common.SetSessionFolderGuardBlockedPaths(sessionID, common.DeduplicateStrings(blocked))
	common.ReplaceSessionShellEnvPrefix(sessionID, "WORKFLOW_KB_", nil)
	// Empty DB_PATH also replaces a grant inherited from a restored parent session.
	common.SetSessionShellEnv(sessionID, map[string]string{"DB_PATH": "", workflowDBAccessEnv: DBAccessNone, "WORKFLOW_KB_ACCESS": KBAccessNone})
}

func withoutPlatformStoreTools(tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	blocked := map[string]bool{"browse_knowledgebase": true, "read_knowledgebase": true, "update_knowledgebase": true, "backup_knowledgebase": true, "manage_knowledgebase_access": true, "query_workflow_db": true, "mutate_workflow_db": true, "apply_workflow_db_migration": true, "create_workflow_database_snapshot": true, "get_goal_metrics": true, "record_goal_observations": true}
	filtered := make([]llmtypes.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Function == nil || !blocked[tool.Function.Name] {
			filtered = append(filtered, tool)
		}
	}
	for name := range blocked {
		delete(executors, name)
	}
	return filtered, executors
}

func (hcpo *StepBasedWorkflowOrchestrator) executionStoreBlockedPaths() []string {
	if hcpo.platformStoresEnabled() {
		return nil
	}
	return platformStoreBlockedPaths(hcpo.GetWorkspacePath())
}
