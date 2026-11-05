package server

import (
	"context"
	"fmt"
	"time"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

type scheduleGuardRegistrar struct {
	definitionRegistrar
	guarded workflow.DefinitionToolRegistrar
}

func (r scheduleGuardRegistrar) RegisterCustomTool(n, d string, s map[string]interface{}, f func(context.Context, map[string]interface{}) (string, error), g string) error {
	return r.guarded.RegisterCustomTool(n, d, s, f, g)
}
func (r scheduleGuardRegistrar) RegisterCustomToolWithTimeout(n, d string, s map[string]interface{}, f func(context.Context, map[string]interface{}) (string, error), t time.Duration, g string) error {
	return r.guarded.RegisterCustomToolWithTimeout(n, d, s, f, t, g)
}

func (api *StreamingAPI) scheduleCollisionCheck(workspacePath, sessionID, triggeredBy string) workflow.ScheduleCollisionCheck {
	// A scheduled run must be able to execute its own steps and repairs.
	if isScheduledSessionIdentity(sessionID, triggeredBy) {
		return nil
	}
	return func(ctx context.Context, operation string, args map[string]interface{}) error {
		if binding, ok := api.botExecutionForSession(sessionID); ok {
			validated, err := api.revalidateExecutionPrincipal(context.WithValue(ctx, UserContextKey, binding.Claims), binding.Request)
			if err != nil {
				return err
			}
			if GetUserFromContext(validated).BotRouteGrant != binding.Claims.BotRouteGrant {
				return fmt.Errorf("bot grant changed; start a new turn before executing more tools")
			}
		}

		// No schedule-overlap guard: the person's own Builder chat runs its steps while a schedule runs (owner, 5 Nov).
		return nil
	}
}
