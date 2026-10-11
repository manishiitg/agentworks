package step_based_workflow

import (
	"context"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// agentSequenceExecutionID reuses the workshop execution identity when a
// standalone step enters the agent runtime. A full workflow carries
// a workflow-level identity instead, so it deliberately returns an empty value
// and lets the caller mint a per-step ID.
func agentSequenceExecutionID(ctx context.Context, stepID string) string {
	if ctx == nil || strings.TrimSpace(stepID) == "" {
		return ""
	}
	id := strings.TrimSpace(virtualtools.SubAgentSpecFromContext(ctx).BackgroundAgentID)
	if strings.HasPrefix(id, "exec-"+stepID+"-") {
		return id
	}
	return ""
}
