package server

import (
	"context"
	"fmt"
	"strings"

	workshop "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// resolveWorkshopMCPServer reads durable selection on each bridge call. A retained
// CLI can outlive both the Agent instance and the catalog used to start its turn.
// Global discovery is metadata, not this chat's authorization boundary.
func (api *StreamingAPI) resolveWorkshopMCPServer(ctx context.Context, sessionID, server, tool string) (*executor.ResolvedMCPServer, error) {
	if resolved, isReportRun, err := api.resolveReportRunMCPServer(ctx, sessionID, server, tool); isReportRun {
		return resolved, err
	}
	cached, ok := api.workshopChatSessions.Load(sessionID)
	if !ok {
		return nil, nil
	} // Ordinary chats and step agents keep their own scope.
	session, ok := cached.(interface {
		GetConfig() *workshop.WorkshopConfig
	})
	if !ok {
		return nil, fmt.Errorf("MCP workshop scope is unavailable")
	}
	cfg := session.GetConfig()
	if cfg == nil || cfg.WorkspacePath == "" {
		return nil, fmt.Errorf("MCP scope unavailable for this workshop")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, cfg.WorkspacePath)
	if err != nil {
		return nil, fmt.Errorf("read current workflow MCP scope: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("workflow MCP scope is missing")
	}
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil, fmt.Errorf("load current MCP configuration: %w", err)
	}
	userID := ""
	if api.eventStore != nil {
		userID = api.eventStore.GetSessionOwner(sessionID)
	}
	if userID == "" {
		return nil, fmt.Errorf("MCP session owner is unavailable")
	}
	return resolveSelectedMCPServer(catalog, runtimeMCPServers(manifest.Capabilities.SelectedServers), manifest.Capabilities.SelectedTools, userID, server, tool)
}

func resolveSelectedMCPServer(catalog *mcpclient.MCPConfig, selected, selectedTools []string, userID, server, tool string) (*executor.ResolvedMCPServer, error) {
	canonical, config, err := catalog.ResolveServer(server)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, name := range selected {
		if name == mcpclient.NoServers {
			continue
		}
		resolved, _, resolveErr := catalog.ResolveServer(name)
		if resolveErr == nil && resolved == canonical {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("Server %q is not available in this session's scope. Selected servers: %v", canonical, selected)
	}
	if len(selectedTools) > 0 {
		toolAllowed := false
		for _, entry := range selectedTools {
			name, pattern, ok := strings.Cut(entry, ":")
			if !ok {
				continue
			}
			resolved, _, resolveErr := catalog.ResolveServer(name)
			if resolveErr == nil && resolved == canonical && (pattern == "*" || pattern == tool) {
				toolAllowed = true
				break
			}
		}
		if !toolAllowed {
			return nil, fmt.Errorf("Tool %q is not selected for MCP server %q", tool, canonical)
		}
	}
	// MCP credentials are shared platform connections. Existing installations
	// retain their persisted token path; new connections use _platform.
	if config.OAuth != nil && strings.TrimSpace(config.OAuth.TokenFile) == "" {
		oauth := *config.OAuth
		oauth.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, canonical)
		config.OAuth = &oauth
	}
	return &executor.ResolvedMCPServer{Name: canonical, Config: config, ConnectionSessionID: platformMCPConnectionSessionID}, nil
}
