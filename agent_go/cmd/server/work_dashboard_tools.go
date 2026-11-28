package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// registerWorkDashboardTools exposes the same managed SQLite and live HTML
// report primitives AgentWorks uses, scoped to one Crew project. Every session
// gets the same database registry; Run gets only its read source. Dashboard
// authoring and database writes stay with Builder sessions.
func (api *StreamingAPI) registerWorkDashboardTools(
	llmAgent *agent.LLMAgentWrapper,
	profile *resolvedAgentProfile,
	sessionID, userID, workspacePath string,
	readOnly bool,
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
		if readOnly && name != "query_workflow_db" {
			continue
		}
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
		if !readOnly && name == "query_workflow_db" {
			if props, ok := parameters["properties"].(map[string]interface{}); ok {
				props["from_project"] = map[string]interface{}{
					"type":        "string",
					"description": "Read another accessible project's or workflow's database read-only instead of this one's: its workspace_path from workspace action=references_list. Use describe and query to inspect or copy tables into this project with mutate.",
				}
			}
			executor = api.wrapCrewDatabaseReadFromProject(executor, userID, profile.ToolCallerClaims)
		}
		if err := llmAgent.RegisterCustomTool(name, definition.Function.Description, parameters, executor, registry.Categories[name]); err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
	}
	if readOnly {
		return nil
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
	if err := api.registerDashboardTools(llmAgent, userID, publicWorkspacePath, profile.ToolCallerClaims); err != nil {
		return err
	}
	return nil
}

// wrapCrewDatabaseReadFromProject lets a project Builder run read-only database
// queries (describe/query/integrity_check) against another accessible project or
// workflow named by from_project, matching dashboard from_project. Mutations stay
// strictly bound to the current project.
func (api *StreamingAPI) wrapCrewDatabaseReadFromProject(
	base func(context.Context, map[string]any) (string, error),
	userID string,
	caller *UserClaims,
) func(context.Context, map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		fromRaw, _ := args["from_project"].(string)
		from := strings.TrimRight(strings.TrimSpace(fromRaw), "/")
		if from == "" {
			return base(ctx, args)
		}
		claims := caller
		if current := GetUserFromContext(ctx); current != nil && current.UserID != userID {
			return "", fmt.Errorf("Open your own project chat to read another project's database")
		}
		if claims == nil || claims.UserID != userID {
			return "", fmt.Errorf("Open your own project chat to read another project's database")
		}
		ctx = context.WithValue(ctx, UserContextKey, claims)
		targetRoot, err := api.resolveCrewDatabaseReadTarget(ctx, claims, from)
		if err != nil {
			return "", err
		}
		queryArgs := make(map[string]any, len(args))
		for k, v := range args {
			if k == "from_project" {
				continue
			}
			queryArgs[k] = v
		}
		client := api.reportPreviewWorkspaceClient(ctx, claims, targetRoot)
		return virtualtools.RunWorkflowDBQuery(ctx, client, path.Join(targetRoot, "db", "db.sqlite"), queryArgs)
	}
}

func (api *StreamingAPI) resolveCrewDatabaseReadTarget(ctx context.Context, claims *UserClaims, root string) (string, error) {
	unavailable := fmt.Errorf("project or workflow database unavailable; pick a workspace_path from workspace action=references_list that you can open")
	clean, err := wf.CleanRelative(root)
	if err != nil || clean != root {
		return "", fmt.Errorf("from_project must be a canonical workspace_path from workspace action=references_list")
	}
	if crew, ok := resolveCrewPath(ctx, claims.UserID, clean); ok && crew.Rest == "" {
		if crewAccessFor(claims, crew) == crewAccessNone {
			return "", unavailable
		}
		product, _, _ := projectProductForPath(crew.Root)
		manifest, err := readCrewProjectManifests(ctx, product.ProfileID, crew.Root)
		if err != nil {
			return "", unavailable
		}
		id := path.Base(crew.Root)
		if strings.TrimSpace(manifest.ID) != "" {
			id = strings.TrimSpace(manifest.ID)
		}
		if claims.AccessToken != nil && (!claims.AccessToken.Allows("crews:read") || !claims.AccessToken.AllowsCrew(id)) {
			return "", fmt.Errorf("this connection cannot read that project's database")
		}
		return crew.Root, nil
	}
	if !isReportRunWorkflowRoot(clean) {
		return "", unavailable
	}
	access, manifest := workflowAccessForWorkspacePath(ctx, claims, clean)
	if access == WorkflowAccessNone || manifest == nil || manifest.Kind == "relay" || !userAllowedWorkflowID(claims, manifest.ID) {
		return "", unavailable
	}
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("workflows:read") || !claims.AccessToken.AllowsWorkflow(manifest.ID)) {
		return "", fmt.Errorf("this connection cannot read that workflow's database")
	}
	return clean, nil
}
