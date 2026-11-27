package server

import (
	"context"
	"os"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

var mcpBridgeCustomToolCategories = map[string]bool{
	"workspace":            true,
	"workspace_tools":      true,
	"workspace_browser":    true,
	"workspace_advanced":   true,
	"workspace_image":      true,
	"workspace_image_gen":  true,
	"workspace_image_edit": true,
	"human_tools":          true,
	"workflow":             true,
	"workflow_creator":     true,
	"knowledgebase_tools":  true,
	"knowledgebase":        true,
	"llm_config_tools":     true,
	"secret_tools":         true,
	"notification_tools":   true,
	"skill_tools":          true,
	"mcp_server_tools":     true,
	"activity_status":      true,
	"auto_improvement":     true,
}

var mcpBridgeVirtualToolCategories = map[string]bool{}

func normalizeMCPBridgeCategory(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
}

func isMCPBridgeCustomToolCategory(name string) bool {
	return mcpBridgeCustomToolCategories[normalizeMCPBridgeCategory(name)]
}

func isMCPBridgeVirtualToolCategory(name string) bool {
	return mcpBridgeVirtualToolCategories[normalizeMCPBridgeCategory(name)]
}

// runtimeMCPServers removes legacy custom-tool categories from a workflow's
// selected-server list. Older workflow.json files stored category labels such
// as workspace_advanced alongside real MCP servers. Those labels are used when
// registering direct tools later in setup; they must never be handed to the MCP
// connection layer as server names.
func runtimeMCPServers(selected []string) []string {
	servers := make([]string, 0, len(selected))
	for _, name := range selected {
		if isMCPBridgeCustomToolCategory(name) || isMCPBridgeVirtualToolCategory(name) {
			continue
		}
		servers = append(servers, name)
	}
	if len(servers) == 0 {
		return []string{mcpclient.NoServers}
	}
	return servers
}

const envMCPServerAPIToken = "MCP_SERVER_API_TOKEN"

// envBridgeTokenSecret pins the session-token signing secret across restarts
// (so retained CLI sessions keep working). It is read once and removed from
// the environment; without it a random secret is made per start.
const envBridgeTokenSecret = "MCP_BRIDGE_TOKEN_SECRET"

func resolveBridgeTokenSecret() string {
	secret := strings.TrimSpace(os.Getenv(envBridgeTokenSecret))
	os.Unsetenv(envBridgeTokenSecret)
	if secret != "" {
		return secret
	}
	return common.NewBridgeTokenSecret()
}

func resolveServerAPIToken() string {
	if token := strings.TrimSpace(os.Getenv(envMCPServerAPIToken)); token != "" {
		return token
	}
	return executor.GenerateAPIToken()
}

func seedMCPBridgeCodeExecRegistry(logger loggerv2.Logger) {
	advancedExecutors := virtualtools.CreateWorkspaceAdvancedToolExecutors()
	browserExecutors := virtualtools.CreateWorkspaceBrowserToolExecutors()

	bridgeExecutors := make(map[string]func(context.Context, map[string]interface{}) (string, error), 3)
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file"} {
		if exec, ok := advancedExecutors[name]; ok {
			bridgeExecutors[name] = exec
		}
	}
	if exec, ok := browserExecutors["agent_browser"]; ok {
		bridgeExecutors["agent_browser"] = exec
	}

	codeexec.InitRegistryWithVirtualTools(nil, bridgeExecutors, nil, nil, logger)
}
