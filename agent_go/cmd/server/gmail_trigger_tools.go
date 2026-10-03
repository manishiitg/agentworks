package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// Gmail is a trigger source, not another public per-workflow webhook.
type WorkflowGmailTriggerConfig struct {
	Filters      *gmailinbound.Filters `json:"filters,omitempty"`
	ConnectionID string                `json:"connection_id"`
	Address      string                `json:"address"`
	Reply        bool                  `json:"reply"`
}

func (s WorkflowSchedule) IsGmailTrigger() bool {
	return s.ScheduleType == "webhook" && strings.EqualFold(strings.TrimSpace(s.Kind), "gmail")
}

type gmailTriggerInput struct {
	Filters         *gmailinbound.Filters `json:"filters"`
	WorkspacePath   string                `json:"workspace_path"`
	ConnectionID    string                `json:"connection_id"`
	Name            string                `json:"name"`
	Enabled         bool                  `json:"enabled"`
	Reply           bool                  `json:"reply"`
	RouteSelections map[string]string     `json:"route_selections"`
	GroupNames      []string              `json:"group_names"`
	StepID          string                `json:"step_id"`
}

func (api *StreamingAPI) saveGmailWorkflowTrigger(ctx context.Context, route gmailinbound.Route, mailboxEmail ...string) error {
	filters, err := gmailinbound.NormalizeFilters(route.Filters)
	if err != nil {
		return err
	}
	route.Filters = filters
	if !route.WorkflowTrigger {
		if len(mailboxEmail) > 0 {
			return api.gmailInbound.Store.SaveRoute(ctx, route, mailboxEmail[0])
		}
		return nil
	}
	workflowWebhookConfigMu.Lock()
	defer workflowWebhookConfigMu.Unlock()
	manifest, found, err := ReadWorkflowManifest(ctx, route.WorkspacePath)
	if err != nil || !found || manifest.ID != route.ProjectID {
		return fmt.Errorf("workflow unavailable")
	}
	previousSchedules := append([]WorkflowSchedule(nil), manifest.Schedules...)
	groups := normalizeScheduleGroupNames(route.GroupNames)
	if route.Enabled {
		if err := directWebhookPreflight(manifest); err != nil {
			return err
		}
		groups, err = validateScheduleGroupNamesForWorkspace(ctx, route.WorkspacePath, groups)
		if err != nil {
			return err
		}
		if err := validateWebhookTarget(ctx, route.WorkspacePath, route.StepID, route.RouteSelections); err != nil {
			return err
		}
	}
	name := route.Name
	if name == "" {
		name = "Incoming Gmail"
	}
	sched := WorkflowSchedule{ID: route.ID, Name: name, ScheduleType: "webhook", Kind: "gmail", Timezone: "UTC", Enabled: route.Enabled, GroupNames: groups, RouteSelections: route.RouteSelections, Mode: "workshop", WorkshopMode: "run", CollisionPolicy: "skip", PulseMode: "off", PulseModeReason: "Gmail deliveries execute only the saved route.", Webhook: &WorkflowWebhookConfig{StepID: route.StepID, InputMode: "raw"}, Gmail: &WorkflowGmailTriggerConfig{ConnectionID: route.ConnectionID, Address: route.Address, Reply: route.Reply, Filters: route.Filters}}
	index := -1
	for i, existing := range manifest.Schedules {
		if existing.ID == route.ID {
			if !existing.IsGmailTrigger() {
				return fmt.Errorf("trigger ID belongs to a different source")
			}
			index = i
		}
	}
	if index >= 0 {
		manifest.Schedules[index] = sched
	} else {
		manifest.Schedules = append(manifest.Schedules, sched)
	}
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	if err := WriteWorkflowManifest(ctx, route.WorkspacePath, manifest); err != nil {
		return err
	}
	if len(mailboxEmail) > 0 {
		if err := api.gmailInbound.Store.SaveRoute(ctx, route, mailboxEmail[0]); err != nil {
			manifest.Schedules = previousSchedules
			if rollbackErr := WriteWorkflowManifest(context.WithoutCancel(ctx), route.WorkspacePath, manifest); rollbackErr != nil {
				return fmt.Errorf("email state failed: %w; manifest rollback failed: %w", err, rollbackErr)
			}
			return fmt.Errorf("cannot save Gmail trigger: %w", err)
		}
	}
	if api.scheduler != nil {
		api.scheduler.InvalidateWorkflowManifestCache()
	}
	return nil
}

