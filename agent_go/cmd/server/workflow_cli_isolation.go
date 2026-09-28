package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// Workflow chat isolation is the default for coding-agent providers. Keep an
// explicit environment rollback while deployments transition; workflow
// manifests cannot enable or disable this server-owned boundary.
func workflowCLIIsolationEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI")), "false")
}

func workflowCLIMode(req *QueryRequest, readOnly bool) string {
	if readOnly {
		return "run"
	}
	if req != nil && req.ExecutionOptions != nil {
		if mode := normalizeChatHistoryWorkshopMode(req.ExecutionOptions.WorkshopMode); mode != "" {
			return mode
		}
	}
	return "workshop"
}

// workflowCLIStateRoot resolves the server-owned runtime root. Launchers may
// pin it explicitly, but ordinary server and Desktop starts must still have a
// durable location after a restart. Keep this outside workspace-docs: the
// private projection contains generated CLI instructions and must never become
// workflow data or be visible through workspace tools.
func workflowCLIStateRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("AGENTWORKS_STATE_ROOT must be an absolute path")
		}
		return filepath.Clean(configured), nil
	}
	return defaultWorkflowCLIStateRoot()
}

// defaultWorkflowCLIStateRoot is the root used when no launcher pins
// AGENTWORKS_STATE_ROOT. Deployments that start pinning it carry state from
// here once (carryOverLegacyStateRoot) instead of silently starting empty.
func defaultWorkflowCLIStateRoot() (string, error) {
	if userData := strings.TrimSpace(os.Getenv("RUNLOOP_USER_DATA_DIR")); userData != "" {
		if !filepath.IsAbs(userData) {
			return "", fmt.Errorf("RUNLOOP_USER_DATA_DIR must be an absolute path")
		}
		return filepath.Join(filepath.Clean(userData), "state"), nil
	}
	configRoot, err := os.UserConfigDir()
	if err != nil || !filepath.IsAbs(configRoot) {
		return "", fmt.Errorf("cannot resolve a durable AgentWorks state directory")
	}
	return filepath.Join(configRoot, "AgentWorks", "state"), nil
}

func workflowCLIWorkingDir(folder, user, session, provider, mode string) (string, error) {
	shared := codingAgentWorkspaceWorkingDir(folder)
	if !workflowCLIIsolationEnabled() || !isCodingAgentProvider(provider, "") {
		return shared, nil
	}
	stateRoot, err := workflowCLIStateRoot()
	if err != nil {
		return "", fmt.Errorf("cannot isolate workflow CLI session: %w", err)
	}
	dir, err := cliruntime.Prepare(stateRoot, fsutil.WorkspaceDocsRoot(), user, shared, session, provider, mode)
	if err != nil {
		return "", fmt.Errorf("cannot isolate workflow CLI session: %w", err)
	}
	return dir, nil
}

func workflowCLIWorkspaceInstructions(folder string) string {
	return fmt.Sprintf("\nCLI runtime location: the current directory contains this chat's private instructions and skills. The authoritative workflow root is %q. Use absolute paths rooted there for native file tools, and explicitly cd there for native shell commands that use workflow-relative paths. Workspace bridge tools already resolve to that workflow. Keep generated CLI instructions and skills in the private runtime directory.\n", codingAgentWorkspaceWorkingDir(folder))
}

func workflowCLIResumeAllowed(agent *mcpagent.Agent, runtime *ChatHistoryAgentRuntime) bool {
	if !workflowCLIIsolationEnabled() || runtime == nil {
		return true
	}
	current := mcpagent.SnapshotAgentSession(agent)
	if current == nil {
		return false
	}
	// Apply this only to agents constructed with a private runtime directory.
	// Ordinary chats and existing isolated step agents keep their own policy.
	if !strings.Contains(current.Provider.WorkingDir, string(os.PathSeparator)+"cli-runtimes"+string(os.PathSeparator)+"v1"+string(os.PathSeparator)) {
		return true
	}
	if runtime.AgentSessionHandle == nil {
		return false
	}
	// Older Muse completion handles omitted cwd. Recover only from the native
	// session's metadata, and still enforce the private-directory identity.
	// Never infer the missing directory from the current chat alone.
	if strings.EqualFold(current.Provider.Provider, "muse-cli") &&
		strings.EqualFold(runtime.Provider, "muse-cli") &&
		strings.EqualFold(runtime.AgentSessionHandle.Provider.Provider, "muse-cli") &&
		strings.TrimSpace(runtime.AgentSessionHandle.Provider.WorkingDir) == "" {
		id := firstNonEmptyTrimmed(runtime.AgentSessionHandle.Provider.NativeSessionID, runtime.ExternalSessionID)
		if runtime.ExternalSessionID != "" && runtime.AgentSessionHandle.Provider.NativeSessionID != "" && runtime.ExternalSessionID != runtime.AgentSessionHandle.Provider.NativeSessionID {
			return false
		}
		saved := musecli.NativeSessionWorkingDir(id)
		if !cliruntime.CanResume(current.Provider.WorkingDir, saved) {
			return false
		}
		copyHandle := *runtime.AgentSessionHandle
		copyHandle.Provider.WorkingDir = saved
		runtime.AgentSessionHandle = &copyHandle
	}
	if !cliruntime.CanResume(current.Provider.WorkingDir, runtime.AgentSessionHandle.Provider.WorkingDir) {
		return false
	}
	// Codex's project directory also selects the process cwd and can override
	// WorkingDir. Validate both persisted representations before applying either.
	if strings.EqualFold(current.Provider.Provider, "codex-cli") {
		for _, projectDir := range []string{runtime.ProjectDirID, runtime.AgentSessionHandle.Provider.ProjectDirID} {
			if projectDir != "" && !cliruntime.CanResume(current.Provider.WorkingDir, projectDir) {
				return false
			}
		}
	}
	return true
}
