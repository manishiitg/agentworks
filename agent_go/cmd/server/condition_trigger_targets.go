package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
)

// Conditions are callers of existing entry points; they never mutate a target
// to install a binding. A route binding is stored on the caller and re-admitted
// when the existing webhook executor starts it, including after recovery.
type conditionRouteCaller struct {
	OwnerID  string `json:"owner_id"`
	JobID    string `json:"job_id"`
	Revision string `json:"revision"`
}

func (c *conditionService) resolveTarget(ctx context.Context, job productScheduleJob, name string) (triggerTarget, error) {
	// Use current visible products, including shared workflows in Run mode.
	query := strings.TrimPrefix(strings.TrimSpace(name), "#")
	if strings.HasPrefix(strings.ToLower(query), "code:") || isCodeProjectPath(query) {
		return triggerTarget{}, errCodeHasNoFunctions
	}
	workflows, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return triggerTarget{}, err
	}
	needle := strings.ToLower(strings.TrimPrefix(query, "workflow:"))
	var matches []triggerTarget
	for _, w := range workflows {
		if w.MyAccess == "" || w.MyAccess == WorkflowAccessNone {
			continue
		}
		for _, n := range []string{w.Manifest.ID, w.Manifest.Label, w.WorkspacePath, path.Base(w.WorkspacePath)} {
			if strings.ToLower(n) == needle {
				matches = append(matches, triggerTarget{Kind: triggerCallerWorkflow, Path: w.WorkspacePath, Label: w.Manifest.Label, Manifest: w.Manifest})
				break
			}
		}
	}
	if len(matches) > 1 {
		return triggerTarget{}, conditiontrigger.PermanentError{Err: errors.New("watcher target is ambiguous; use the exact workspace path")}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return resolveFunctionTarget(ctx, &UserClaims{UserID: job.UserID}, c.caller(job), name)
}

// conditionChatIDPattern is what a Code agent target's chat_id may be: main, new, or a side chat id.
var conditionChatIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (c *conditionService) validateTarget(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config) error {
	if cfg.Target.Kind == "agent" {
		if cfg.Target.Name != "" {
			return errors.New("Code agent targets stay in this project; use chat_id for its side chats")
		}
		if id := strings.TrimSpace(cfg.Target.ChatID); id != "" && !conditionChatIDPattern.MatchString(id) {
			return errors.New("chat_id must be main, new (a new chat for each event) or the id of an existing side chat")
		}
		return nil
	}
	t, err := c.resolveTarget(ctx, job, cfg.Target.Name)
	if err != nil {
		return err
	}
	switch cfg.Target.Kind {
	case "function":
		functions, err := callableFunctions(ctx, t)
		if err != nil {
			return err
		}
		if _, found := findCrewFunction(functions, cfg.Target.Function); !found {
			return conditiontrigger.PermanentError{Err: fmt.Errorf("target has no published function %q", cfg.Target.Function)}
		}
	case "route":
		if t.Manifest == nil || t.Manifest.Kind == "relay" {
			return errors.New("route targets must be workflows; call a published Relay function instead")
		}
		if _, err := validateScheduleGroupNamesForWorkspace(ctx, t.Path, cfg.Target.Groups); err != nil {
			return err
		}
		return validateWebhookTarget(ctx, t.Path, "", cfg.Target.Routes)
	case "trigger":
		if t.Manifest != nil {
			s, err := findInternalWorkflowTrigger(t.Manifest, cfg.Target.TriggerID)
			if err != nil {
				return err
			}
			if !s.Caller.matchesAnyPresented(c.caller(job).Stamp) {
				return ErrInternalCallerMismatch
			}
		}
	}
	return nil
}