func (api *StreamingAPI) gmailWorkflowTrigger(ctx context.Context, route gmailinbound.Route) (*WorkflowManifest, WorkflowSchedule, error) {
	manifest, found, err := ReadWorkflowManifest(ctx, route.WorkspacePath)
	if err != nil || !found || manifest.ID != route.ProjectID {
		return nil, WorkflowSchedule{}, fmt.Errorf("workflow unavailable")
	}
	for _, sched := range manifest.Schedules {
		if sched.ID != route.ID {
			continue
		}
		if !sched.IsGmailTrigger() || !sched.Enabled || sched.Gmail == nil || sched.Gmail.ConnectionID != route.ConnectionID || sched.Gmail.Address != route.Address {
			return nil, WorkflowSchedule{}, fmt.Errorf("Gmail trigger is disabled or its mailbox binding changed")
		}
		if err := validateWebhookSchedule(sched); err != nil {
			return nil, WorkflowSchedule{}, err
		}
		if err := validateWebhookTarget(ctx, route.WorkspacePath, sched.Webhook.StepID, sched.RouteSelections); err != nil {
			return nil, WorkflowSchedule{}, err
		}
		if _, err := validateScheduleGroupNamesForWorkspace(ctx, route.WorkspacePath, sched.GroupNames); err != nil {
			return nil, WorkflowSchedule{}, err
		}
		return manifest, sched, nil
	}
	return nil, WorkflowSchedule{}, fmt.Errorf("Gmail trigger no longer exists; ask Builder to configure it")
}

// Reuse the authenticated trigger pipeline, its durable run IDs and exact route
// execution. Email contents are payload data, never configuration or a plan.
func (api *StreamingAPI) dispatchGmailWorkflowTrigger(ctx context.Context, d *gmailinbound.Delivery) error {
	if api.scheduler == nil {
		return fmt.Errorf("workflow scheduler unavailable")
	}
	userCtx := internalBotRequestContext(ctx, d.Route.OwnerID)
	manifest, sched, err := api.gmailWorkflowTrigger(userCtx, d.Route)
	if err != nil {
		return err
	}
	body, err := json.Marshal(d.Message)
	if err != nil {
		return err
	}
	runID := webhookDeliveryRunID(manifest.ID, sched.ID, d.ID)
	// Persist an inspectable run reference before accepting external effects.
	d.SessionID = runID
	if err := api.gmailInbound.Store.Finish(ctx, *d, "running", nil); err != nil {
		return err
	}
	receiver := webhookReceiver{start: api.scheduler.triggerSavedSchedule, existing: api.scheduler.existingWebhookRun}
	result, err := receiver.deliver(userCtx, manifest.ID, d.Route.WorkspacePath, sched, d.ID, "gmail.message", body)
	if err != nil {
		return err
	}
	return api.waitGmailWorkflowRun(ctx, d, result.RunID)
}

