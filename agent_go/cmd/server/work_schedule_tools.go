package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// registerWorkScheduleTools exposes the project-scoped subset Work needs:
// recurring one-message jobs. Definitions live in workflow.json and execution
// reuses ProductScheduleService, so there is no second scheduler.
func (api *StreamingAPI) registerWorkScheduleTools(registrar definitionToolRegistrar, profileID, userID, workspacePath string, readOnly bool) error {
	if api.productSchedules == nil {
		return nil
	}
	// Product conversations keep a public Chats/... binding, while project
	// manifests live under the paired user's _users/<id>/Chats/... directory.
	// Read the manifest from that runtime path before registering tools.
	projectWorkspace := agentProfileRuntimeWorkspace(userID, workspacePath)
	raw, found, err := readFileFromWorkspace(context.Background(), filepath.ToSlash(filepath.Join(projectWorkspace, "product.json")))
	if err != nil || !found {
		return firstError(err, fmt.Errorf("project manifest not found"))
	}
	var manifest productProjectManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	if !strings.EqualFold(manifest.Product, profileID) || !isProjectProfileID(profileID) || strings.TrimSpace(manifest.ID) == "" {
		return fmt.Errorf("invalid project manifest")
	}
	projectID := manifest.ID
	// Crew Run mode: readers list the crew's own schedules and triggers,
	// resolved under the crew owner. Mutations below are owner-only.
	listUserID := userID
	if readOnly {
		if ownerID, ok := crewProjectOwnerID(projectWorkspace); ok {
			listUserID = ownerID
		}
	}
	// Only a Crew is callable through functions; a Code never is.
	crewFunctionsHint := ""
	if strings.EqualFold(profileID, "work") {
		crewFunctionsHint = " Other Crews, workflows and external connections do not need a webhook: they call this Crew's functions (call_function; `ask` is always available), which sets up their binding automatically."
	}
	register := func(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		return registrar.RegisterCustomTool(name, description, parameters, execute, "work_schedule_tools")
	}
	resolveID := func(value string) string {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, projectScheduleJobPrefix) {
			return value
		}
		return projectScheduleJobID(profileID, projectID, value)
	}
	if err := register("list_project_schedules", "List this project's recurring message schedules and their latest run status.", map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{},
	}, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		jobs, err := api.productSchedules.JobsForUser(ctx, listUserID)
		if err != nil {
			return "", err
		}
		var out []ScheduledJobResponse
		for _, job := range jobs {
			if strings.EqualFold(job.Profile.ID, profileID) && job.ProjectID == projectID {
				runsWorkspace, _ := api.productSchedules.RunsWorkspace(ctx, job)
				out = append(out, api.productSchedules.jobResponse(job, runsWorkspace))
			}
		}
		encoded, err := json.MarshalIndent(map[string]interface{}{"schedules": out}, "", "  ")
		return string(encoded), err
	}); err != nil {
		return err
	}
	if !readOnly {
		if err := register("create_project_schedule", "Create one schedule for this project: recurring (cron_expression) or one-time (in_minutes, or run_at for an exact moment). A one-time schedule runs once and never again, for example 'check the deploy in 3 hours'. Choose crew_chat to queue work in the main project conversation, or isolated for this schedule's own persistent automation conversation.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":            map[string]interface{}{"type": "string"},
				"message":         map[string]interface{}{"type": "string", "description": "The single instruction the agent should perform at each occurrence."},
				"cron_expression": map[string]interface{}{"type": "string", "description": "Standard five-field cron expression, for a recurring schedule."},
				"in_minutes":      map[string]interface{}{"type": "integer", "minimum": 1, "maximum": oneTimeMaxMinutes, "description": "One-time: run once this many minutes from now (180 = 3 hours)."},
				"run_at":          map[string]interface{}{"type": "string", "description": "One-time: run once at this exact moment, RFC3339 with an offset, for example 2026-10-05T21:30:00+05:30. Must be in the future."},
				"timezone":        map[string]interface{}{"type": "string", "description": "IANA timezone, for example Asia/Kolkata. Required with cron_expression; for a one-time schedule it only sets how the time is shown."},
				"enabled":         map[string]interface{}{"type": "boolean"},
				"run_destination": map[string]interface{}{"type": "string", "enum": []string{runDestinationCrewChat, runDestinationIsolated}, "description": "Where runs execute. Defaults to crew_chat."},
			},
			"required": []string{"name", "message"},
		}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["name"].(string)
			message, _ := args["message"].(string)
			cronExpression, _ := args["cron_expression"].(string)
			timezone, _ := args["timezone"].(string)
			runAt, err := oneTimeRunAt(args, time.Now())
			if err != nil {
				return "", err
			}
			if (strings.TrimSpace(cronExpression) != "") == (runAt != "") {
				return "", fmt.Errorf("give exactly one of cron_expression (recurring) or in_minutes / run_at (one-time)")
			}
			if runAt == "" && strings.TrimSpace(timezone) == "" {
				return "", fmt.Errorf("timezone is required with cron_expression")
			}
			enabled, hasEnabled := args["enabled"].(bool)
			destination, _ := args["run_destination"].(string)
			isolated, err := isolatedForRunDestination(destination)
			if err != nil {
				return "", err
			}
			if !hasEnabled {
				enabled = true
			}
			job, err := api.productSchedules.CreateProjectSchedule(ctx, userID, profileID, projectID, productschedule.Schedule{
				Name: strings.TrimSpace(name), Messages: []string{strings.TrimSpace(message)}, CronExpression: strings.TrimSpace(cronExpression), RunAt: runAt, Timezone: strings.TrimSpace(timezone), Enabled: enabled, Isolated: isolated,
			})
			if err != nil {
				return "", err
			}
			runsWorkspace, _ := api.productSchedules.RunsWorkspace(ctx, job)
			encoded, err := json.MarshalIndent(api.productSchedules.jobResponse(job, runsWorkspace), "", "  ")
			return string(encoded), err
		}); err != nil {
			return err
		}
		if err := register("update_project_schedule", "Update a project message schedule. Call list_project_schedules first and use its exact id.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"}, "message": map[string]interface{}{"type": "string"},
				"cron_expression": map[string]interface{}{"type": "string"}, "timezone": map[string]interface{}{"type": "string"},
				"in_minutes": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": oneTimeMaxMinutes, "description": "Make it one-time: run once this many minutes from now. Replaces the recurring timing."},
				"run_at":     map[string]interface{}{"type": "string", "description": "Make it one-time: run once at this RFC3339 moment. Replaces the recurring timing."}, "enabled": map[string]interface{}{"type": "boolean"},
				"run_destination": map[string]interface{}{"type": "string", "enum": []string{runDestinationCrewChat, runDestinationIsolated}},
			},
			"required": []string{"id"},
		}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			runAt, runAtErr := oneTimeRunAt(args, time.Now())
			if runAtErr != nil {
				return "", runAtErr
			}
			var requestedIsolation *bool
			if value, ok := args["run_destination"].(string); ok {
				isolated, destinationErr := isolatedForRunDestination(value)
				if destinationErr != nil {
					return "", destinationErr
				}
				requestedIsolation = &isolated
			}
			job, err := api.productSchedules.UpdateProjectSchedule(ctx, userID, resolveID(id), func(schedule *productschedule.Schedule) {
				if value, ok := args["name"].(string); ok && strings.TrimSpace(value) != "" {
					schedule.Name = strings.TrimSpace(value)
				}
				if value, ok := args["message"].(string); ok && strings.TrimSpace(value) != "" {
					schedule.Messages = []string{strings.TrimSpace(value)}
				}
				if value, ok := args["cron_expression"].(string); ok && strings.TrimSpace(value) != "" {
					schedule.CronExpression, schedule.CadenceHours, schedule.RunAt = strings.TrimSpace(value), 0, ""
				}
				if runAt != "" {
					schedule.RunAt, schedule.CronExpression, schedule.CadenceHours = runAt, "", 0
				}
				if value, ok := args["timezone"].(string); ok && strings.TrimSpace(value) != "" {
					schedule.Timezone = strings.TrimSpace(value)
				}
				if value, ok := args["enabled"].(bool); ok {
					schedule.Enabled = value
				}
				if requestedIsolation != nil {
					schedule.Isolated = *requestedIsolation
				}
			})
			if err != nil {
				return "", err
			}
			runsWorkspace, _ := api.productSchedules.RunsWorkspace(ctx, job)
			encoded, err := json.MarshalIndent(api.productSchedules.jobResponse(job, runsWorkspace), "", "  ")
			return string(encoded), err
		}); err != nil {
			return err
		}
		if err := register("delete_project_schedule", "Delete a project schedule. Call list_project_schedules first and use its exact id.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}, "required": []string{"id"},
		}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			if err := api.productSchedules.DeleteProjectSchedule(ctx, userID, resolveID(id)); err != nil {
				return "", err
			}
			return "Schedule deleted.", nil
		}); err != nil {
			return err
		}
	}
	if err := register("list_project_triggers", "List the authenticated webhook triggers stored in this project's workflow.json. Secrets are never returned.", map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{},
	}, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		triggers, err := api.productSchedules.projectWebhookConfigs(ctx, listUserID, profileID, projectID)
		if err != nil {
			return "", err
		}
		out := make([]productWebhookResponse, 0, len(triggers))
		for _, trigger := range triggers {
			out = append(out, productWebhookDTO(trigger))
		}
		encoded, err := json.MarshalIndent(map[string]interface{}{"triggers": out}, "", "  ")
		return string(encoded), err
	}); err != nil {
		return err
	}
	if !readOnly {
		if err := register("create_project_trigger", "Create an authenticated webhook trigger for this project. Choose crew_chat to queue work in the main project conversation, or isolated for this webhook's own continuing conversation. Return the one-time secret immediately, and tell the user that anyone holding the URL and secret can start a run as them, with their connections and secrets."+crewFunctionsHint, map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string"}, "message": map[string]interface{}{"type": "string"},
				"auth_mode": map[string]interface{}{"type": "string", "enum": []string{"bearer", "github"}}, "enabled": map[string]interface{}{"type": "boolean"},
				"run_destination": map[string]interface{}{"type": "string", "enum": []string{runDestinationCrewChat, runDestinationIsolated}},
				"kind":            map[string]interface{}{"type": "string", "enum": []string{"internal"}},
				"caller":          triggerCallerToolSchema(triggerCallerWorkflow, triggerCallerCrew),
			}, "required": []string{"name", "message"},
		}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["name"].(string)
			message, _ := args["message"].(string)
			authMode, _ := args["auth_mode"].(string)
			destination, _ := args["run_destination"].(string)
			kind, _ := args["kind"].(string)
			enabled, ok := args["enabled"].(bool)
			if !ok {
				enabled = true
			}
			response, _, err := api.productSchedules.saveProductWebhookConfig(ctx, userID, productWebhookRequest{ProfileID: profileID, ProjectID: projectID, Name: name, Message: message, AuthMode: authMode, Enabled: enabled, RunDestination: destination, Kind: kind, Caller: triggerCallerFromArgs(args)}, "")
			if err != nil {
				return "", err
			}
			encoded, err := json.MarshalIndent(response, "", "  ")
			return string(encoded), err
		}); err != nil {
			return err
		}
		if err := register("update_project_trigger", "Update, enable, disable, or rotate a project webhook trigger. Call list_project_triggers first. A rotated secret is returned only once. Internal triggers carry kind and caller instead of a secret.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"}, "message": map[string]interface{}{"type": "string"},
				"auth_mode": map[string]interface{}{"type": "string", "enum": []string{"bearer", "github"}}, "enabled": map[string]interface{}{"type": "boolean"}, "rotate_secret": map[string]interface{}{"type": "boolean"},
				"run_destination": map[string]interface{}{"type": "string", "enum": []string{runDestinationCrewChat, runDestinationIsolated}},
				"kind":            map[string]interface{}{"type": "string", "enum": []string{"internal"}},
				"caller":          triggerCallerToolSchema(triggerCallerWorkflow, triggerCallerCrew),
			}, "required": []string{"id"},
		}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			triggers, err := api.productSchedules.projectWebhookConfigs(ctx, userID, profileID, projectID)
			if err != nil {
				return "", err
			}
			var current *productWebhookTrigger
			for i := range triggers {
				if triggers[i].ID == strings.TrimSpace(id) {
					current = &triggers[i]
					break
				}
			}
			if current == nil {
				return "", fmt.Errorf("trigger not found")
			}
			name, message, enabled, destination := current.Name, current.Message, current.Enabled, firstNonEmptyTrimmed(current.RunDestination, runDestinationCrewChat)
			authMode := ""
			if current.Webhook != nil {
				authMode = current.Webhook.AuthMode
			}
			kind := current.Kind
			caller := current.Caller
			if value, ok := args["name"].(string); ok && strings.TrimSpace(value) != "" {
				name = value
			}
			if value, ok := args["message"].(string); ok && strings.TrimSpace(value) != "" {
				message = value
			}
			if value, ok := args["auth_mode"].(string); ok && strings.TrimSpace(value) != "" {
				authMode = value
			}
			if value, ok := args["enabled"].(bool); ok {
				enabled = value
			}
			if value, ok := args["run_destination"].(string); ok && strings.TrimSpace(value) != "" {
				destination = value
			}
			if value, ok := args["kind"].(string); ok && strings.TrimSpace(value) != "" {
				kind = value
			}
			if updated := triggerCallerFromArgs(args); updated != nil {
				caller = updated
			}
			rotate, _ := args["rotate_secret"].(bool)
			response, _, err := api.productSchedules.saveProductWebhookConfig(ctx, userID, productWebhookRequest{ProfileID: profileID, ProjectID: projectID, Name: name, Message: message, AuthMode: authMode, Enabled: enabled, RotateSecret: rotate, RunDestination: destination, Kind: kind, Caller: caller}, current.ID)
			if err != nil {
				return "", err
			}
			encoded, err := json.MarshalIndent(response, "", "  ")
			return string(encoded), err
		}); err != nil {
			return err
		}
		if err := register("delete_project_trigger", "Delete a project webhook trigger. Call list_project_triggers first and use its exact id.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}, "required": []string{"id"},
		}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			if err := api.productSchedules.deleteProductWebhookConfig(ctx, userID, profileID, projectID, strings.TrimSpace(id)); err != nil {
				return "", err
			}
			return "Trigger deleted.", nil
		}); err != nil {
			return err
		}
		return register("trigger_project_schedule", "Run a project schedule now. Call list_project_schedules first and use its exact id.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}, "required": []string{"id"},
		}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, _ := args["id"].(string)
			sessionID, err := api.productSchedules.Trigger(ctx, userID, resolveID(id))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Schedule started in session %s.", sessionID), nil
		})
	}
	return nil
}

