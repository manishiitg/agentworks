package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// Gmail is a trigger source, not another public per-workflow webhook.
type WorkflowGmailTriggerConfig struct {
	Rules        []gmailinbound.Rule   `json:"rules,omitempty"`
	Filters      *gmailinbound.Filters `json:"filters,omitempty"`
	ConnectionID string                `json:"connection_id"`
	Address      string                `json:"address"`
	Reply        bool                  `json:"reply"`
}

func (s WorkflowSchedule) IsGmailTrigger() bool {
	return s.ScheduleType == "webhook" && strings.EqualFold(strings.TrimSpace(s.Kind), "gmail")
}

type gmailTriggerInput struct {
	Rules           []gmailinbound.Rule   `json:"rules"`
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
	route.Rules, err = gmailinbound.NormalizeRules(route.Rules, route.WorkflowTrigger)
	if err != nil {
		return err
	}
	if len(route.Rules) > 0 && (route.RouteSelections != nil || len(route.GroupNames) > 0 || route.StepID != "") {
		return fmt.Errorf("put workflow bindings inside each email rule")
	}
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
	if len(route.Rules) > 0 {
		groups = nil
		for i := range route.Rules {
			rule := &route.Rules[i]
			rule.GroupNames = normalizeScheduleGroupNames(rule.GroupNames)
			if route.Enabled {
				rule.GroupNames, err = validateScheduleGroupNamesForWorkspace(ctx, route.WorkspacePath, rule.GroupNames)
				if err != nil {
					return fmt.Errorf("email rule %s: %w", rule.ID, err)
				}
				if err := validateWebhookTarget(ctx, route.WorkspacePath, rule.StepID, rule.RouteSelections); err != nil {
					return fmt.Errorf("email rule %s: %w", rule.ID, err)
				}
			}
			groups = append(groups, rule.GroupNames...)
		}
		groups = normalizeScheduleGroupNames(groups)
	}
	if route.Enabled {
		if err := directWebhookPreflight(manifest); err != nil {
			return err
		}
		if len(route.Rules) == 0 {
			groups, err = validateScheduleGroupNamesForWorkspace(ctx, route.WorkspacePath, groups)
			if err != nil {
				return err
			}
			if err := validateWebhookTarget(ctx, route.WorkspacePath, route.StepID, route.RouteSelections); err != nil {
				return err
			}
		}
	}
	name := route.Name
	if name == "" {
		name = "Incoming Gmail"
	}
	sched := WorkflowSchedule{ID: route.ID, Name: name, ScheduleType: "webhook", Kind: "gmail", Timezone: "UTC", Enabled: route.Enabled, GroupNames: groups, RouteSelections: route.RouteSelections, Mode: "workshop", WorkshopMode: "run", CollisionPolicy: "skip", PulseMode: "off", PulseModeReason: "Gmail deliveries execute only the saved route.", Webhook: &WorkflowWebhookConfig{StepID: route.StepID, InputMode: "raw"}, Gmail: &WorkflowGmailTriggerConfig{ConnectionID: route.ConnectionID, Address: route.Address, Reply: route.Reply, Filters: route.Filters, Rules: route.Rules}}
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
		if len(route.Rules) > 0 || len(sched.Gmail.Rules) > 0 {
			if !reflect.DeepEqual(route.Rules, sched.Gmail.Rules) || !reflect.DeepEqual(route.Filters, sched.Gmail.Filters) {
				return nil, WorkflowSchedule{}, fmt.Errorf("Gmail rule configuration changed; ask Builder to configure it")
			}
			if route.SelectedRuleID != "" {
				if _, err := route.SelectedRule(); err != nil {
					return nil, WorkflowSchedule{}, err
				}
				sched, err = gmailRuleSchedule(sched, route.SelectedRuleID)
				if err != nil {
					return nil, WorkflowSchedule{}, err
				}
			} else {
				// Inspection validates every active binding, but never chooses a rule.
				for _, rule := range sched.Gmail.Rules {
					if rule.IsEnabled() {
						if err := validateWebhookTarget(ctx, route.WorkspacePath, rule.StepID, rule.RouteSelections); err != nil {
							return nil, WorkflowSchedule{}, err
						}
						if _, err := validateScheduleGroupNamesForWorkspace(ctx, route.WorkspacePath, rule.GroupNames); err != nil {
							return nil, WorkflowSchedule{}, err
						}
					}
				}
				return manifest, sched, nil
			}
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
// Resolve only a server-selected rule ID against the current saved manifest.
// Neither email JSON nor webhook payload mappings can supply this selection.
func gmailRuleSchedule(sched WorkflowSchedule, ruleID string) (WorkflowSchedule, error) {
	if !sched.IsGmailTrigger() || sched.Gmail == nil || sched.Webhook == nil {
		return WorkflowSchedule{}, fmt.Errorf("target is not a Gmail trigger")
	}
	if len(sched.Gmail.Rules) == 0 && ruleID == "" {
		return sched, nil
	}
	for _, rule := range sched.Gmail.Rules {
		if rule.ID == ruleID && rule.IsEnabled() {
			sched.RouteSelections, sched.GroupNames = rule.RouteSelections, rule.GroupNames
			sched.Name += " · " + rule.Name
			webhook := *sched.Webhook
			webhook.StepID = rule.StepID
			sched.Webhook = &webhook
			return sched, nil
		}
	}
	return WorkflowSchedule{}, fmt.Errorf("selected Gmail rule is missing or disabled")
}

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
	receiver := webhookReceiver{start: func(workspacePath, scheduleID, originSessionID string, input *WorkflowWebhookDelivery) (string, error) {
		input.gmailRuleID = d.RuleID
		return api.scheduler.triggerSavedSchedule(workspacePath, scheduleID, originSessionID, input)
	}, existing: api.scheduler.existingWebhookRun}
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
	if err := api.registerGmailSetupTool(reg, session); err != nil {
		return err
	}
	if err := reg.RegisterCustomTool("get_gmail_trigger", "Inspect this target's incoming Gmail trigger, receiving address, saved routing, ordered rules and filters, readiness, delivery activity and setup options (eligible OAuth client names and account-connect permission). Read before and after changing it. The pane is read-only for configuration; sender_consent reports whether the signed-in owner must confirm additional senders there. Mailbox watch readiness is separate from sender approval. If configured=false, inspect setup.provisioning. An interactive app administrator can use setup_gmail_inbound to prepare automatic provisioning and a human Google consent link. Explain required Cloud permissions; use the optional manual checklist only when needed. Do not just tell the user to ask an administrator.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		return api.gmailTriggerToolRequest(ctx, session, workspace, nil)
	}, "gmail_connection_management"); err != nil {
		return err
	}
	filterSchema := map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
		"sender_allowlist":     map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}, "description": "Any exact email address or exact @domain matches. No wildcard/display name. In common filters, omitted/empty keeps owner-only access. In a rule, omitted/empty inherits the common sender policy; an explicit rule list also intersects any explicit common list. Only propose when the owner requests these senders. Additional senders require a separate browser confirmation; public mailbox domain entries are rejected."},
		"subject_contains_any": map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}, "description": "At least one subject phrase must match (OR)."},
		"body_contains_any":    map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}, "description": "At least one body phrase must match (OR)."},
		"allow_automatic":      map[string]interface{}{"type": "boolean", "description": "Explicitly allow automated notifications from sender_allowlist (required). Never permits auto-replies, bounces, spam or trash."},
		"subject_contains":     map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}},
		"body_contains":        map[string]interface{}{"type": "array", "maxItems": 10, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 256}},
		"has_attachments":      map[string]interface{}{"type": "boolean", "description": "True requires attachments; false requires no attachments; omit for either."},
		"new_threads_only":     map[string]interface{}{"type": "boolean"},
	}}
	ruleSchema := map[string]interface{}{"type": "array", "maxItems": 20, "description": "Ordered named rules; first authorized match wins. Omission preserves rules. A supplied list replaces all; [] restores legacy single-action mode (supply workflow binding when clearing). Preserve stable IDs on edits. No match skips the email.", "items": map[string]interface{}{"type": "object", "additionalProperties": false, "required": []string{"id", "name"}, "properties": map[string]interface{}{
		"id":               map[string]interface{}{"type": "string", "pattern": "^[a-zA-Z0-9_-]{1,64}$", "description": "Stable unique rule ID chosen by Builder; do not ask the user for IDs."},
		"name":             map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 120},
		"enabled":          map[string]interface{}{"type": "boolean", "description": "Defaults true; false pauses just this rule."},
		"filters":          filterSchema,
		"instruction":      map[string]interface{}{"type": "string", "maxLength": 16384, "description": "Crew/Code only: saved chat instruction, with incoming email supplied separately as untrusted context."},
		"route_selections": map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}, "description": "Workflow only: exact saved branches; {} explicitly selects the full workflow."},
		"group_names":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Workflow only: required saved variable groups."},
		"step_id":          map[string]interface{}{"type": "string", "description": "Workflow only: standalone saved step, instead of route_selections."},
	}}}
	return reg.RegisterCustomTool("manage_gmail_trigger", "Connect, configure or disable this target's unique incoming-email trigger as its interactive owner. Read get_gmail_trigger and list_gmail_connections first. Use action=connect to create an account or request read consent on connection_id and return a Google consent link; the human must authorize, then inspect again before configure. Existing account-management permissions apply. client_name is only for connect; omit when one deployed OAuth client is eligible. For workflows discover saved route IDs/groups with manage_workflow_webhook(action=list). Use ordered rules for different actions: each has stable id, name, enabled (default true), filters and either a Crew/Code instruction or a workflow route_selections and group_names (or step_id). First authorized match wins; unmatched mail is skipped. Common filters restrict every rule. Per-rule senders inherit common senders or owner-only unless explicitly selected. Rules use one address/watch. Omitted rules are preserved; a supplied array replaces all rules. [] restores the legacy single action (supply its workflow binding). Never combine rules with top-level workflow bindings. Preserve rule IDs and untouched rules when editing. Without rules, configure legacy route_selections and group_names (or step_id). Optional filters: sender_allowlist accepts exact email addresses or @domains; any listed sender matches (OR), and an omitted/empty list keeps owner-only access. Only propose additional senders when the interactive owner requests it. Non-owner senders are blocked until the owner separately confirms the exact saved configuration in the Incoming email pane. Agent tools and chat confirmations cannot approve it. Read sender_consent after saving and report pending approval; public mailbox domains such as @gmail.com are rejected, use exact addresses. Changing senders, rules, replies or enabled state invalidates previous consent. Existing subject_contains/body_contains require every keyword; subject_contains_any/body_contains_any require any listed keyword. Condition groups use AND; literal matching ignores case. allow_automatic explicitly permits notifications from the allowlist; auto-replies, bounces, spam and trash remain blocked. Omitted filters are preserved; filters={} clears them; a supplied filters object replaces the whole filter set. New threads only rejects replies and threads already accepted here. No filters by default. Crew/Code email starts isolated project chats. reply/enabled default true on first setup. Disable preserves the address. No arbitrary target, wildcard senders, UI setup forms or direct gog watch changes. Return address, filter summary and verified readiness; saving alone does not prove live delivery.", map[string]interface{}{
		"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]interface{}{
			"action":        map[string]interface{}{"type": "string", "enum": []string{"connect", "configure", "disable"}},
			"client_name":   map[string]interface{}{"type": "string", "description": "Only for connect: exact configured OAuth client; do not invent names."},
			"connection_id": map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string"},
			"enabled": map[string]interface{}{"type": "boolean"}, "reply": map[string]interface{}{"type": "boolean"},
			"route_selections": map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
			"group_names":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"step_id":          map[string]interface{}{"type": "string"},
			"filters":          filterSchema,
			"rules":            ruleSchema,
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
		if api.gmailInbound == nil || len(config.Topics) == 0 {
			return "", fmt.Errorf("Automatic incoming email is not enabled on this server. Read get_gmail_trigger.setup.provisioning and ask an interactive administrator to use setup_gmail_inbound for the one-time receiving setup. Google sign-in alone does not enable it.")
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
		input = gmailTriggerInput{WorkspacePath: workspace, ConnectionID: r.ConnectionID, Name: r.Name, Enabled: r.Enabled, Reply: r.Reply, RouteSelections: r.RouteSelections, GroupNames: r.GroupNames, StepID: r.StepID, Filters: r.Filters, Rules: r.Rules}
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
	if raw, supplied := args["rules"]; supplied {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return "", err
		}
		var rules []gmailinbound.Rule
		if err := json.Unmarshal(encoded, &rules); err != nil {
			return "", err
		}
		if len(rules) > 0 {
			for _, key := range []string{"route_selections", "group_names", "step_id"} {
				if _, supplied := args[key]; supplied {
					return "", fmt.Errorf("put workflow bindings inside each email rule")
				}
			}
			values["route_selections"], values["group_names"], values["step_id"] = nil, nil, ""
		} else if !isProjectWorkspacePath(workspace) {
			if _, supplied := args["group_names"]; !supplied {
				return "", fmt.Errorf("supply a legacy workflow binding when clearing email rules")
			}
			if _, supplied := args["route_selections"]; !supplied {
				if _, supplied := args["step_id"]; !supplied {
					return "", fmt.Errorf("supply route_selections or step_id when clearing email rules")
				}
			}
		}
	}
	if current.Route == nil && !isProjectWorkspacePath(workspace) && args["rules"] == nil {
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