func (api *StreamingAPI) waitGmailWorkflowRun(ctx context.Context, d *gmailinbound.Delivery, runID string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		run, err := api.scheduler.existingWebhookRun(ctx, runID)
		if err != nil {
			return err
		}
		if schedulerstate.IsTerminal(run.State) {
			if run.ActiveSessionID != "" {
				d.SessionID = run.ActiveSessionID
			}
			runs, err := ReadScheduleRuns(ctx, d.Route.WorkspacePath)
			if err != nil {
				return err
			}
			for _, entry := range runs {
				if entry.ID == runID {
					d.Response = entry.FinalResponse
					if entry.SessionID != "" {
						d.SessionID = entry.SessionID
					}
					break
				}
			}
			if run.State != schedulerstate.StateCompleted {
				return fmt.Errorf("workflow Gmail run %s: %s", run.State, run.ErrorMessage)
			}
			if d.Response == "" {
				d.Response = "The workflow completed. Run: " + runID
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (api *StreamingAPI) registerGmailTriggerTools(reg definitionToolRegistrar, session, workspace string) error {
	if err := reg.RegisterCustomTool("get_gmail_trigger", "Inspect this target's incoming Gmail trigger, receiving address, saved routing and filters, readiness, delivery activity and setup options (eligible OAuth client names and account-connect permission). Read before and after changing it. The right pane is read-only. If configured=false, an operator must enable Pub/Sub first.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		return api.gmailTriggerToolRequest(ctx, session, workspace, nil)
	}, "gmail_connection_management"); err != nil {
		return err
	}
	return reg.RegisterCustomTool("manage_gmail_trigger", "Connect, configure or disable this target's unique incoming-email trigger as its interactive owner. Read get_gmail_trigger and list_gmail_connections first. Use action=connect to create an account or request read consent on connection_id and return a Google consent link; the human must authorize, then inspect again before configure. Existing account-management permissions apply. client_name is only for connect; omit when one deployed OAuth client is eligible. For workflows discover saved route IDs/groups with manage_workflow_webhook(action=list), then configure route_selections and group_names (or step_id). Optional filters: sender_allowlist accepts exact email addresses or @domains; any listed sender matches (OR), and an omitted/empty list keeps owner-only access. Only widen senders when the interactive owner requests it. Existing subject_contains/body_contains require every keyword; subject_contains_any/body_contains_any require any listed keyword. Condition groups use AND; literal matching ignores case. allow_automatic explicitly permits notifications from the allowlist; auto-replies, bounces, spam and trash remain blocked. Omitted filters are preserved; filters={} clears them; a supplied filters object replaces the whole filter set. New threads only rejects replies and threads already accepted here. No filters by default. Crew/Code email starts isolated project chats. reply/enabled default true on first setup. Disable preserves the address. No arbitrary target, wildcard senders, UI setup forms or direct gog watch changes. Return address, filter summary and verified readiness; saving alone does not prove live delivery.", map[string]interface{}{
		"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]interface{}{
			"action":        map[string]interface{}{"type": "string", "enum": []string{"connect", "configure", "disable"}},
			"client_name":   map[string]interface{}{"type": "string", "description": "Only for connect: exact configured OAuth client; do not invent names."},
			"connection_id": map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"},
			"enabled": map[string]interface{}{"type": "boolean"}, "reply": map[string]interface{}{"type": "boolean"},
			"route_selections": map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
			"group_names":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"step_id":          map[string]interface{}{"type": "string"},
			"filters": map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
				"sender_allowlist":     map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}, "description": "Any exact email address or exact @domain matches. No wildcard/display name. Omitted or empty keeps owner-only access. Only set when the owner explicitly requests these senders."},
				"subject_contains_any": map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}, "description": "At least one subject phrase must match (OR)."},
				"body_contains_any":    map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}, "description": "At least one body phrase must match (OR)."},
				"allow_automatic":      map[string]interface{}{"type": "boolean", "description": "Explicitly allow automated notifications from sender_allowlist (required). Never permits auto-replies, bounces, spam or trash."},
				"subject_contains":     map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}},
				"body_contains":        map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}},
				"has_attachments":      map[string]interface{}{"type": "boolean", "description": "True requires attachments; false requires no attachments; omit for either."},
				"new_threads_only":     map[string]interface{}{"type": "boolean"},
			}},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		return api.gmailTriggerToolRequest(ctx, session, workspace, args)
	}, "gmail_connection_management")
}

