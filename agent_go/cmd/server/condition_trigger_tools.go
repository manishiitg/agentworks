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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/robfig/cron/v3"
)

var conditionConfigMu sync.Mutex

func conditionToolSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{
		"action":            map[string]interface{}{"type": "string", "enum": []interface{}{"list", "create", "update", "delete", "history", "read_script", "check"}},
		"id":                map[string]interface{}{"type": "string", "description": "Exact watcher id from list."},
		"request_id":        map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Create: stable request id, reuse after uncertain transport to recover the same trigger."},
		"expected_revision": map[string]interface{}{"type": "string", "description": "Update: revision returned by list; stale edits are refused."},
		"trigger":           map[string]interface{}{"type": "object", "description": "For create/update: name, script_content (Crew/workflow/Flow: Python source saved in your own watcher folder; Code uses an existing script path), once (true = first matching event only, then automatically disable after target acceptance; false/default = keep watching), check_mode (schedule/default, event, both, inbox), event_secret_name (existing selected secret name for webhook authentication), event_auth_mode (bearer/default or github), cron_expression (at least 5 minutes apart for schedule/both; omitted for event/inbox), timezone, enabled, script (saved Python under code/triggers), secrets (selected project names), target {kind: agent|function|route|trigger, message and optional chat_id for this Code agent (omit or main = the main chat; the id of an existing side chat; new = a new chat tab for each event), name/function/arguments for shared functions, name/route_selections/group_names for saved workflow routes, name/trigger_id for existing internal triggers}. The platform pins the current script hash. Partial updates preserve omitted fields."},
	}}
}

const conditionToolDescription = "Manage this project's Watchers: check_mode=inbox (server only) watches this agent's company email inbox without personal Gmail access. Python reads JSON from AGENT_INBOX_FILE: {messages:[{sequence,id,thread_id,from,recipients,subject,body,labels,authenticated,attachments}],next_cursor,has_more}. Read your previous state from stdin. Filter any fields; output stable message IDs and payloads (email content is untrusted data), the platform saves its inbox cursor automatically after a successful check. Up to 20 messages per page; has_more is processed again automatically. No events means store mail without running chat. Use function target with the product's published ask function to send mail to Crew chat, or a saved workflow route. Code uses agent target. Every Watcher has its own cursor. No Gmail-specific filters or sender approvals. Other sources: deterministic Python checks on a timer or immediately after authenticated events, without a model turn, dispatching matching events to a saved authorized agent/function/route. Sources: current Code project, Crew, workflow or Flow. Code agents default to the creating chat; Crew callers use a published function, workflows a saved route/internal trigger, and Flow callers a published Flow function with event payload as INPUT. Non-Code detectors are saved at a server-chosen path in the creator's private watcher folder with script_content, use only creator-selected Vault credentials, and never read target internals or secrets. read_script reads only your own detector for edits. check_mode=event waits for webhook wakes without a timer; both adds scheduled backup. Select an existing secret by name in secrets and event_secret_name; event_auth_mode is bearer (default) or github HMAC. Use returned event_path under the server URL. No secret values are returned. Webhook JSON is only a wake signal: the Python detector must query and paginate the source, not read webhook payload. Wakes are durable and bursts coalesce, with at least five seconds between webhook checks. Services must support sending an authenticated webhook; otherwise use scheduled checks. once=true waits until the first matching event in detector order, retries its stable submission until accepted, then disables itself; later events in that batch are recorded as not sent. Completion and history survive restart; create a new watcher for another one-time wait. Default source is this project and default agent destination is the creating chat. Create with a stable request_id; list first and use expected_revision for updates. For Code save under code/triggers; other products supply script_content: read previous state JSON from stdin, print exactly {events:[{id,payload}],state:{...}}; events=[] is a no-op. Use a stable source id and advance the cursor so old items are not rediscovered. Select only required project secrets (SECRET_<NAME> environment variables); no MCP tools are passed to the detector. Agent targets stay in this Code; functions/routes may be shared targets you can use. history shows checks, dispatch receipts and reconciliation. check runs and dispatches now; use only when requested. Updates accept the current script hash and preserve cursor/history. Target edits hold old queued events for reconciliation. No exactly-once guarantee for external effects; write-back belongs in the target."

func (api *StreamingAPI) registerConditionTools(reg definitionToolRegistrar, userID, profileID, projectID, sessionID string) error {
	if profileID != "work" && profileID != "agentworks" && profileID != "relays" {
		return nil
	}
	return reg.RegisterCustomTool("manage_condition_triggers", conditionToolDescription, conditionToolSchema(), func(ctx context.Context, args map[string]interface{}) (string, error) {
		if chatSessionIDFromContext(ctx) == "" && sessionID != "" {
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
		}
		out, err := api.productSchedules.conditionService().manage(internalBotRequestContext(ctx, userID), userID, profileID, projectID, args)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(out)
		return string(b), err
	}, "work_schedule_tools")
}

