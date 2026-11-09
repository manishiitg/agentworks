package orchestrator

import (
	"fmt"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/projectfunctions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ValidateCustomToolSelection checks saved selections against the same current
// definitions used for registration. It validates names, never grants access.
func ValidateCustomToolSelection(selection string) error {
	category, name, found := strings.Cut(selection, ":")
	if !found || category == "" || name == "" {
		return fmt.Errorf("custom tool %q must use category:tool or category:*", selection)
	}
	// Definitions are independent of deployment flags, logins and the current
	// workflow pool. Do not create runtime executors to check a saved name.
	names := map[string]bool{}
	var definitions []llmtypes.Tool
	switch category {
	case "workspace_advanced", "workspace_tools":
		definitions = append(virtualtools.CreateWorkspaceAdvancedTools(), virtualtools.CreateWorkspaceImageTools()...)
		if category == "workspace_tools" {
			definitions = append(definitions, virtualtools.CreateWorkspaceBrowserTools()...)
		}
	case "workspace_browser":
		definitions = virtualtools.CreateWorkspaceBrowserTools()
	case "workspace_image":
		definitions = virtualtools.CreateWorkspaceImageTools()
	case "human_tools":
		for _, tool := range virtualtools.HumanToolImplementationNames() {
			names[tool] = true
		}
	case "agent_messaging":
		// Workflow messaging admits the catalog's explicit send operation.
		for _, action := range catalog.Actions {
			if action.Family == "messages" && action.Name == "send" {
				names[action.Member] = true
			}
		}
	case virtualtools.WorkflowDBToolCategory:
		names = virtualtools.WorkflowDBToolNames()
	case "knowledgebase":
		for _, tool := range knowledgebase.ToolNames() {
			names[tool] = true
		}
	case virtualtools.GetSubAgentToolCategory():
		definitions = virtualtools.CreateSubAgentTools()
	case projectfunctions.ExecutionTools().Category:
		for _, tool := range projectfunctions.ExecutionTools().Tools {
			names[tool] = true
		}
	}
	for _, tool := range definitions {
		if tool.Function != nil {
			names[tool.Function.Name] = true
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("custom tool %q names a category absent from the current catalog; review and remove or replace this selection", selection)
	}
	if name == "*" || names[name] {
		return nil
	}
	for member := range names {
		if catalog.Identity(member) == name {
			return nil
		}
	}
	if name == "notify_user" {
		return fmt.Errorf("custom tool %q is retired: the run record is the summary and notify() tells the owner in the Crew chat; for named recipients use send_email or send_slack. Remove this named selection before upgrading", selection)
	}
	if name == "files.image" || name == "read_image" {
		return fmt.Errorf("custom tool %q is retired with no platform replacement: remove this named selection and use the coding agent's native image reader (for example Read or view_image). Review image-dependent instructions before upgrading", selection)
	}
	return fmt.Errorf("custom tool %q is absent from the current catalog; review and remove or replace this named selection before upgrading", selection)
}

// getToolNamesByCategory returns a set of tool names for a given category
// This uses the actual tool creation functions as the source of truth
func getToolNamesByCategory(category string) map[string]bool {
	toolNames := make(map[string]bool)

	switch category {
	case "workspace_tools", "workspace_advanced", "workspace_image":
		for toolName := range virtualtools.WorkspaceToolNamesByCategory(category) {
			toolNames[toolName] = true
		}
	case virtualtools.WorkflowDBToolCategory:
		for toolName := range virtualtools.WorkflowDBToolNames() {
			toolNames[toolName] = true
		}
	case "human_tools":
		for _, toolName := range virtualtools.HumanToolImplementationNames() {
			toolNames[toolName] = true
		}
	case "workspace_browser":
		// Browser automation tools (1 tool: agent_browser)
		executors := virtualtools.CreateWorkspaceBrowserToolExecutors()
		for toolName := range executors {
			toolNames[toolName] = true
		}
	}

	return toolNames
}

// AgentWorksChatHumanToolSelections projects the explicit product.yaml chat
// admission into the category-qualified format used by execution-agent tool
// filtering. Builder-owned phase/reviewer agents must use this instead of
// human_tools:* so adding a Go implementation cannot silently widen Builder.
func AgentWorksChatHumanToolSelections(mode string) []string {
	implementations := make(map[string]bool)
	for _, name := range virtualtools.HumanToolImplementationNames() {
		implementations[name] = true
	}
	var selections []string
	for _, name := range agentworksproduct.ChatTools(mode) {
		if implementations[name] {
			selections = append(selections, virtualtools.GetHumanToolCategory()+":"+name)
		}
	}
	return selections
}

// FilterCustomToolsByCategory filters custom tools and executors based on enabled tools
// Format: single array with entries like "category:tool" or "category:*"
//   - "workspace_tools:*" → the advanced workspace registry plus browser tools
//   - "workspace_tools:execute_shell_command" → specific tool
//   - "human_tools:*" → all tools from HumanToolImplementationNames()
//   - "human_tools:human_feedback" → specific blocking tool
//   - "human_tools:send_email" → email to named recipients
//
// Category identification uses the actual tool creation functions as the source of truth
// If enabledTools is empty, return all tools (backward compatible - default behavior)
func FilterCustomToolsByCategory(
	allTools []llmtypes.Tool,
	allExecutors map[string]interface{},
	enabledTools []string, // format: "category:tool" or "category:*"
) ([]llmtypes.Tool, map[string]interface{}) {
	// Build a set of enabled tool names
	enabledToolNames := make(map[string]bool)

	// Parse enabled tools array
	for _, entry := range enabledTools {
		// Format: "category:tool" or "category:*"
		// Use SplitN to handle tool names that might contain colons (split only on first colon)
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			// Invalid format, skip
			continue
		}

		category := parts[0]
		toolSpec := parts[1]

		if toolSpec == "*" {
			// Enable all tools from this category
			categoryToolNames := getToolNamesByCategory(category)
			for toolName := range categoryToolNames {
				enabledToolNames[toolName] = true
			}
		} else {
			// Enable specific tool
			enabledToolNames[toolSpec] = true
		}
	}

	// If nothing is specified, return all tools (backward compatible)
	if len(enabledTools) == 0 {
		return allTools, allExecutors
	}

	// Filter tools based on enabled tool names
	var filteredTools []llmtypes.Tool
	filteredExecutors := make(map[string]interface{})

	for _, tool := range allTools {
		toolName := tool.Function.Name

		// Check if tool is in the enabled set
		if enabledToolNames[toolName] || enabledToolNames[catalog.Identity(toolName)] {
			filteredTools = append(filteredTools, tool)
			// Include corresponding executor if it exists
			if executor, exists := allExecutors[toolName]; exists {
				filteredExecutors[toolName] = executor
			}
		}
	}

	return filteredTools, filteredExecutors
}

// PreparePhaseAgentTools returns a minimal tool set for phase agents (planning, debugging, etc.)
// Phase agents only need shell_command (for file operations) and the human
// tools explicitly admitted to Builder by AgentWorks product.yaml.
// They do NOT need the full workspace_advanced tool set.
func (bo *BaseOrchestrator) PreparePhaseAgentTools() ([]llmtypes.Tool, map[string]interface{}) {
	enabledTools := []string{"workspace_advanced:execute_shell_command"}
	enabledTools = append(enabledTools, AgentWorksChatHumanToolSelections("builder")...)
	return FilterCustomToolsByCategory(
		bo.WorkspaceTools,
		bo.WorkspaceToolExecutors,
		enabledTools,
	)
}