func (api *StreamingAPI) gmailTriggerToolRequest(ctx context.Context, session, workspace string, args map[string]interface{}) (string, error) {
	claims := GetUserFromContext(ctx)
	active, _ := api.getActiveSession(session)
	policy := resolveWorkflowChatPolicy(session, QueryRequest{}, active, false)
	if claims == nil || claims.Provider == "bot_route" || claims.AccessToken != nil || claims.ExternalBuilderOperationID != "" || claims.BotRouteGrant != "" || policy.Origin != "interactive" || active != nil && active.TriggeredBy == "email" {
		return "", fmt.Errorf("Gmail trigger management requires an interactive owner conversation")
	}
	config, err := readGmailInboundConfig()
	if err != nil {
		config = gmailInboundConfig{}
	}
	get := httptest.NewRequest(http.MethodGet, "/api/gmail-inbound/route?workspace_path="+url.QueryEscape(workspace), nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	api.gmailInboundRoute(config)(rec, get)
	if rec.Code != 200 {
		return "", fmt.Errorf("%s", strings.TrimSpace(rec.Body.String()))
	}
	if args == nil {
		return rec.Body.String(), nil
	}
	action, _ := args["action"].(string)
	if action == "connect" {
		if api.gmailInbound == nil {
			return "", fmt.Errorf("an administrator must configure Gmail Pub/Sub first")
		}
		return api.connectGmailTriggerAccount(ctx, workspace, config, args)
	}
	if action != "configure" && action != "disable" {
		return "", fmt.Errorf("action must be connect, configure or disable")
	}
	var current struct {
		Route *gmailinbound.Route `json:"route"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &current); err != nil {
		return "", err
	}
	input := gmailTriggerInput{WorkspacePath: workspace, Name: "Incoming Gmail", Enabled: true, Reply: true}
	if current.Route != nil {
		r := current.Route
		input = gmailTriggerInput{WorkspacePath: workspace, ConnectionID: r.ConnectionID, Name: r.Name, Enabled: r.Enabled, Reply: r.Reply, RouteSelections: r.RouteSelections, GroupNames: r.GroupNames, StepID: r.StepID, Filters: r.Filters}
	}
	if action == "disable" && current.Route == nil {
		return "", fmt.Errorf("no Gmail trigger exists")
	}
	// Merge only documented settings; target identity is never agent-selected.
	values := map[string]interface{}{}
	b, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(b, &values); err != nil {
		return "", err
	}
	for key, value := range args {
		if key == "action" {
			continue
		}
		if _, ok := values[key]; !ok || key == "workspace_path" {
			return "", fmt.Errorf("unsupported Gmail trigger setting %q", key)
		}
		values[key] = value
	}
	if current.Route == nil && !isProjectWorkspacePath(workspace) {
		if _, ok := args["group_names"]; !ok {
			return "", fmt.Errorf("discover and supply workflow group_names before creating a Gmail trigger")
		}
		if _, routes := args["route_selections"]; !routes {
			if _, step := args["step_id"]; !step {
				return "", fmt.Errorf("supply saved route_selections ({} for the full workflow) or step_id")
			}
		}
	}
	if action == "disable" {
		values["enabled"] = false
	}
	body, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	post := httptest.NewRequest(http.MethodPost, "/api/gmail-inbound/route", bytes.NewReader(body)).WithContext(ctx)
	rec = httptest.NewRecorder()
	api.gmailInboundRoute(config)(rec, post)
	if rec.Code != 200 {
		return "", fmt.Errorf("%s", strings.TrimSpace(rec.Body.String()))
	}
	if event, err := workspaceViewPresentation("schedules", workspace); err == nil {
		api.emitAgentProfileEvent(session, event)
	}
	return rec.Body.String(), nil
}
