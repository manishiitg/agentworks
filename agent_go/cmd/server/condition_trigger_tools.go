package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/robfig/cron/v3"
)

var conditionConfigMu sync.Mutex

func conditionToolSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{
		"action":            map[string]interface{}{"type": "string", "enum": []interface{}{"list", "create", "update", "delete", "history", "check"}},
		"id":                map[string]interface{}{"type": "string", "description": "Exact watcher id from list."},
		"request_id":        map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Create: stable request id, reuse after uncertain transport to recover the same trigger."},
		"expected_revision": map[string]interface{}{"type": "string", "description": "Update: revision returned by list; stale edits are refused."},
		"trigger":           map[string]interface{}{"type": "object", "description": "For create/update: name, cron_expression (at least 5 minutes apart), timezone, enabled, script (saved Python under code/triggers), secrets (selected project names), target {kind: agent|function|route|trigger, message/chat_id for this Code agent, name/function/arguments for shared functions, name/route_selections/group_names for saved workflow routes, name/trigger_id for existing internal triggers}. The platform pins the current script hash. Partial updates preserve omitted fields."},
	}}
}

const conditionToolDescription = "Manage this project's Watchers: deterministic Python checks on a timer, without a model turn, dispatching matching events to a saved authorized agent/function/route. Code is the first source product. Create with a stable request_id; list first and use expected_revision for updates. Save the script under code/triggers: read previous state JSON from stdin, print exactly {events:[{id,payload}],state:{...}}; events=[] is a no-op. Use a stable source id and advance the cursor so old items are not rediscovered. Select only required project secrets (SECRET_<NAME> environment variables); no MCP tools are passed to the detector. Agent targets stay in this Code; functions/routes may be shared targets you can use. history shows checks, dispatch receipts and reconciliation. check runs and dispatches now; use only when requested. Updates accept the current script hash and preserve cursor/history. Target edits hold old queued events for reconciliation. No exactly-once guarantee for external effects; write-back belongs in the target."

func (api *StreamingAPI) registerConditionTools(reg definitionToolRegistrar, userID, profileID, projectID string) error {
	if profileID != "code" {
		return nil
	}
	return reg.RegisterCustomTool("manage_condition_triggers", conditionToolDescription, conditionToolSchema(), func(ctx context.Context, args map[string]interface{}) (string, error) {
		out, err := api.productSchedules.conditionService().manage(internalBotRequestContext(ctx, userID), userID, profileID, projectID, args)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(out)
		return string(b), err
	}, "work_schedule_tools")
}

func (c *conditionService) source(ctx context.Context, owner, profile, project string) (productScheduleJob, error) {
	if profile != "code" {
		return productScheduleJob{}, errors.New("watchers are currently available in Code")
	}
	p, b, m, err := c.s.projectManifest(ctx, owner, profile, project)
	if err != nil {
		return productScheduleJob{}, err
	}
	job := productScheduleJob{UserID: owner, Profile: p, ProjectID: m.ID, ProjectTitle: m.displayTitle(), WorkspacePath: b.WorkspacePath, ManifestPath: b.ManifestPath}
	caller := c.caller(job)
	if err := authorizeOwnedCodeCaller(ctx, owner, owner, caller); err != nil {
		return productScheduleJob{}, err
	}
	return job, nil
}

func conditionView(s productschedule.Schedule) map[string]interface{} {
	b, _ := json.Marshal(s.Condition)
	out := map[string]interface{}{}
	_ = json.Unmarshal(b, &out)
	out["id"], out["name"], out["enabled"], out["cron_expression"], out["timezone"] = s.ID, s.Name, s.Enabled, s.CronExpression, s.Timezone
	revision, _ := json.Marshal(s)
	out["revision"] = conditiontrigger.Hash(string(revision))
	return out
}

