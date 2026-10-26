package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// read_linked_goal lets any Crew read a linked Goal's runs and measurements (PLAT-825-99). Crews differ by content,
// not features (decisions log 2026-10-26): AgentWorks emails through the Crew's own Google account like any Crew.

func (api *StreamingAPI) registerAgentWorksAgentTools(registrar definitionToolRegistrar, gate *productToolGate, userID, workspacePath string) error {
	register := func(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		gate.Declare(name)
		return registrar.RegisterCustomTool(name, description, parameters, execute, "agentworks_agent_tools")
	}
	claims := &UserClaims{UserID: strings.TrimSpace(userID)}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username = record.Username
		claims.Email = record.Email
	}
	withClaims := func(ctx context.Context) context.Context {
		if isExternalCrewDirectCall(ctx) {
			return ctx
		}
		copy := *claims
		return context.WithValue(ctx, UserContextKey, &copy)
	}

	if err := register("read_linked_goal", "Read one linked Goal you can currently open as its owner or editor, read-only. action=runs: its scheduled runs, newest first (optional schedule_id; limit and offset page each schedule). action=metrics: its saved measurements and their history. Pass the Goal's exact workspace_path from the linked list.", map[string]interface{}{
		"type": "object", "required": []string{"workspace_path", "action"}, "properties": map[string]interface{}{
			"workspace_path": map[string]interface{}{"type": "string", "description": "The linked Goal's exact workspace_path."},
			"action":         map[string]interface{}{"type": "string", "enum": []string{"runs", "metrics"}},
			"schedule_id":    map[string]interface{}{"type": "string", "description": "For runs: one schedule; omit for every timer of the Goal."},
			"limit":          map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 50},
			"offset":         map[string]interface{}{"type": "integer", "minimum": 0},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		target, manifest, err := api.agentWorksReadableGoal(ctx, workspacePath, stringToolArg(args, "workspace_path"))
		if err != nil {
			return "", err
		}
		switch stringToolArg(args, "action") {
		case "metrics":
			ledger, err := step.LoadGoalMeasurements(ctx, target, 200)
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(map[string]interface{}{"workflow_id": manifest.ID, "metrics": ledger.Metrics, "observations": ledger.Observations})
			return string(encoded), err
		case "runs":
			limit, offset := min(max(externalInt(args, "limit", 10), 1), 50), max(externalInt(args, "offset", 0), 0)
			only := strings.TrimSpace(stringToolArg(args, "schedule_id"))
			schedules := []map[string]interface{}{}
			for _, schedule := range manifest.Schedules {
				if schedule.ScheduleType == "webhook" || only != "" && schedule.ID != only {
					continue
				}
				runs, total, err := ListScheduleRuns(ctx, target, schedule.ID, limit, offset)
				if err != nil {
					return "", fmt.Errorf("run history of %q is unavailable", schedule.Name)
				}
				rows := make([]map[string]any, 0, len(runs))
				for _, run := range runs {
					rows = append(rows, externalCompactScheduleRun(run))
				}
				schedules = append(schedules, map[string]interface{}{
					"schedule_id": schedule.ID, "name": schedule.Name, "enabled": schedule.Enabled, "cron_expression": schedule.CronExpression,
					"timezone": schedule.Timezone, "runs": rows, "total": total, "has_more": offset+len(rows) < total,
				})
			}
			encoded, err := json.Marshal(map[string]interface{}{"workflow_id": manifest.ID, "label": manifest.Label, "schedules": schedules})
			return string(encoded), err
		default:
			return "", fmt.Errorf("action must be runs or metrics")
		}
	}); err != nil {
		return err
	}

	return nil
}

// agentWorksReadableGoal admits one Goal for reading: linked to this AgentWorks, and the person can open it as owner
// or editor right now (rechecked on every call, so a lost access stops the reads at once).
func (api *StreamingAPI) agentWorksReadableGoal(ctx context.Context, crewRoot, raw string) (string, *WorkflowManifest, error) {
	target := strings.TrimSuffix(strings.TrimSpace(raw), "/")
	refs, err := readWorkWorkflowReferences(ctx, crewRoot)
	if err != nil {
		return "", nil, fmt.Errorf("linked Goals are unavailable")
	}
	linked := false
	for _, ref := range refs {
		if target != "" && strings.TrimSuffix(strings.TrimSpace(ref), "/") == target {
			linked = true
		}
	}
	if !linked {
		return "", nil, fmt.Errorf("%q is not linked to this Crew; use the exact workspace_path of a linked Goal", target)
	}
	if isCrewProjectPath(target) {
		return "", nil, fmt.Errorf("%q is a Crew; read its files instead", target)
	}
	if isExternalCrewDirectCall(ctx) && !api.externalCrewReferenceAllowed(ctx, GetUserFromContext(ctx), target, false) {
		return "", nil, fmt.Errorf("that Goal is outside this connection's access")
	}
	if level, _ := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), target); level != WorkflowAccessOwner && level != WorkflowAccessWrite {
		return "", nil, fmt.Errorf("you can no longer open %q as its owner or editor", target)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, target)
	if err != nil || !found || manifest == nil {
		return "", nil, fmt.Errorf("Goal %q is unavailable", target)
	}
	return target, manifest, nil
}