// oneTimeMaxMinutes is the longest in_minutes a one-time schedule accepts (a year).
const oneTimeMaxMinutes = 365 * 24 * 60

// oneTimeRunAt turns the tool's in_minutes / run_at arguments into the RFC3339 instant stored on a
// one-time schedule. It returns "" when neither is given. A moment in the past is refused: the
// scheduler would run it at once, which is never what "check this later" means.
func oneTimeRunAt(args map[string]interface{}, now time.Time) (string, error) {
	runAt, _ := args["run_at"].(string)
	runAt = strings.TrimSpace(runAt)
	minutes, hasMinutes := 0, false
	switch value := args["in_minutes"].(type) {
	case float64:
		minutes, hasMinutes = int(value), true
	case int:
		minutes, hasMinutes = value, true
	}
	if runAt != "" && hasMinutes {
		return "", fmt.Errorf("give either in_minutes or run_at, not both")
	}
	if hasMinutes {
		if minutes < 1 || minutes > oneTimeMaxMinutes {
			return "", fmt.Errorf("in_minutes must be between 1 and %d", oneTimeMaxMinutes)
		}
		return now.Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339), nil
	}
	if runAt == "" {
		return "", nil
	}
	at, err := productschedule.Schedule{RunAt: runAt}.RunAtTime()
	if err != nil {
		return "", err
	}
	if !at.After(now) {
		return "", fmt.Errorf("run_at %s is not in the future", runAt)
	}
	return at.UTC().Format(time.RFC3339), nil
}
