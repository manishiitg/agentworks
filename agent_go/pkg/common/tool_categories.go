package common

import "strings"

// builtinToolCategories is the canonical set of code-provided custom/virtual
// tool category names. Workflow manifests list these in selected_servers
// alongside real MCP servers, but they are always available (registered by the
// app, not loaded from the MCP config), so dependency/availability checks must
// exclude them. Mirrors the routing set in cmd/server (customToolCategories +
// virtualToolCategories) and the orchestrator category lists.
var builtinToolCategories = map[string]bool{
	"workspace":            true,
	"workspace_tools":      true,
	"workspace_advanced":   true,
	"workspace_browser":    true,
	"workspace_image":      true,
	"workspace_image_gen":  true,
	"workspace_image_edit": true,
	"human_tools":          true,
	"workflow":             true,
	"workflow_creator":     true,
	"knowledgebase_tools":  true,
	"llm_config_tools":     true,
	"secret_tools":         true,
	"skill_tools":          true,
	"mcp_server_tools":     true,
	"activity_status":      true,
	"auto_improvement":     true,
	"memory":               true,
}

// IsBuiltinToolCategory reports whether name refers to a built-in custom/virtual
// tool category (always available) rather than a configured MCP server. Tolerant
// of hyphen/underscore and case differences, matching MCP server-name lookup.
func IsBuiltinToolCategory(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
	return builtinToolCategories[n]
}
