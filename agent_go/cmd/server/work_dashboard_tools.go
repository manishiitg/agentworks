package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// registerWorkDashboardTools exposes the same managed SQLite and live HTML
// report primitives AgentWorks uses, scoped to one Work project. Product words
// stay Dashboard/Database; the established workflow_* tool names remain stable
// so Work does not fork the storage or validation implementation.
func (api *StreamingAPI) registerWorkDashboardTools(
	llmAgent *agent.LLMAgentWrapper,
	profile *resolvedAgentProfile,
	sessionID, userID, workspacePath string,
) error {
	if profile == nil || !isProjectProfileID(profile.Definition.ID) ||
		(!agentprofiles.HasFeature(profile.Definition, "database") && !agentprofiles.HasFeature(profile.Definition, "dashboard")) {
		return nil
	}
	if !isActiveWorkProjectWorkspace(userID, workspacePath) {
		return nil
	}
	if llmAgent == nil || llmAgent.GetUnderlyingAgent() == nil {
		return fmt.Errorf("Work Dashboard tools require an active agent")
	}

	registry := virtualtools.CreateWorkflowDBToolRegistry(getWorkspaceAPIURL(), userID, sessionID)
	for _, definition := range registry.Tools {
		if definition.Function == nil {
			continue
		}
		name := definition.Function.Name
		executor := registry.Executors[name]
		if executor == nil {
			continue
		}
		var parameters map[string]interface{}
		if definition.Function.Parameters != nil {
			encoded, err := json.Marshal(definition.Function.Parameters)
			if err != nil {
				return fmt.Errorf("encode %s parameters: %w", name, err)
			}
			if err := json.Unmarshal(encoded, &parameters); err != nil {
				return fmt.Errorf("decode %s parameters: %w", name, err)
			}
		}
		if err := llmAgent.RegisterCustomTool(name, definition.Function.Description, parameters, executor, registry.Categories[name]); err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
	}

	publicWorkspacePath := canonicalChatHistoryWorkspacePath(userID, workspacePath)
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
	readFile := func(ctx context.Context, path string) (string, error) {
		result, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: path})
		if err != nil {
			return "", err
		}
		return result.Content, nil
	}
	dbPath := filepath.ToSlash(filepath.Join(publicWorkspacePath, "db", "db.sqlite"))
	hooks := todo_creation_human.ReportHTMLValidationHooks{
		ExplainSQL: func(ctx context.Context, sqlText string) error {
			_, err := client.QueryAuthorizedWorkflowDB(ctx, workspace.QueryWorkflowDBParams{DBPath: dbPath, SQL: "EXPLAIN " + sqlText, PrepareOnly: true})
			return err
		},
		FileExists: func(ctx context.Context, relativePath string) (bool, error) {
			_, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: filepath.ToSlash(filepath.Join(publicWorkspacePath, relativePath))})
			if err == nil {
				return true, nil
			}
			if strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(err.Error(), "404") {
				return false, nil
			}
			return false, err
		},
	}
	if err := todo_creation_human.RegisterHTMLReportTools(llmAgent, publicWorkspacePath, api.logger, readFile, hooks); err != nil {
		return fmt.Errorf("register Dashboard validator: %w", err)
	}
	if err := api.registerReportPreviewTool(llmAgent, sessionID, userID, publicWorkspacePath); err != nil {
		return fmt.Errorf("register Dashboard preview: %w", err)
	}
	if err := api.registerDashboardTools(llmAgent, userID, publicWorkspacePath); err != nil {
		return err
	}
	return nil
}
