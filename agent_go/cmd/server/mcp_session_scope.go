package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
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
	userID := ""
	if api.eventStore != nil {
		userID = api.eventStore.GetSessionOwner(sessionID)
	}
	if userID == "" {
		return nil, fmt.Errorf("MCP session owner is unavailable")
	}
	ctx = context.WithValue(ctx, common.UserIDKey, userID)
	// The workflow's own connections: only the ones this place has, never
	// anyone else's personal server.
	if isPlaceMCPInternalName(strings.TrimSpace(server)) {
		_, overrides := attachedMCPServersForRoot(ctx, cfg.WorkspacePath)
		if override, ok := overrides[strings.TrimSpace(server)]; ok && override.Server != nil {
			return &executor.ResolvedMCPServer{Name: strings.TrimSpace(server), Config: *override.Server, ConnectionSessionID: strings.TrimSpace(server)}, nil
		}
		return nil, errPlaceMCPUnavailable
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
	return api.resolveScopedGovernedMCP(ctx, catalog, runtimeMCPServers(manifest.Capabilities.SelectedServers), manifest.Capabilities.SelectedTools, userID, server, tool)
}
