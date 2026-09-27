package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/mcpagent/executor"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// humanAnswerScope is who may answer a decision through
// answer_human_input_request: only a person in their own Builder chat. The
// answer is recorded as theirs and applied in the same turn, so an agent that
// answers is an agent approving its own proposal.
//
// Refused:
//   - background and review agents (their tool sessions are registered in
//     workshop_tool_sessions.go), which includes every Pulse reviewer;
//   - scheduled runs and Pulse lifecycle turns (sched_/schedule- sessions);
//   - any session with no workflow or no authenticated owner.
//
// The workflow and the person come from the calling session through
// pulseToolScope (write access required), never from workspace_path or the
// model (reported 2026-09-27 against 4eb7fbad1).
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
	workspacePath, claims, err := pulsePlatformAPI.pulseToolScope(ctx, requestedWorkspace, true)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", refusal, err)
	}
	return workspacePath, claims, nil
}