func (c *conditionService) dispatchTarget(ctx context.Context, job productScheduleJob, caller triggerLinkCaller, cfg conditiontrigger.Config, ev conditiontrigger.Event, key string) (string, error) {
	if err := c.validateTarget(ctx, job, cfg); err != nil {
		return "", err
	}
	t, err := c.resolveTarget(ctx, job, cfg.Target.Name)
	if err != nil {
		return "", err
	}
	switch cfg.Target.Kind {
	case "function":
		functions, err := callableFunctions(ctx, t)
		if err != nil {
			return "", err
		}
		fn, found := findCrewFunction(functions, cfg.Target.Function)
		if !found {
			return "", errors.New("watcher function was removed")
		}
		args := map[string]interface{}{}
		for k, v := range ev.Payload {
			args[k] = v
		}
		for k, v := range cfg.Target.Arguments {
			args[k] = v
		}
		if t.Kind == triggerCallerCrew {
			if problems := validateCrewFunctionValue(fn.InputSchema, args); len(problems) > 0 {
				return "", conditiontrigger.PermanentError{Err: fmt.Errorf("function inputs: %s", strings.Join(problems, "; "))}
			}
		}
		if t.Manifest != nil && t.Manifest.Kind != "relay" {
			sched, err := findWorkflowFunctionTrigger(t.Manifest, fn.Name)
			if err != nil {
				return "", conditiontrigger.PermanentError{Err: err}
			}
			if _, _, err := workflowFunctionArgs(*sched, args); err != nil {
				return "", conditiontrigger.PermanentError{Err: err}
			}
		}
		call, err := c.s.api.startCrewFunctionCall(ctx, job.UserID, caller, t, fn, args, triggerTargetDefaultTimeout, key)
		if err != nil {
			return "", err
		}
		return call.ID, nil
	case "trigger":
		// Untrusted event fields cannot set run_as_owner/run_mode or target identity.
		result, err := c.s.api.dispatchTargetTrigger(ctx, job.UserID, caller, t, cfg.Target.TriggerID, key, "agentworks.condition", map[string]interface{}{"event_id": ev.ID, "payload": ev.Payload})
		if err != nil {
			return "", err
		}
		return result.RunID, nil
	case "route":
		if c.s.api.scheduler == nil {
			return "", errors.New("workflow execution unavailable")
		}
		s := conditionRouteSchedule(job, cfg)
		payload, _ := json.Marshal(ev.Payload)
		receiver := webhookReceiver{existing: c.s.api.scheduler.existingWebhookRun, start: func(workspacePath, scheduleID, origin string, input *WorkflowWebhookDelivery) (string, error) {
			input.ConditionCaller = &conditionRouteCaller{OwnerID: job.UserID, JobID: job.ID(), Revision: cfg.Revision()}
			return c.s.api.scheduler.triggerSavedSchedule(workspacePath, scheduleID, origin, input)
		}}
		result, err := receiver.deliver(ctx, t.Manifest.ID, t.Path, s, key, "agentworks.condition", payload)
		if err != nil {
			return "", err
		}
		return result.RunID, nil
	}
	return "", errors.New("unknown watcher target")
}

func conditionRouteSchedule(job productScheduleJob, cfg conditiontrigger.Config) WorkflowSchedule {
	return WorkflowSchedule{ID: "condition-" + conditiontrigger.Hash(conditionKey(job)), Name: job.Schedule.Name, Enabled: true, ScheduleType: "webhook", Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerCrew, ID: job.ProjectID, ProfileID: job.Profile.ID}, GroupNames: cfg.Target.Groups, RouteSelections: cfg.Target.Routes, Webhook: &WorkflowWebhookConfig{InputMode: "raw"}, PulseMode: "off"}
}

func (s *SchedulerService) conditionRoute(ctx context.Context, workspacePath, id string, input *WorkflowWebhookDelivery) (*WorkflowSchedule, error) {
	if input.ConditionCaller == nil || s.api.productSchedules == nil {
		return nil, errors.New("watcher route caller unavailable")
	}
	b := input.ConditionCaller
	ctx = internalBotRequestContext(ctx, b.OwnerID)
	job, err := s.api.productSchedules.Job(ctx, b.OwnerID, b.JobID)
	if err != nil || job.Schedule.Condition == nil {
		return nil, errors.New("watcher route caller was removed")
	}
	c := s.api.productSchedules.conditionService()
	cfg := *job.Schedule.Condition
	if err := c.current(ctx, job, cfg); err != nil {
		return nil, err
	}
	if cfg.Revision() != b.Revision || cfg.Target.Kind != "route" {
		return nil, errors.New("watcher route binding changed")
	}
	if err := c.validateTarget(ctx, job, cfg); err != nil {
		return nil, err
	}
	t, err := c.resolveTarget(ctx, job, cfg.Target.Name)
	if err != nil || t.Path != workspacePath {
		return nil, errors.New("watcher route target changed")
	}
	sched := conditionRouteSchedule(job, cfg)
	if sched.ID != id {
		return nil, errors.New("watcher route identity mismatch")
	}
	return &sched, nil
}
