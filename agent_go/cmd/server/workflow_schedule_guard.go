package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
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

		if api.scheduler == nil {
			return nil
		}
		s := api.scheduler
		s.stateStoreMu.RLock()
		defer s.stateStoreMu.RUnlock()
		if s.stateStore == nil {
			return fmt.Errorf("schedule_state_unavailable: cannot verify workflow ownership; retry when scheduler storage is available")
		}
		// A Pulse turn only reads; the runs and edits it asks the Builder for
		// must not be refused because of it (owner, 2026-10-08: Pulse blocked
		// its own measurement re-runs on linkedin and social-media).
		run, err := s.stateStore.ActiveRunForScope(ctx, "workflow", filepath.Clean(strings.TrimSpace(workspacePath)),
			manualWorkflowPulseScheduleID, pulseFixRunScheduleID)
		if err != nil {
			return fmt.Errorf("schedule_state_unavailable: %w", err)
		}
		if run == nil {
			return nil
		}
		if force, ok := args["force"].(bool); ok && force {
			log.Printf("[SCHEDULE_GUARD] force override session=%s operation=%s schedule=%s run=%s", sessionID, operation, run.ScheduleID, run.RunID)
			return nil
		}
		body, _ := json.Marshal(map[string]interface{}{
			"code": "schedule_running", "operation": operation, "workspace_path": workspacePath,
			"schedule_id": run.ScheduleID, "run_id": run.RunID, "session_id": run.ActiveSessionID,
			"started_at": run.StartedAt, "lease_expires_at": run.UpdatedAt.Add(schedulerstate.LeaseDuration),
			"recovery_required": time.Since(run.UpdatedAt) > schedulerstate.LeaseDuration,
			"message":           "This workflow has an active schedule. No action was performed. Stop the current workflow-mutation batch, including different pending writes. Show one warning to the user and wait for explicit approval before retrying any of those actions with force=true. Do not bypass through shell or a background agent.",
		})
		return fmt.Errorf("%s", body)
	}
}
