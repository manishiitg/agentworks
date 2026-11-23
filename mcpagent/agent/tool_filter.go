package mcpagent

import (
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// ToolFilter centralizes all tool filtering logic to ensure consistency
// between LLM tool registration (agent.go) and discovery (code_execution_tools.go)
type ToolFilter struct {
	selectedTools        []string        // "server:tool" or "package:tool" format
	selectedServers      []string        // server names for "all tools" mode
	customToolCategories map[string]bool // known custom tool categories (e.g., "workspace", "human_tools")
	mcpServerNames       map[string]bool // known MCP server names from Clients
	logger               loggerv2.Logger

	// Pre-computed lookup maps for efficient filtering
	normalizedToolSet        map[string]bool // normalized "package:tool" -> true
	serversWithAllTools      map[string]bool // servers with "server:*" pattern
	serversWithSpecificTools map[string]bool // servers with specific tools selected

	// System custom tool categories that should be included by default (like virtual tools)
	// These are workspace_tools and human_tools which are system tools, not MCP tools
	systemCategories map[string]bool
}

// NewToolFilter creates a new tool filter with the given configuration
// Parameters:
//   - selectedTools: list of "server:tool" or "package:*" patterns
//   - selectedServers: list of server names (for "all tools" mode)
//   - clients: MCP client map to identify MCP servers
//   - customCategories: list of custom tool category names (e.g., "workspace", "human_tools")
//   - logger: for debug logging
func NewToolFilter(
	selectedTools []string,
	selectedServers []string,
	clients map[string]mcpclient.ClientInterface,
	customCategories []string,
	logger loggerv2.Logger,
) *ToolFilter {
	// Use logger directly (already v2.Logger)
	if logger == nil {
		logger = loggerv2.NewNoop()
	}

	tf := &ToolFilter{
		selectedTools:            selectedTools,
		selectedServers:          selectedServers,
		customToolCategories:     make(map[string]bool),
		mcpServerNames:           make(map[string]bool),
		logger:                   logger,
		normalizedToolSet:        make(map[string]bool),
		serversWithAllTools:      make(map[string]bool),
		serversWithSpecificTools: make(map[string]bool),
		systemCategories:         make(map[string]bool),
	}

	// Initialize system categories that should always be included (like virtual tools)
	// These are workspace and app-provided categories - system tools that should be available
	// regardless of MCP tool filtering, unless explicitly excluded
	// Note: workspace has sub-categories (workspace_basic, workspace_browser, workspace_advanced, workspace_git)
	systemCats := []string{
		"workspace",            // legacy/generic
		"workspace_basic",      // read_workspace_file, update_workspace_file, etc.
		"workspace_browser",    // agent_browser
		"workspace_advanced",   // execute_shell_command
		"workspace_git",        // git tools
		"workspace_image_gen",  // AI image generation tool (workspace_image_gen)
		"workspace_image_edit", // AI image editing tool (workspace_image_edit)
		"human_tools",          // human_feedback, notify_user, etc.
		"delegation_tools",     // delegate, send_message_to_agent, etc.
		"workflow",             // plan modification tools (update_regular_step, add_regular_step, etc.)
		"skill_tools",          // read_skill (intrinsic attached-skill identity)
	}
	for _, cat := range systemCats {
		tf.systemCategories[cat] = true
		if !strings.HasSuffix(cat, "_tools") {
			tf.systemCategories[cat+"_tools"] = true
		}
	}

	// Build custom category lookup
	for _, cat := range customCategories {
		tf.customToolCategories[cat] = true
		// Also add the _tools suffix version for directory matching
		if !strings.HasSuffix(cat, "_tools") {
			tf.customToolCategories[cat+"_tools"] = true
		}
	}

	// Build MCP server name lookup (normalized)
	for serverName := range clients {
		normalized := tf.NormalizeServerName(serverName)
		tf.mcpServerNames[normalized] = true
		tf.mcpServerNames[serverName] = true // Keep original too
	}

	// Pre-compute lookup maps from selectedTools
	for _, fullName := range selectedTools {
		parts := strings.SplitN(fullName, ":", 2)
		if len(parts) == 2 {
			serverOrPkg := parts[0]
			toolName := parts[1]
			normalizedServer := tf.NormalizeServerName(serverOrPkg)

			if toolName == "*" {
				// "server:*" means all tools from this server/package
				tf.serversWithAllTools[normalizedServer] = true
				tf.serversWithAllTools[serverOrPkg] = true
			} else {
				// Specific tool selected
				tf.serversWithSpecificTools[normalizedServer] = true
				tf.serversWithSpecificTools[serverOrPkg] = true

				// Store normalized full name for exact lookup
				normalizedFull := normalizedServer + ":" + tf.NormalizeToolName(toolName)
				tf.normalizedToolSet[normalizedFull] = true
				// Also store original format
				tf.normalizedToolSet[fullName] = true
			}
		}
	}

	tf.logger.Debug("Created tool filter",
		loggerv2.Any("selected_tools", selectedTools),
		loggerv2.Any("selected_servers", selectedServers),
		loggerv2.Any("mcp_servers", tf.mcpServerNames),
		loggerv2.Any("custom_categories", tf.customToolCategories),
		loggerv2.Any("servers_with_all_tools", tf.serversWithAllTools),
		loggerv2.Any("servers_with_specific_tools", tf.serversWithSpecificTools),
		loggerv2.Any("normalized_tool_set", tf.normalizedToolSet))

	// Additional debug: Log detailed breakdown of wildcard patterns
	if len(tf.serversWithAllTools) > 0 {
		tf.logger.Debug("Wildcard patterns detected",
			loggerv2.Any("servers_with_all_tools_keys", getMapKeys(tf.serversWithAllTools)))
	}
	if len(tf.serversWithSpecificTools) > 0 {
		tf.logger.Debug("Specific tool patterns detected",
			loggerv2.Any("servers_with_specific_tools_keys", getMapKeys(tf.serversWithSpecificTools)))
	}

	return tf
}

// NormalizeServerName normalizes server/package names for comparison
// Handles hyphen vs underscore differences (e.g., "google-sheets" vs "google_sheets")
func (tf *ToolFilter) NormalizeServerName(name string) string {
	// Replace hyphens with underscores and lowercase
	return strings.ToLower(strings.ReplaceAll(name, "-", "_"))
}

// NormalizeToolName converts tool names to a consistent format for comparison
// Handles: snake_case, PascalCase, kebab-case
// All converted to lowercase snake_case
func (tf *ToolFilter) NormalizeToolName(name string) string {
	// First normalize: replace hyphens with underscores
	normalized := strings.ReplaceAll(name, "-", "_")

	// If already contains underscores, just lowercase
	if strings.Contains(normalized, "_") {
		return strings.ToLower(normalized)
	}

	// Convert PascalCase/camelCase to snake_case
	var result strings.Builder
	for i, r := range normalized {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// IsNoFilteringActive returns true if no filtering is configured
// (both selectedTools and selectedServers are empty)
func (tf *ToolFilter) IsNoFilteringActive() bool {
	return len(tf.selectedTools) == 0 && len(tf.selectedServers) == 0
}

// IsCategoryDirectory checks if a directory name represents a custom tool category
// Uses explicit category list instead of "not in Clients" check
// 🔧 FIX: Handles both base names (e.g., "workspace") and _tools-suffixed names (e.g., "workspace_tools")
func (tf *ToolFilter) IsCategoryDirectory(dirName string) bool {
	// Check against known custom categories
	normalized := tf.NormalizeServerName(dirName)

	// 🔧 FIX: Check system categories first (workspace, human) - these are always available
	// Previously, system categories like "workspace" were only in systemCategories map,
	// but IsCategoryDirectory() only checked customToolCategories. This caused "workspace"
	// to be incorrectly filtered as an MCP server, leading to "server workspace is filtered out" errors.
	// Now we check systemCategories first to ensure workspace/human tools are always recognized as categories.
	if tf.systemCategories[normalized] {
		return true
	}

	// Direct category match (e.g., "workspace", "human_tools")
	if tf.customToolCategories[normalized] {
		return true
	}

	// 🔧 FIX: If dirName has _tools suffix, strip it and check base name
	// This handles cases where package names like "workspace_tools" are passed
	// but we need to check if "workspace" is a category
	if strings.HasSuffix(dirName, "_tools") {
		baseName := strings.TrimSuffix(dirName, "_tools")
		baseNormalized := tf.NormalizeServerName(baseName)
		if tf.systemCategories[baseNormalized] || tf.customToolCategories[baseNormalized] {
			return true
		}
	}

	// If it's NOT a known MCP server, treat as category
	// This handles dynamically registered custom tool categories
	if !tf.mcpServerNames[normalized] {
		// 🔧 FIX: Check if it's a known system category (added for fallback matching)
		// This ensures system categories are found even if not in the normalized lookup
		for category := range tf.systemCategories {
			if tf.NormalizeServerName(category) == normalized {
				return true
			}
		}
		// Check if it's a known custom category (case-insensitive)
		for category := range tf.customToolCategories {
			if tf.NormalizeServerName(category) == normalized {
				return true
			}
		}
	}

	return false
}

// IsVirtualToolsDirectory checks if a directory is the virtual_tools directory
func (tf *ToolFilter) IsVirtualToolsDirectory(dirName string) bool {
	return dirName == "virtual_tools"
}

// ShouldIncludeTool checks if a tool should be included based on filtering configuration
// This is the main filtering method used by both agent.go and code_execution_tools.go
//
// Parameters:
//   - packageOrServer: the server name (for MCP tools) or package name (for custom tools)
//   - toolName: the tool/function name
//   - isCustomTool: true if this is a custom tool (workspace, human_tools, etc.), false for MCP tools
//   - isVirtualTool: true if this is a virtual tool (get_api_spec, etc.)
//
// Returns true if the tool should be included
func (tf *ToolFilter) ShouldIncludeTool(packageOrServer string, toolName string, isCustomTool bool, isVirtualTool bool) bool {
	// Virtual tools are ALWAYS included (system tools)
	if isVirtualTool {
		tf.logger.Debug("Tool included (virtual tool, always included)",
			loggerv2.String("package", packageOrServer),
			loggerv2.String("tool", toolName))
		return true
	}

	// If no filtering is active, include all tools
	if tf.IsNoFilteringActive() {
		tf.logger.Debug("Tool included (no filtering active)",
			loggerv2.String("package", packageOrServer),
			loggerv2.String("tool", toolName))
		return true
	}

	// Check if this is a system category (workspace_tools, human_tools)
	// System categories are included by default unless they have specific tools selected
	// (in which case only those specific tools are included)
	normalizedPkgForSystem := tf.NormalizeServerName(packageOrServer)
	if tf.systemCategories[normalizedPkgForSystem] || tf.systemCategories[packageOrServer] {
		// Check if this system category has specific tools selected
		// If not, include ALL tools from this category by default
		if !tf.serversWithSpecificTools[normalizedPkgForSystem] && !tf.serversWithSpecificTools[packageOrServer] {
			tf.logger.Debug("Tool included (system category, included by default)",
				loggerv2.String("package", packageOrServer),
				loggerv2.String("tool", toolName))
			return true
		}
		// System category has specific tools selected - fall through to check those
	}

	// Normalize names for comparison
	normalizedPkg := tf.NormalizeServerName(packageOrServer)
	normalizedTool := tf.NormalizeToolName(toolName)

	// Debug: Log filter state for troubleshooting
	tf.logger.Debug("Tool filter check",
		loggerv2.String("package", packageOrServer),
		loggerv2.String("normalized_package", normalizedPkg),
		loggerv2.String("tool", toolName),
		loggerv2.String("normalized_tool", normalizedTool),
		loggerv2.Any("is_custom_tool", isCustomTool),
		loggerv2.Any("is_virtual_tool", isVirtualTool),
		loggerv2.Any("selected_tools", tf.selectedTools),
		loggerv2.Any("selected_servers", tf.selectedServers),
		loggerv2.Any("servers_with_all_tools", tf.serversWithAllTools),
		loggerv2.Any("servers_with_specific_tools", tf.serversWithSpecificTools))

	// Check if this package/server has "all tools" pattern (package:*)
	hasAllToolsNormalized := tf.serversWithAllTools[normalizedPkg]
	hasAllToolsOriginal := tf.serversWithAllTools[packageOrServer]
	if hasAllToolsNormalized || hasAllToolsOriginal {
		tf.logger.Debug("Tool included (package has '*' pattern)",
			loggerv2.String("package", packageOrServer),
			loggerv2.String("normalized_package", normalizedPkg),
			loggerv2.String("tool", toolName),
			loggerv2.Any("match_normalized", hasAllToolsNormalized),
			loggerv2.Any("match_original", hasAllToolsOriginal),
			loggerv2.Any("servers_with_all_tools", tf.serversWithAllTools))
		return true
	}

	// PRIORITY: Check if this package/server has specific tools selected FIRST
	// If specific tools are selected, they take precedence over selectedServers
	// This allows: selectedTools=[gmail:read_email] to only include that tool,
	// even if selectedServers=[gmail] (which would otherwise include all gmail tools)
	if tf.serversWithSpecificTools[normalizedPkg] || tf.serversWithSpecificTools[packageOrServer] {
		// Check if this exact tool is in the selection
		normalizedFull := normalizedPkg + ":" + normalizedTool
		if tf.normalizedToolSet[normalizedFull] {
			tf.logger.Debug("Tool included (specific tool selected, normalized)",
				loggerv2.String("package", packageOrServer),
				loggerv2.String("tool", toolName),
				loggerv2.String("normalized", normalizedFull))
			return true
		}

		// Also check original format
		originalFull := packageOrServer + ":" + toolName
		if tf.normalizedToolSet[originalFull] {
			tf.logger.Debug("Tool included (specific tool selected, original)",
				loggerv2.String("package", packageOrServer),
				loggerv2.String("tool", toolName),
				loggerv2.String("original", originalFull))
			return true
		}

		// Package has specific tools but this one isn't selected
		tf.logger.Debug("Tool excluded (package has specific tools but this one not selected)",
			loggerv2.String("package", packageOrServer),
			loggerv2.String("tool", toolName))
		return false
	}

	// Package/server has no specific tools in selectedTools
	// Now check if server is in selectedServers (which means include ALL tools from this server)
	if len(tf.selectedServers) > 0 {
		for _, selectedServer := range tf.selectedServers {
			normalizedSelected := tf.NormalizeServerName(selectedServer)
			matchesNormalized := normalizedSelected == normalizedPkg
			matchesOriginal := selectedServer == packageOrServer
			if matchesNormalized || matchesOriginal {
				// Server is in selectedServers and has no specific tools - include ALL tools from this server
				tf.logger.Debug("Tool included (server in selectedServers - includes ALL tools, no specific tools override)",
					loggerv2.String("package", packageOrServer),
					loggerv2.String("normalized_package", normalizedPkg),
					loggerv2.String("tool", toolName),
					loggerv2.String("selected_server", selectedServer),
					loggerv2.String("normalized_selected", normalizedSelected),
					loggerv2.Any("match_normalized", matchesNormalized),
					loggerv2.Any("match_original", matchesOriginal))
				return true
			}
		}
		// Server is not in selectedServers and has no specific tools - exclude
		// Server is not in selectedServers and has no specific tools - exclude

		// Not in selectedServers
		// For custom tools, also check if their category is in selectedTools
		if isCustomTool {
			// Custom tools might be filtered by category
			// Check if any tool from this category is selected (which would be in serversWithSpecificTools)
			// If the category isn't mentioned at all in selectedTools, exclude it
			tf.logger.Debug("Tool excluded (custom tool, category not in selectedServers or selectedTools)",
				loggerv2.String("package", packageOrServer),
				loggerv2.String("tool", toolName))
			return false
		}

		// MCP tool not in selectedServers
		tf.logger.Debug("Tool excluded (server not in selectedServers)",
			loggerv2.String("package", packageOrServer),
			loggerv2.String("tool", toolName))
		return false
	}

	// No selectedServers configured and no specific tools for this package
	// If selectedTools is set but this server isn't mentioned, EXCLUDE (strict filtering)
	// If selectedTools is empty AND selectedServers is empty, include all (no filtering)
	if len(tf.selectedTools) > 0 {
		// selectedTools is set but this package isn't in it at all
		tf.logger.Debug("Tool excluded (package not in selectedTools)",
			loggerv2.String("package", packageOrServer),
			loggerv2.String("tool", toolName))
		return false
	}

	// No selectedTools and no selectedServers - include all (backwards compatible)
	tf.logger.Debug("Tool included (default: no restrictions on this package)",
		loggerv2.String("package", packageOrServer),
		loggerv2.String("tool", toolName))
	return true
}

// ShouldIncludeServer checks if a server/package should be included at all
// Used for server-level filtering before checking individual tools
func (tf *ToolFilter) ShouldIncludeServer(serverName string) bool {
	// If no filtering is active, include all servers
	if tf.IsNoFilteringActive() {
		return true
	}

	normalizedServer := tf.NormalizeServerName(serverName)

	// Check if server has "all tools" pattern
	if tf.serversWithAllTools[normalizedServer] || tf.serversWithAllTools[serverName] {
		return true
	}

	// Check if server has specific tools selected
	if tf.serversWithSpecificTools[normalizedServer] || tf.serversWithSpecificTools[serverName] {
		return true
	}

	// Check if server is in selectedServers
	for _, selected := range tf.selectedServers {
		if tf.NormalizeServerName(selected) == normalizedServer || selected == serverName {
			return true
		}
	}

	// Server not mentioned in any filter
	// If selectedServers is set, exclude servers not in the list
	if len(tf.selectedServers) > 0 {
		return false
	}

	// No selectedServers, so include by default
	return true
}

// GetToolCategory returns the category for a custom tool based on its package name
// Returns empty string if not a custom tool category
func (tf *ToolFilter) GetToolCategory(packageName string) string {
	normalized := tf.NormalizeServerName(packageName)

	// Preserve generated aliases such as workspace_tools -> workspace, but keep
	// canonical categories that already end in _tools (human_tools and
	// delegation_tools) intact when no unsuffixed category was registered.
	category := strings.TrimSuffix(normalized, "_tools")
	if category != normalized && tf.customToolCategories[category] {
		return category
	}
	if tf.customToolCategories[normalized] {
		return normalized
	}

	return ""
}

// IsSystemCategory checks if a package/category is a system category
// System categories (workspace_tools, human_tools) are included by default
func (tf *ToolFilter) IsSystemCategory(packageName string) bool {
	normalized := tf.NormalizeServerName(packageName)
	return tf.systemCategories[normalized] || tf.systemCategories[packageName]
}

// AddCustomCategory registers a new custom tool category so that
// IsCategoryDirectory recognises it for get_api_spec lookups.
// Safe to call after NewToolFilter (e.g. from RegisterCustomTool).
func (tf *ToolFilter) AddCustomCategory(category string) {
	if category == "" {
		return
	}
	tf.customToolCategories[category] = true
	if !strings.HasSuffix(category, "_tools") {
		tf.customToolCategories[category+"_tools"] = true
	}
}

// getMapKeys is a helper function to extract keys from a map for logging
func getMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
