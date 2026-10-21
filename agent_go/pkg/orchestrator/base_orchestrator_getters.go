package orchestrator

import (
	"fmt"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GetLogger returns the orchestrator's logger
func (bo *BaseOrchestrator) GetLogger() loggerv2.Logger {
	return bo.logger
}

// GetStartTime returns the start time
func (bo *BaseOrchestrator) GetStartTime() time.Time {
	return bo.startTime
}

// GetOrchestratorType returns the orchestrator type
func (bo *BaseOrchestrator) GetOrchestratorType() OrchestratorType {
	return bo.orchestratorType
}

// Workflow-specific methods (only available for workflow orchestrators)
// GetObjective returns the current objective
func (bo *BaseOrchestrator) GetObjective() string {
	return bo.objective
}

// SetObjective sets the objective
func (bo *BaseOrchestrator) SetObjective(objective string) {
	bo.objective = objective
}

// GetWorkspacePath returns the current workspace path
func (bo *BaseOrchestrator) GetWorkspacePath() string {
	return bo.workspacePath
}

// SetWorkspacePath sets the workspace path
func (bo *BaseOrchestrator) SetWorkspacePath(workspacePath string) {
	bo.workspacePath = workspacePath
}

// SetCLISecurityPolicy stores an immutable policy snapshot which is copied into
// every coding-agent configuration created by this orchestrator.
func (bo *BaseOrchestrator) SetCLISecurityPolicy(policy *llmtypes.CLISecurityPolicy) {
	if policy == nil {
		bo.cliSecurityPolicy = nil
		return
	}
	copyPolicy := policy.Clone()
	bo.cliSecurityPolicy = &copyPolicy
}

func (bo *BaseOrchestrator) GetCLISecurityPolicy() *llmtypes.CLISecurityPolicy {
	if bo.cliSecurityPolicy == nil {
		return nil
	}
	copyPolicy := bo.cliSecurityPolicy.Clone()
	return &copyPolicy
}

// SetWorkspacePathForFolderGuard sets separate read and write paths for folder guard validation
// If both arrays are empty, folder guard validation is disabled (allows all paths)
func (bo *BaseOrchestrator) SetWorkspacePathForFolderGuard(readPaths []string, writePaths []string) {
	if len(readPaths) == 0 && len(writePaths) == 0 {
		// Empty arrays disable folder guard
		bo.folderGuardReadPaths = nil
		bo.folderGuardWritePaths = nil
		bo.GetLogger().Info("🔓 Folder guard disabled (empty read/write paths)")
	} else {
		bo.folderGuardReadPaths = readPaths
		bo.folderGuardWritePaths = writePaths
		bo.GetLogger().Info(fmt.Sprintf("🔒 Folder guard enabled - Read paths: %v, Write paths: %v", readPaths, writePaths))
	}
}

// GetFolderGuardPaths returns the current folder guard read and write paths
func (bo *BaseOrchestrator) GetFolderGuardPaths() (readPaths []string, writePaths []string) {
	return bo.folderGuardReadPaths, bo.folderGuardWritePaths
}

// GetContextAwareBridge returns the context-aware event bridge
func (bo *BaseOrchestrator) GetContextAwareBridge() mcpagent.AgentEventListener {
	return bo.contextAwareBridge
}

// GetMCPConfigPath returns the MCP configuration path
func (bo *BaseOrchestrator) GetMCPConfigPath() string {
	return bo.mcpConfigPath
}

// GetTemperature returns the temperature setting
func (bo *BaseOrchestrator) GetTemperature() float64 {
	return bo.temperature
}

// GetAgentMode returns the agent mode
func (bo *BaseOrchestrator) GetAgentMode() string {
	return bo.agentMode
}

// GetSelectedServers returns the selected servers
func (bo *BaseOrchestrator) GetSelectedServers() []string {
	return bo.selectedServers
}

// GetSelectedTools returns the selected tools
func (bo *BaseOrchestrator) GetSelectedTools() []string {
	return bo.selectedTools
}

// GetSelectedSkills returns the selected skills (skill folder names)
func (bo *BaseOrchestrator) GetSelectedSkills() []string {
	return bo.selectedSkills
}

// SetSelectedSkills sets the selected skills (skill folder names)
func (bo *BaseOrchestrator) SetSelectedSkills(skills []string) {
	bo.selectedSkills = skills
	bo.GetLogger().Info(fmt.Sprintf("🎯 Set selected skills: %v", skills))
}

// GetSecrets returns the decrypted secrets
func (bo *BaseOrchestrator) GetSecrets() []SecretEntry {
	return bo.secrets
}

// SetSecrets sets the decrypted secrets to inject into agents
func (bo *BaseOrchestrator) SetSecrets(secrets []SecretEntry) {
	oldSecrets := bo.secrets
	bo.secrets = secrets
	if bo.workspaceEnvRef != nil {
		newSecretSet := make(map[string]bool, len(secrets))
		bo.workspaceEnvMu.Lock()
		for _, s := range secrets {
			envKey := "SECRET_" + s.Name
			bo.workspaceEnvRef[envKey] = s.Value
			newSecretSet[envKey] = true
		}
		for _, s := range oldSecrets {
			envKey := "SECRET_" + s.Name
			if !newSecretSet[envKey] {
				delete(bo.workspaceEnvRef, envKey)
			}
		}
		bo.workspaceEnvMu.Unlock()
		bo.GetLogger().Info(fmt.Sprintf("🔐 Synced %d secrets into workspace env ref", len(secrets)))
	}
	bo.GetLogger().Info(fmt.Sprintf("🔐 Set %d secrets for agent injection", len(secrets)))
}

// GetUseCodeExecutionMode returns the code execution mode setting
func (bo *BaseOrchestrator) GetUseCodeExecutionMode() bool {
	return bo.useCodeExecutionMode
}

// SetUseCodeExecutionMode updates the code execution mode setting
func (bo *BaseOrchestrator) SetUseCodeExecutionMode(enabled bool) {
	bo.useCodeExecutionMode = enabled
}

// SetSelectedServers updates the selected MCP servers list
func (bo *BaseOrchestrator) SetSelectedServers(servers []string) {
	bo.selectedServers = servers
}

// SetSelectedTools updates the selected tools list
func (bo *BaseOrchestrator) SetSelectedTools(tools []string) {
	bo.selectedTools = tools
}

// GetLLMConfig returns the LLM configuration
func (bo *BaseOrchestrator) GetLLMConfig() *LLMConfig {
	return bo.llmConfig
}

// GetAPIKeys safely returns the API keys from the LLM configuration
// Returns nil if llmConfig is nil (prevents nil pointer dereference)
func (bo *BaseOrchestrator) GetAPIKeys() *APIKeys {
	if bo.llmConfig == nil {
		return nil
	}
	return bo.llmConfig.APIKeys
}

// Returns nil if llmConfig is nil (prevents nil pointer dereference)

// GetTracer returns the tracer (not implemented - orchestrator doesn't have its own tracer)
func (bo *BaseOrchestrator) GetTracer() observability.Tracer {
	// Orchestrators don't have their own tracer - they coordinate agents that have tracers
	return nil
}

// GetMaxTurns returns the maximum turns for the orchestrator
func (bo *BaseOrchestrator) GetMaxTurns() int {
	return bo.maxTurns
}

// GetType returns the orchestrator type
func (bo *BaseOrchestrator) GetType() string {
	return string(bo.orchestratorType)
}

// SetIterationFolder sets the iteration folder and automatically applies it to the context-aware bridge
// This ensures all agents created by this orchestrator automatically get the iteration folder for token persistence
func (bo *BaseOrchestrator) SetIterationFolder(iterationFolder string) {
	bo.iterationFolder = iterationFolder
	bo.applyIterationFolderToBridge()
	bo.GetLogger().Info(fmt.Sprintf("📁 Set iteration folder for token persistence: %s (applied to all agents)", iterationFolder))
}

// GetIterationFolder returns the current iteration folder
func (bo *BaseOrchestrator) GetIterationFolder() string {
	return bo.iterationFolder
}

// GetBrowserDownloadsPath returns the relative path for browser downloads (e.g., "runs/.../execution/Downloads")
func (bo *BaseOrchestrator) GetBrowserDownloadsPath() string {
	return bo.browserDownloadsPath
}

// SetBrowserDownloadsPath sets the relative path for browser downloads
func (bo *BaseOrchestrator) SetBrowserDownloadsPath(path string) {
	bo.browserDownloadsPath = path
}
