package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
)

// Schedules of a workflow or Crew over MCP. Every action runs through the
// app's own scheduler handlers, so validation (cron, calendar, groups, the
// collision guard) and permissions match the Schedules page: changes need
// the owner, run now and stop need the workflow to be visible, and Crew
// schedules follow the Crew's owner. Relays have no schedules (they use
// function triggers). The older list_schedules, trigger_schedule and
// get_schedule_runs stay for workflows.

var externalScheduleActions = []any{"list", "create", "update", "delete", "enable", "disable", "run_now", "stop", "runs"}

// Targeting fields the server sets itself; a caller's copy is ignored.
var externalScheduleTargetFields = []string{"workspace_path", "entity_type", "product_profile_id", "product_project_id", "id"}

func externalScheduleDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_schedules", "List, create, change, delete, enable, disable, run now, stop, or read the run history of a workflow's or Crew's schedules (not Relays: they use function triggers). Pass workflow_id or crew_id. Changes need the owner, as on the Schedules page; run_now and stop need run access.", true, false, map[string]any{
		"workflow_id": externalString("Workflow ID from list_workflows. Pass this or crew_id."),
		"crew_id":     externalString("Crew ID from list_crews. Pass this or workflow_id."),
		"action":      map[string]any{"type": "string", "enum": externalScheduleActions},
		"schedule_id": externalString("Schedule ID from action list; required except for list and create."),
		"schedule":    map[string]any{"type": "object", "description": "For create and update: the schedule's fields, as the Schedules page saves them. name, schedule_type (cron or calendar), cron_expression, timezone, calendar_items, enabled, messages (a Crew schedule needs exactly one), description; workflows also group_names, route_selections, collision_policy, after_run (backup/publish/notify), after_schedule_ids. update replaces the fields you send."},
		"limit":       externalInteger(1, 200),
		"offset":      externalInteger(0, 1000000),
	}, "action")
}

type externalScheduleTarget struct {
	workflow *DiscoveredWorkflow
	crewID   string
}

// scheduleHandler runs one app scheduler handler as the caller and returns
// its status and body.
func scheduleHandler(r *http.Request, handler http.HandlerFunc, method, path string, vars map[string]string, query url.Values, body any) (int, []byte) {
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req := httptest.NewRequest(method, target, reader).WithContext(r.Context())
	req.Header.Set("Content-Type", "application/json")
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	return recorder.Code, recorder.Body.Bytes()
}

