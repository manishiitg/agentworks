package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func init() {
	stepworkflow.BrainNoteReader = knowledgebaseReadReferencedNote
}

// knowledgebaseReadReferencedNote reads a Brain note that a step's description
// names (PLAT-556 decision 1). It acts as the run's person, under that person's
// folder roles, limited by the project's Brain access and always read-only: a
// step never reads a Brain folder its person or project may not read.
func knowledgebaseReadReferencedNote(ctx context.Context, workspacePath, notePath string) (string, error) {
	claims := GetUserFromContext(ctx)
	if !knowledgebaseMCPAllowed(claims) {
		return "", fmt.Errorf("Brain is unavailable to this run")
	}
	if claims.AccessToken != nil && !claims.AccessToken.Allows("knowledgebase:read") {
		return "", fmt.Errorf("this connection does not allow Brain reads")
	}
	project, err := knowledgeProjectLoad(ctx, claims.UserID, workspacePath, false)
	if err != nil {
		return "", fmt.Errorf("project Brain access is unavailable: %w", err)
	}
	mode := project.BrainMode()
	policy := &knowledgebase.BindingPolicy{Audience: project.Audience}
	if mode == "off" {
		return "", fmt.Errorf("Brain access is off for this project")
	}
	policy.ReadAll = true
	service, err := knowledgebaseService()
	if err != nil {
		return "", fmt.Errorf("Brain service unavailable")
	}
	if err := knowledgebaseSyncIdentities(ctx, service); err != nil {
		return "", err
	}
	r := (&http.Request{}).WithContext(context.WithValue(ctx, UserContextKey, claims))
	principal := knowledgebasePrincipal(r, claims)
	principal.BindingPolicy = policy
	result, err := service.CallTool(r.Context(), principal, knowledgebase.ToolRead, map[string]any{"action": "read", "path": notePath})
	if err != nil {
		return "", err
	}
	entry, ok := result.(map[string]any)
	if !ok {
		return "", fmt.Errorf("unexpected Brain read result")
	}
	content, ok := entry["content"].(string)
	if !ok {
		return "", fmt.Errorf("Brain note has no content")
	}
	return content, nil
}
