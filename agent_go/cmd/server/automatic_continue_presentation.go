package server

import (
	"context"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// Presentation only: a server-created continuation is not a person's prompt.
// The full input still reaches the agent, and scheduling/delivery is unchanged.
func automaticContinueNoticeKind(req QueryRequest) string {
	if !req.IsAutoNotification {
		return ""
	}
	switch req.TriggeredBy {
	case "restart_continue":
		return "app_restarted"
	case usageLimitContinueTrigger:
		return "usage_limit_reset"
	}
	return ""
}
func automaticContinuePresentation(ctx context.Context, req QueryRequest, prompt string) context.Context {
	if kind := automaticContinueNoticeKind(req); kind != "" {
		return storeevents.WithBackgroundWakePresentation(ctx, prompt, storeevents.BackgroundWakePresentation{Kind: kind})
	}
	return ctx
}
