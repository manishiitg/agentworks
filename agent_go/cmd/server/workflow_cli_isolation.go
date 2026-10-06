package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// Workflow chat isolation is the default for coding-agent providers. Keep an
// explicit Builder rollback while deployments transition; Run must always
// isolate so a writable CLI cwd cannot promote read-only workflow access.
// Workflow manifests cannot enable or disable this server-owned boundary.
func workflowCLIIsolationEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI")), "false")
}

func workflowCLIIsolationEnabledForMode(mode string) bool {
	return workflowCLIIsolationEnabled() || strings.EqualFold(strings.TrimSpace(mode), "run")
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
	if !workflowCLIIsolationEnabledForMode(mode) {
		return codingAgentWorkspaceWorkingDir(folder), nil
	}
	return linkedProjectCLIWorkingDir(folder, user, session, provider, mode)
}

// Shared by workflow and Crew native builders; project skill leftovers are data,
// while this session's attached skill registry owns platform guidance.
const linkedProjectRuntimeSkillInstructions = "\nPlatform capability guidance: load current attached builder-reference and runtime-http-tools skills through the admitted read_skill/discovery tools or the runtime-provided skill paths. Do not search project/ for legacy generated .pi/.claude/.agents platform skill files; those can be stale or belong to another provider. Never guess a provider's skill folder. Inspect the current search_tools/get_api_spec catalog before saying a capability is unavailable. Vault administrators can manage encrypted shared secrets and copy project secrets with manage_global_secret(action=share); environment globals alone remain read-only. Current tool admission and backend authorization remain authoritative.\n"

func workflowCLIWorkspaceInstructions(folder string) string {
	return fmt.Sprintf("\nWorkflow CLI runtime: the current directory holds this chat's mode-specific instructions, skills and CLI configuration. `project/` links to the authoritative workflow at %q. Native file tools use `project/<path>`; use `cd project && ...` for commands that need workflow-relative paths. Durable outputs belong under that link. Search and glob tools do not look inside the link unless you name it: always pass `project` (or `project/<folder>`) as the search path, because a search from the current directory finds none of the project's files. Workspace bridge tools already resolve to the real workflow and must not include the `project/` prefix. Keep generated CLI instructions/configuration in the private runtime, preserve the workflow's own instructions, and obey current Run/Builder permissions through linked paths.\n", codingAgentWorkspaceWorkingDir(folder)) + linkedProjectRuntimeSkillInstructions
}

func workflowCLIResumeAllowed(agent *mcpagent.Agent, runtime *ChatHistoryAgentRuntime) bool {
	if runtime == nil {
		return true
	}
	current := mcpagent.SnapshotAgentSession(agent)
	if current == nil {
		// Preserve legacy non-isolated restoration for agents without a
		// configured provider handle. Private Crew agents are constructed with
		// their runtime cwd and always take the identity check below.
		return !workflowCLIIsolationEnabled()
	}
	// Apply this only to agents constructed with a private runtime directory.
	// Crew linked runtimes use the same identity check, even when workflow
	// isolation is disabled. Ordinary chats and step agents keep their policy.
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