func (c *conditionService) manage(ctx context.Context, owner, profile, project string, args map[string]interface{}) (interface{}, error) {
	action, _ := args["action"].(string)
	if action == "create" || action == "update" || action == "delete" {
		conditionConfigMu.Lock()
		defer conditionConfigMu.Unlock()
	}
	base, err := c.source(ctx, owner, profile, project)
	if err != nil {
		return nil, err
	}
	id, _ := args["id"].(string)
	if action == "list" {
		jobs, err := c.s.JobsForUser(ctx, owner)
		if err != nil {
			return nil, err
		}
		out := []interface{}{}
		for _, j := range jobs {
			if j.Profile.ID == profile && j.ProjectID == project && j.Schedule.Condition != nil {
				v := conditionView(j.Schedule)
				v["enabled"] = j.Effective().Enabled
				v["last_status"], v["last_error"], v["last_run_at"] = j.State.LastStatus, j.State.LastError, j.State.LastRunAt
				out = append(out, v)
			}
		}
		return map[string]interface{}{"triggers": out}, nil
	}
	job := base
	if action != "create" {
		if id == "" {
			return nil, errors.New("id is required; list watchers first")
		}
		job, err = c.s.Job(ctx, owner, projectScheduleJobID(profile, project, id))
		if err != nil || job.Schedule.Condition == nil {
			return nil, errors.New("watcher not found")
		}
	}
	switch action {
	case "delete":
		if err := c.s.DeleteProjectSchedule(ctx, owner, job.ID()); err != nil {
			return nil, err
		}
		return map[string]interface{}{"deleted": true}, nil
	case "check":
		if !job.Effective().Enabled {
			return nil, errors.New("enable the watcher before checking and dispatching")
		}
		_, err := c.poll(ctx, job, "manual", time.Time{})
		return map[string]interface{}{"checked": err == nil}, err
	case "history":
		l, err := c.engine.Read(ctx, conditionKey(job))
		if err != nil {
			return nil, err
		}
		runs, err := ReadScheduleRuns(ctx, job.WorkspacePath)
		if err != nil {
			return nil, err
		}
		checks := []ScheduleRunEntry{}
		for _, run := range runs {
			if run.ScheduleID == job.ID() {
				checks = append(checks, run)
			}
		}
		if len(checks) > 20 {
			checks = checks[len(checks)-20:]
		}
		pending := conditiontrigger.Pending(l)
		if l.Rows == nil {
			l.Rows = []conditiontrigger.Row{}
		}
		if len(l.Rows) > 50 {
			l.Rows = l.Rows[len(l.Rows)-50:]
		}
		return map[string]interface{}{"events": l.Rows, "checks": checks, "pending": pending}, nil
	case "create", "update":
		if action == "update" {
			expected, _ := args["expected_revision"].(string)
			if expected == "" || expected != conditionView(job.Schedule)["revision"] {
				return nil, errors.New("watcher changed or expected_revision is missing; list before updating")
			}
		}
		trigger, ok := args["trigger"].(map[string]interface{})
		if !ok {
			return nil, errors.New("trigger definition is required")
		}
		doc := map[string]interface{}{}
		if action == "update" {
			doc = conditionView(job.Schedule)
		}
		for k, v := range trigger {
			switch k {
			case "name", "enabled", "cron_expression", "timezone", "script", "secrets", "target":
				doc[k] = v
			default:
				return nil, fmt.Errorf("unknown watcher setting %q", k)
			}
		}
		b, _ := json.Marshal(doc)
		var fields struct {
			Name     string `json:"name"`
			Enabled  *bool  `json:"enabled"`
			Cron     string `json:"cron_expression"`
			Timezone string `json:"timezone"`
			conditiontrigger.Config
		}
		if err := json.Unmarshal(b, &fields); err != nil {
			return nil, err
		}
		cfg := fields.Config
		// Pin saved source before validation. A caller cannot choose a hash to run
		// content that was never read under the source's ownership check.
		if !strings.HasPrefix(cfg.Script, "code/triggers/") || path.Clean(cfg.Script) != cfg.Script {
			return nil, errors.New("script must be under code/triggers")
		}
		source, found, err := c.s.readFile(ctx, path.Join(base.WorkspacePath, cfg.Script))
		if err != nil || !found {
			return nil, errors.New("save the Python script before creating the watcher")
		}
		cfg.SHA256 = conditiontrigger.Hash(source)
		if err := c.authorize(ctx, base, cfg); err != nil {
			return nil, err
		}
		if err := c.validateTarget(ctx, base, cfg); err != nil {
			return nil, err
		}
		if cfg.Target.Kind != "agent" {
			t, err := c.resolveTarget(ctx, base, cfg.Target.Name)
			if err != nil {
				return nil, err
			}
			cfg.Target.Name = t.Path
		}
		enabled := true
		if fields.Enabled != nil {
			enabled = *fields.Enabled
		}
		schedule := productschedule.Schedule{Name: strings.TrimSpace(fields.Name), CronExpression: fields.Cron, Timezone: fields.Timezone, Enabled: enabled, Condition: &cfg}
		if action == "create" {
			requestID, _ := args["request_id"].(string)
			if strings.TrimSpace(requestID) == "" || len(requestID) > 128 {
				return nil, errors.New("create needs a stable request_id (1–128 bytes)")
			}
			schedule.ID = "cond-" + conditiontrigger.Hash(owner + "\x00" + profile + "\x00" + project + "\x00" + requestID)[:32]
		} else {
			schedule.ID = job.Schedule.ID
			schedule.CreatedAt = job.Schedule.CreatedAt
		}
		if err := productschedule.Validate(schedule); err != nil {
			return nil, err
		}
		parsed, err := cron.ParseStandard(schedule.CronExpression)
		if err != nil {
			return nil, err
		}
		at := time.Now()
		for i := 0; i < 5; i++ {
			next := parsed.Next(at)
			after := parsed.Next(next)
			if after.Sub(next) < 5*time.Minute {
				return nil, errors.New("watcher checks must be at least 5 minutes apart")
			}
			at = next
		}
		if action == "create" {
			if prior, lookupErr := c.s.Job(ctx, owner, projectScheduleJobID(profile, project, schedule.ID)); lookupErr == nil {
				if prior.Schedule.Condition == nil || prior.Schedule.Condition.Revision() != cfg.Revision() || prior.Schedule.Name != schedule.Name || prior.Schedule.CronExpression != schedule.CronExpression || prior.Schedule.Timezone != schedule.Timezone || prior.Schedule.Enabled != schedule.Enabled {
					return nil, errors.New("request_id already belongs to a different watcher")
				}
				return conditionView(prior.Schedule), nil
			} else if !strings.HasPrefix(lookupErr.Error(), "product schedule ") || !strings.HasSuffix(lookupErr.Error(), "not found for this user") {
				return nil, lookupErr
			}
			job, err = c.s.CreateProjectSchedule(ctx, owner, profile, project, schedule)
		} else {
			job, err = c.s.UpdateProjectSchedule(ctx, owner, job.ID(), func(s *productschedule.Schedule) { *s = schedule })
			if err == nil {
				err = c.s.updateStateByKey(ctx, owner, scheduleStateKey(job), func(st *productScheduleUserState) { st.Enabled = nil; st.ConsecutiveFailures = 0 })
			}
		}
		if err != nil {
			return nil, err
		}
		return conditionView(job.Schedule), nil
	default:
		return nil, errors.New("action must be list, create, update, delete, history or check")
	}
}

func (s *ProductScheduleService) conditionHTTP(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "sign in first", 401)
		return
	}
	ctx := internalBotRequestContext(r.Context(), claims.UserID)
	args := map[string]interface{}{}
	if r.Method != http.MethodGet {
		r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, "invalid watcher request", 400)
			return
		}
	}
	project := r.URL.Query().Get("project_id")
	if v, ok := args["project_id"].(string); ok {
		project = v
	}
	if r.Method == http.MethodGet {
		args["action"] = "list"
		if id := mux.Vars(r)["id"]; id != "" {
			args["action"], args["id"] = "history", id
		}
	}
	out, err := s.conditionService().manage(ctx, claims.UserID, "code", project, args)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
