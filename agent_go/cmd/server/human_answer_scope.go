package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/mcpagent/executor"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func humanAnswerScope(ctx context.Context, requestedWorkspace string) (string, *UserClaims, error) {
	sessionID := strings.TrimSpace(executor.SessionIDFromContext(ctx))
	if sessionID == "" {
		sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
		sessionID = strings.TrimSpace(sessionID)
	}
	const refusal = "only a person in their own Builder chat can answer a decision; leave it pending in Needs you"
	if _, ok := step.LookupWorkshopToolSession(sessionID); ok {
		return "", nil, fmt.Errorf("%s (a background agent cannot answer it)", refusal)
	}
	if sessionID == "" || isScheduledSession(sessionID) {
		return "", nil, fmt.Errorf("%s (a scheduled or unattended run cannot answer it)", refusal)
	}
	if workspacePath, claims, crew, err := workflowToolsAPI.crewHumanAnswerScope(ctx, sessionID, requestedWorkspace); crew {
		return workspacePath, claims, err
	}
	workspacePath, claims, err := workflowToolsAPI.workflowToolScope(ctx, requestedWorkspace, true)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", refusal, err)
	}
	return workspacePath, claims, nil
}
