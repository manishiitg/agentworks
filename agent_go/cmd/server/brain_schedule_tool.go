package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
)

// brainScheduleToolName lets the Brain chat manage the person's Organize Brain schedule (PLAT-618): see it, turn it on
// or off, set how often it runs and what it asks for, or run it now. Brain schedules always run in the Brain chat. It acts only on the caller's own schedule state.
const brainScheduleToolName = "brain_schedule"

func (api *StreamingAPI) registerBrainScheduleTool(registrar definitionToolRegistrar, userID string) error {
	if api == nil || api.productSchedules == nil {
		return nil
	}
	jobID := productScheduleJobID(knowledgebaseproduct.ProfileID, "organize")
	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":        map[string]interface{}{"type": "string", "enum": []string{"status", "enable", "disable", "set_cadence", "set_message", "run_now"}},
			"message":       map[string]interface{}{"type": "string", "description": "For set_message: what each scheduled run should do, in the person's words (for example \"organize Engineering by teams and merge duplicates\" or \"only propose changes, do not apply them\"). Empty restores the default."},
			"cadence_hours": map[string]interface{}{"type": "integer", "minimum": minScheduleCadenceHours, "maximum": maxScheduleCadenceHours, "description": "For set_cadence: hours between runs, for example 72 for every 3 days or 168 for weekly."},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
	description := "Manage this person's Organize Brain schedule. It runs in this Brain chat, as this person, and sends the schedule's message (default: organize the whole Brain following \"Organizing Brain\"). action=status shows whether it is on, how often, the message and the last run; enable/disable turn it on or off; set_cadence sets how often (cadence_hours); set_message sets what each run should do (scope, mode, apply or only propose); run_now starts a run at once."
	return registrar.RegisterCustomTool(brainScheduleToolName, description, params, func(ctx context.Context, args map[string]interface{}) (string, error) {
		claims := GetUserFromContext(ctx)
		if claims == nil || claims.UserID != userID {
			return "", fmt.Errorf("the Brain schedule belongs to its signed-in person")
		}
		ps := api.productSchedules
		action, _ := args["action"].(string)
		var job productScheduleJob
		var err error
		switch strings.TrimSpace(action) {
		case "status":
			job, err = ps.Job(ctx, userID, jobID)
		case "enable", "disable":
			job, err = ps.SetEnabled(ctx, userID, jobID, action == "enable")
		case "set_cadence":
			hours, ok := args["cadence_hours"].(float64)
			if !ok {
				return "", fmt.Errorf("set_cadence needs cadence_hours")
			}
			job, err = ps.SetCadence(ctx, userID, jobID, int(hours))
		case "set_message":
			message, _ := args["message"].(string)
			job, err = ps.SetMessage(ctx, userID, jobID, message)
		case "run_now":
			session, runErr := ps.Trigger(ctx, userID, jobID)
			if runErr != nil {
				return "", runErr
			}
			return fmt.Sprintf(`{"started":true,"session_id":%q}`, session), nil
		default:
			return "", fmt.Errorf("unknown action %q", action)
		}
		if err != nil {
			return "", err
		}
		effective := job.Effective()
		out, _ := json.Marshal(map[string]any{"enabled": effective.Enabled, "cadence_hours": effective.CadenceHours, "messages": effective.Messages, "last_run_at": job.State.LastRunAt, "last_status": job.State.LastStatus, "run_count": job.State.RunCount})
		return string(out), nil
	}, "knowledgebase")
}