func externalScheduleRespond(w http.ResponseWriter, status int, body []byte) {
	status, body = normalizeCreated(status, body)
	if status >= 400 {
		externalError(w, status, "schedule_rejected", strings.TrimSpace(string(body)))
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		externalJSON(w, map[string]any{"ok": true})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// crewScheduleJobs lists the caller's schedules of one Crew.
func (api *StreamingAPI) crewScheduleJobs(r *http.Request, crewID string) ([]ScheduledJobResponse, error) {
	status, body := scheduleHandler(r, listScheduledJobsHandler(api.scheduler), http.MethodGet, "/api/scheduler/jobs", nil,
		url.Values{"entity_type": {"product"}, "limit": {"1000"}}, nil)
	if status >= 400 {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	var page struct {
		Jobs []ScheduledJobResponse `json:"jobs"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, err
	}
	jobs := []ScheduledJobResponse{}
	for _, job := range page.Jobs {
		if job.WorkflowID == crewID {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (api *StreamingAPI) externalScheduleCall(w http.ResponseWriter, r *http.Request, args map[string]any, target externalScheduleTarget) {
	if api.scheduler == nil {
		externalError(w, http.StatusServiceUnavailable, "scheduler_unavailable", "The scheduler is not running on this server.")
		return
	}
	claims := GetUserFromContext(r.Context())
	action := externalArg(args, "action")
	scheduleID := externalArg(args, "schedule_id")
	if action != "list" && action != "create" && scheduleID == "" {
		externalError(w, 400, "invalid_arguments", "schedule_id is required for "+action+".")
		return
	}
	// Token scopes by action; the handlers check the person's role.
	if t := claims.AccessToken; t != nil {
		var allowed bool
		switch {
		case target.crewID != "" && (action == "list" || action == "runs"):
			allowed = t.Allows("crews:read")
		case target.crewID != "" && (action == "run_now" || action == "stop"):
			allowed = t.Allows("crews:run")
		case target.crewID != "":
			allowed = t.Allows("crews:write")
		case action == "run_now" || action == "stop":
			allowed = t.Allows("runs:execute")
		default:
			allowed = t.Allows("workflows:read") || t.Allows("runs:execute")
		}
		if !allowed || (target.crewID != "" && !t.AllowsCrew(target.crewID)) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" here.")
			return
		}
	}

	// The schedule must belong to the named workflow or Crew.
	if scheduleID != "" {
		belongs := false
		if target.workflow != nil {
			for _, sched := range target.workflow.Manifest.Schedules {
				if sched.ID == scheduleID {
					belongs = true
				}
			}
		} else {
			jobs, err := api.crewScheduleJobs(r, target.crewID)
			if err != nil {
				externalError(w, 502, "scheduler_unavailable", err.Error())
				return
			}
			for _, job := range jobs {
				if job.ID == scheduleID {
					belongs = true
				}
			}
		}
		if !belongs {
			externalError(w, 404, "schedule_not_found", "No schedule with that ID on this workflow or Crew; use action list.")
			return
		}
	}

	respond := func(status int, body []byte) { externalScheduleRespond(w, status, body) }
	vars := map[string]string{"id": scheduleID}
	path := "/api/scheduler/jobs/" + url.PathEscape(scheduleID)
	switch action {
	case "list":
		if target.workflow != nil {
			api.externalListSchedules(w, r, *target.workflow)
			return
		}
		jobs, err := api.crewScheduleJobs(r, target.crewID)
		if err != nil {
			externalError(w, 502, "scheduler_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"crew_id": target.crewID, "schedules": jobs})
	case "create", "update":
		schedule, _ := args["schedule"].(map[string]any)
		if schedule == nil {
			externalError(w, 400, "invalid_arguments", "schedule is required for "+action+".")
			return
		}
		body := map[string]any{}
		for key, value := range schedule {
			body[key] = value
		}
		for _, key := range externalScheduleTargetFields {
			delete(body, key)
		}
		if action == "create" {
			if target.workflow != nil {
				body["workspace_path"] = target.workflow.WorkspacePath
			} else {
				body["entity_type"], body["product_profile_id"], body["product_project_id"] = "product", "work", target.crewID
			}
			respond(scheduleHandler(r, createScheduledJobHandler(api.scheduler), http.MethodPost, "/api/scheduler/jobs", nil, nil, body))
			return
		}
		respond(scheduleHandler(r, updateScheduledJobHandler(api.scheduler), http.MethodPut, path, vars, nil, body))
	case "delete":
		respond(scheduleHandler(r, deleteScheduledJobHandler(api.scheduler), http.MethodDelete, path, vars, nil, nil))
	case "enable":
		respond(scheduleHandler(r, enableScheduledJobHandler(api.scheduler), http.MethodPost, path+"/enable", vars, nil, nil))
	case "disable":
		respond(scheduleHandler(r, disableScheduledJobHandler(api.scheduler), http.MethodPost, path+"/disable", vars, nil, nil))
	case "stop":
		respond(scheduleHandler(r, stopScheduledJobHandler(api.scheduler), http.MethodPost, path+"/stop", vars, nil, nil))
	case "run_now":
		if target.workflow != nil {
			api.externalTriggerSchedule(w, r, args, *target.workflow)
			return
		}
		respond(scheduleHandler(r, triggerScheduledJobHandler(api.scheduler), http.MethodPost, path+"/trigger", vars, nil, nil))
	case "runs":
		if target.workflow != nil {
			api.externalScheduleRuns(w, r, args, *target.workflow)
			return
		}
		query := url.Values{}
		query.Set("limit", fmt.Sprint(externalInt(args, "limit", 20)))
		query.Set("offset", fmt.Sprint(externalInt(args, "offset", 0)))
		respond(scheduleHandler(r, getScheduledJobRunsHandler(api.scheduler), http.MethodGet, path+"/runs", vars, query, nil))
	default:
		externalError(w, 400, "invalid_arguments", "Unknown action.")
	}
}

// normalizeCreated reports 201/202/204 as success for MCP callers.
func normalizeCreated(status int, body []byte) (int, []byte) {
	if status == http.StatusCreated || status == http.StatusAccepted || status == http.StatusNoContent {
		return http.StatusOK, body
	}
	return status, body
}
