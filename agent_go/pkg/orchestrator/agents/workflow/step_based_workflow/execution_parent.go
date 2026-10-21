package step_based_workflow

import (
	"context"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func currentWorkshopParentExecutionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if parentExecutionID := strings.TrimSpace(virtualtools.SubAgentSpecFromContext(ctx).BackgroundAgentID); parentExecutionID != "" {
		return parentExecutionID
	}
	if parentExecutionID, _ := ctx.Value(orchestrator_events.ParentExecutionIDKey).(string); strings.TrimSpace(parentExecutionID) != "" {
		return strings.TrimSpace(parentExecutionID)
	}
	return ""
}

// withWorkshopExecutionParent copies lifecycle and cost identity from a live tool
// call into the detached workshop context. Workshop work must outlive the HTTP
// request, so its cancellation still derives from sessionCtx; copying the whole
// request context would incorrectly cancel a long-running workflow when the
// initiating tool call returns.
func withWorkshopExecutionParent(base, launch context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	base = costobserver.ContextWithAttribution(base, costobserver.AttributionFromContext(launch))
	base = costobserver.ContextWithSource(base, costobserver.SourceFromContext(launch))
	parentExecutionID := currentWorkshopParentExecutionID(launch)
	if parentExecutionID == "" {
		return base
	}
	base = virtualtools.WithBackgroundAgentID(base, parentExecutionID)
	return context.WithValue(base, orchestrator_events.ParentExecutionIDKey, parentExecutionID)
}