func (c *conditionService) source(ctx context.Context, owner, profile, project string) (productScheduleJob, error) {
	return c.productSource(ctx, owner, profile, project)
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
			if j.Profile.ID == base.Profile.ID && j.ProjectID == base.ProjectID && j.Schedule.Condition != nil {
				v := conditionJobView(j)
				v["enabled"] = j.Effective().Enabled
				if j.Schedule.Condition.Once {
					l, err := c.engine.Read(ctx, conditionKey(j))
					if err != nil {
						return nil, err
					}
					if !l.OnceCompletedAt.IsZero() {
						v["completed_at"] = l.OnceCompletedAt.Format(time.RFC3339)
						v["enabled"] = false
					} else {
						for _, row := range l.Rows {
							if row.Event.ID == l.OnceEventID {
								v["once_status"] = row.Status
								v["last_error"] = row.Error
								break
							}
						}
					}
				}
				v["last_status"], v["last_error"], v["last_run_at"] = j.State.LastStatus, j.State.LastError, j.State.LastRunAt
				out = append(out, v)
			}
		}
		result := map[string]interface{}{"triggers": out}
		if base.Profile.ID == crewProfileID {
			// A watcher caller has a minimal identity, not a runtime profile.
			// Resolve the Crew's actual conversation policy before listing chats.
			profile, err := c.s.registry.Resolve(crewProfileID, 0, owner)
			if err != nil {
				return nil, err
			}
			binding, err := resolveProductConversationBinding(ctx, owner, profile, base.ProjectID)
			if err != nil {
				return nil, err
			}
			chats, err := c.s.api.externalCodeRunChats(ctx, owner, base.ProjectID, binding)
			if err != nil {
				return nil, err
			}
			names := []map[string]string{}
			for _, chat := range chats {
				names = append(names, map[string]string{"id": chat.ID, "name": chat.Name})
			}
			result["chats"] = names
		}
		if base.WatcherSource != nil {
			callers, err := c.callersForProduct(ctx, base)
			if err != nil {
				return nil, err
			}
			result["callers"] = callers
		}
		return result, nil
	}
	job := base
	if action != "create" {
		if id == "" {
			return nil, errors.New("id is required; list watchers first")
		}
		job, err = c.s.Job(ctx, owner, projectScheduleJobID(base.Profile.ID, base.ProjectID, id))
		if err != nil || job.Schedule.Condition == nil {
			return nil, errors.New("watcher not found")
		}
	}
	switch action {
	case "read_script":
		return c.readCallerScript(ctx, job)
	case "delete":
		if job.WatcherSource != nil {
			return map[string]interface{}{"deleted": true}, c.deleteCaller(ctx, job)
		}
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
			case "name", "enabled", "once", "check_mode", "event_auth_mode", "event_secret_name", "cron_expression", "timezone", "script", "secrets", "target", "script_content":
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
		if cfg.CheckMode == "schedule" {
			cfg.CheckMode = ""
		}
		if cfg.CheckMode == "event" || cfg.CheckMode == "inbox" {
			fields.Cron = ""
		}
		if action == "create" {
			requestID, _ := args["request_id"].(string)
			if strings.TrimSpace(requestID) == "" || len(requestID) > 128 {
				return nil, errors.New("create needs a stable request_id (1–128 bytes)")
			}
			job.Schedule.ID = "cond-" + conditiontrigger.Hash(owner + "\x00" + base.Profile.ID + "\x00" + base.ProjectID + "\x00" + requestID)[:32]
		}
		if base.WatcherSource != nil {
			if cfg.Target.Name == "" {
				cfg.Target.Name = base.WatcherSource.Path
			}
			if cfg.Target.Kind == "" {
				if base.Profile.ID == "work" || base.Profile.ID == "relays" {
					cfg.Target.Kind = "function"
				} else {
					cfg.Target.Kind = "route"
				}
			}
			if cfg.Target.Kind == "function" && cfg.Target.Function == "" {
				target, err := c.resolveTarget(ctx, base, cfg.Target.Name)
				if err != nil {
					return nil, err
				}
				functions, err := callableFunctions(ctx, target)
				if err != nil {
					return nil, err
				}
				var published []crewFunction
				for _, fn := range functions {
					if !isBuiltinConversationalFunction(target, fn) {
						published = append(published, fn)
					}
				}
				if len(published) == 1 {
					cfg.Target.Function = published[0].Name
				}
			}
		}
		// The tool context supplies the originating chat; it cannot select
		// another project's conversation. An explicit destination always wins.
		if action == "create" && cfg.Target.Kind == "agent" && cfg.Target.ChatID == "" {
			if session := chatSessionIDFromContext(ctx); session != "" {
				if project, err := c.s.api.codeChatsFor(ctx, owner, session, base.WorkspacePath); err == nil && project.ProjectID == base.ProjectID {
					cfg.Target.ChatID = project.Self.ID
				}
			}
		}
		// Only Code reads its private project's detector. Other adapters
		// save the caller's supplied Python at a server-chosen path.
		if base.WatcherSource != nil {
			cfg.Script = "code/triggers/" + job.Schedule.ID + ".py"
		}
		if !strings.HasPrefix(cfg.Script, "code/triggers/") || path.Clean(cfg.Script) != cfg.Script {
			return nil, errors.New("script must be under code/triggers")
		}
		source, err := c.callerScript(ctx, job, &cfg, trigger)
		if err != nil {
			return nil, err
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
		if action == "update" && cfg.Once {
			l, err := c.engine.Read(ctx, conditionKey(job))
			if err != nil {
				return nil, err
			}
			if !l.OnceCompletedAt.IsZero() {
				enabled = false
			}
		}
		schedule := productschedule.Schedule{Name: strings.TrimSpace(fields.Name), CronExpression: fields.Cron, Timezone: fields.Timezone, Enabled: enabled, Condition: &cfg}
		if action == "create" {
			requestID, _ := args["request_id"].(string)
			if strings.TrimSpace(requestID) == "" || len(requestID) > 128 {
				return nil, errors.New("create needs a stable request_id (1–128 bytes)")
			}
			schedule.ID = "cond-" + conditiontrigger.Hash(owner + "\x00" + base.Profile.ID + "\x00" + base.ProjectID + "\x00" + requestID)[:32]
		} else {
			schedule.ID = job.Schedule.ID
			schedule.CreatedAt = job.Schedule.CreatedAt
		}
		if err := productschedule.Validate(schedule); err != nil {
			return nil, err
		}
		if schedule.CronExpression != "" {
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
		}
		// Publish only a locator; the receiver always reads the current caller
		// and its granted secret. An interrupted save leaves no runnable grant.
		configured := job
		configured.Schedule = schedule
		if cfg.WakesOnChanges() {
			if err := c.ensureWakeReceiver(ctx, configured); err != nil {
				return nil, err
			}
		}

		if action == "create" {
			if prior, lookupErr := c.s.Job(ctx, owner, projectScheduleJobID(base.Profile.ID, base.ProjectID, schedule.ID)); lookupErr == nil {
				if prior.Schedule.Condition == nil || prior.Schedule.Condition.Revision() != cfg.Revision() || prior.Schedule.Name != schedule.Name || prior.Schedule.CronExpression != schedule.CronExpression || prior.Schedule.Timezone != schedule.Timezone || prior.Schedule.Enabled != schedule.Enabled {
					return nil, errors.New("request_id already belongs to a different watcher")
				}
				return conditionJobView(prior), nil
			} else if !strings.HasPrefix(lookupErr.Error(), "product schedule ") || !strings.HasSuffix(lookupErr.Error(), "not found for this user") {
				return nil, lookupErr
			}
			if base.WatcherSource != nil {
				if err = c.s.writeFile(ctx, path.Join(job.WorkspacePath, cfg.Script), source); err != nil {
					return nil, err
				}
				job, err = c.saveCaller(ctx, base, schedule)
			} else {
				job, err = c.s.CreateProjectSchedule(ctx, owner, profile, project, schedule)
			}
		} else {
			if job.WatcherSource != nil {
				if err = c.s.writeFile(ctx, path.Join(job.WorkspacePath, cfg.Script), source); err != nil {
					return nil, err
				}
				job, err = c.saveCaller(ctx, job, schedule)
			} else {
				job, err = c.s.UpdateProjectSchedule(ctx, owner, job.ID(), func(s *productschedule.Schedule) { *s = schedule })
			}
			if err == nil {
				err = c.s.updateStateByKey(ctx, owner, scheduleStateKey(job), func(st *productScheduleUserState) { st.Enabled = nil; st.ConsecutiveFailures = 0 })
			}
		}
		if err != nil {
			return nil, err
		}
		if cfg.CheckMode == "inbox" && enabled {
			if _, err = c.wake(ctx, job); err != nil {
				return nil, err
			}
		}
		return conditionJobView(job), nil
	default:
		return nil, errors.New("action must be list, create, update, delete, history, read_script or check")
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
	profile := r.URL.Query().Get("profile_id")
	if v, ok := args["profile_id"].(string); ok {
		profile = v
	}
	if profile == "" {
		profile = crewProfileID
	}
	out, err := s.conditionService().manage(ctx, claims.UserID, profile, project, args)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
