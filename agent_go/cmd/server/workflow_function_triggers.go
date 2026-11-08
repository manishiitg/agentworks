package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Workflow functions: a workflow offers typed functions to Crews, other
// workflows and external MCP/CLI callers through triggers of kind
// "function". A function trigger is an ordinary workflow trigger (fixed
// route and saved input values) plus a name, description and input names, each
// bound to a declared workflow variable. It has no public URL and no secret:
// the platform identifies the caller (a Crew or workflow session, or an
// access token) and checks it may run the workflow. A GitHub or CI webhook on
// the same route keeps its own strict provider checks; a function checks only
// its declared inputs, then sets them as that run's variables.

const triggerKindFunction = "function"

var workflowFunctionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// WorkflowFunctionSpec is the callable contract of a function trigger.
type WorkflowFunctionSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Inputs selects names from workflow Inputs. Types and defaults belong there.
	Inputs         []string               `json:"inputs,omitempty"`
	InputSchema    map[string]interface{} `json:"-"`
	AllowedCallers []triggerCaller        `json:"allowed_callers,omitempty"`
}

func isFunctionTriggerKind(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), triggerKindFunction)
}

// IsFunctionTrigger reports whether the schedule is a callable workflow
// function.
func (s WorkflowSchedule) IsFunctionTrigger() bool {
	return s.ScheduleType == "webhook" && isFunctionTriggerKind(s.Kind) && s.Function != nil
}

func validateWorkflowFunctionSpec(spec *WorkflowFunctionSpec) error {
	if spec == nil {
		return errors.New("a function trigger needs function.name")
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if !workflowFunctionNamePattern.MatchString(spec.Name) {
		return fmt.Errorf("function name %q must be snake_case, starting with a letter", spec.Name)
	}
	if spec.Name == crewFunctionAskName {
		return errors.New("ask is reserved for the assistant")
	}
	seen := map[string]bool{}
	for i, name := range spec.Inputs {
		name = strings.TrimSpace(name)
		spec.Inputs[i] = name
		if name == "" {
			return errors.New("every function input needs a name")
		}
		if name == "group" {
			return errors.New("group is reserved; use input_preset")
		}
		if seen[name] {
			return fmt.Errorf("function input %q is listed twice", name)
		}
		seen[name] = true
	}
	for _, caller := range spec.AllowedCallers {
		c := caller
		if err := validateAnyTriggerCaller(&c); err != nil {
			return err
		}
	}
	return nil
}

func workflowFunctionInputNames(spec *WorkflowFunctionSpec) []string {
	if spec == nil {
		return nil
	}
	return spec.Inputs
}

func workflowFunctionInputSchema(sched WorkflowSchedule) map[string]interface{} {
	if sched.Function.InputSchema != nil {
		return sched.Function.InputSchema
	}
	// An unresolved contract cannot admit caller values.
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}
}

// workflowFunctionResultSchema describes what a workflow function returns:
// the run's outcome, not a validated value.
func workflowFunctionResultSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"status": map[string]interface{}{"type": "string"},
		"error":  map[string]interface{}{"type": "string"},
		"steps":  map[string]interface{}{"type": "array"},
	}}
}

// workflowFunctions lists a workflow's enabled function triggers as the
// functions callers see.
func workflowFunctions(manifest *WorkflowManifest) []crewFunction {
	if manifest == nil {
		return nil
	}
	out := []crewFunction{}
	for _, sched := range manifest.Schedules {
		if !sched.IsFunctionTrigger() || !sched.Enabled {
			continue
		}
		resultSchema := workflowFunctionResultSchema()

		out = append(out, crewFunction{
			Name:         sched.Function.Name,
			Description:  firstNonEmptyTrimmed(sched.Function.Description, sched.Name),
			InputSchema:  workflowFunctionInputSchema(sched),
			ResultSchema: resultSchema,
			CreatedBy:    "workflow trigger " + sched.ID,
			TriggerID:    sched.ID,
		})
	}
	return out
}

// findWorkflowFunctionTrigger returns the enabled function trigger named
// function.
func findWorkflowFunctionTrigger(manifest *WorkflowManifest, function string) (*WorkflowSchedule, error) {
	if manifest == nil {
		return nil, ErrInternalTriggerNotFound
	}
	for i := range manifest.Schedules {
		sched := &manifest.Schedules[i]
		if sched.IsFunctionTrigger() && sched.Function.Name == strings.TrimSpace(function) {
			if !sched.Enabled {
				return nil, fmt.Errorf("function %q is disabled on this workflow", function)
			}
			return sched, nil
		}
	}
	return nil, fmt.Errorf("this workflow has no function %q", function)
}

// workflowFunctionCallerAllowed applies the optional allow-list.
func workflowFunctionCallerAllowed(spec *WorkflowFunctionSpec, caller triggerCaller) bool {
	if spec == nil || len(spec.AllowedCallers) == 0 {
		return true
	}
	for _, allowed := range spec.AllowedCallers {
		a := allowed
		if a.matchesAnyPresented(caller) {
			return true
		}
	}
	return false
}

// sharedFunctionCallerAllowed: anyone a workflow, Flow or Crew is shared with
// may run its published functions (owner decision 2026-10-14: sharing a product
// is sharing what it publishes). The per-function allow-list keeps narrowing
// callers that are not people (a workflow, a Crew); a person entry in it no
// longer restricts. Access is rechecked on every call.
func sharedFunctionCallerAllowed(live *WorkflowManifest, spec *WorkflowFunctionSpec, caller triggerCaller) bool {
	if workflowFunctionCallerAllowed(spec, caller) {
		return true
	}
	return live != nil && strings.EqualFold(caller.Type, triggerCallerUser) && caller.ID != "" &&
		workflowAccessForManifest(principalClaims(caller.ID), live) != WorkflowAccessNone
}

// workflowFunctionArgs checks call arguments against the function's inputs
// and turns them into the run's variables and group. Unknown, missing or
// mistyped inputs fail before anything runs.
func workflowFunctionArgs(sched WorkflowSchedule, args map[string]interface{}) (map[string]string, string, error) {
	if _, present := args["group"]; present {
		return nil, "", fmt.Errorf("group is retired; configure input_preset")
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	schema, err := stepworkflow.CompileInputSchema(workflowFunctionInputSchema(sched))
	if err != nil {
		return nil, "", err
	}
	if err := schema.Validate(args); err != nil {
		return nil, "", fmt.Errorf("function input: %w", err)
	}
	values := map[string]string{}
	for name, raw := range args {
		if !slices.Contains(sched.Function.Inputs, name) {
			return nil, "", fmt.Errorf("unknown input %q", name)
		}
		value, ok := raw.(string)
		if !ok {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return nil, "", err
			}
			value = string(encoded)
		}
		if len(value) > 16384 {
			return nil, "", fmt.Errorf("input %q exceeds 16 KiB", name)
		}
		values[name] = value
	}
	return values, "", nil
}

// workflowFunctionCall is one call of a workflow function.
type workflowFunctionCall struct {
	WorkflowID string
	Function   string
	Caller     triggerCaller
	DeliveryID string
	Args       map[string]interface{}
	// Payload is the JSON the run sees (arguments, call id, caller).
	Payload map[string]interface{}
}

// dispatchWorkflowFunction validates and starts a workflow function run. It
// returns the function trigger's ID with the delivery.
func (s *SchedulerService) dispatchWorkflowFunction(ctx context.Context, call workflowFunctionCall) (string, internalTriggerDeliveryResult, error) {
	workspacePath, manifest, err := findWorkflowManifestByID(ctx, call.WorkflowID)
	if err != nil {
		return "", internalTriggerDeliveryResult{}, fmt.Errorf("%w: %w", ErrInternalTriggerNotFound, err)
	}

	sched, err := findWorkflowFunctionTrigger(manifest, call.Function)
	if err != nil {
		return "", internalTriggerDeliveryResult{}, err
	}
	if !sharedFunctionCallerAllowed(manifest, sched.Function, call.Caller) {
		return sched.ID, internalTriggerDeliveryResult{}, fmt.Errorf("%w: function %q does not allow this caller", ErrInternalCallerMismatch, sched.Function.Name)
	}
	argumentSchedule := *sched

	variables, group, err := workflowFunctionArgs(argumentSchedule, call.Args)
	if err != nil {
		return sched.ID, internalTriggerDeliveryResult{}, err
	}
	payload := call.Payload
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["function"] = sched.Function.Name

	payload["args"] = call.Args
	deliveryID := strings.TrimSpace(call.DeliveryID)
	if deliveryID == "" {
		deliveryID = uuid.NewString()
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return sched.ID, internalTriggerDeliveryResult{}, fmt.Errorf("invalid payload")
	}
	if err := checkInternalPayload(body); err != nil {
		return sched.ID, internalTriggerDeliveryResult{}, err
	}
	receiver := webhookReceiver{start: s.triggerSavedSchedule, existing: s.existingWebhookRun}
	delivery, err := receiver.deliverFunction(ctx, manifest.ID, workspacePath, *sched, deliveryID, body, variables, group)
	return sched.ID, delivery, err
}

// deliverFunction is deliver for a function trigger: the validated inputs
// become the run's variables directly, with no envelope or provider format.
func (receiver webhookReceiver) deliverFunction(ctx context.Context, manifestID, workspacePath string, sched WorkflowSchedule, deliveryID string, body []byte, variables map[string]string, group string) (internalTriggerDeliveryResult, error) {
	runID := webhookDeliveryRunID(manifestID, sched.ID, deliveryID)
	if run, lookupErr := receiver.existing(ctx, runID); lookupErr == nil {
		return internalTriggerDeliveryResult{RunID: run.RunID, DeliveryID: deliveryID, Duplicate: true, Status: string(run.State)}, nil
	}
	input := &WorkflowWebhookDelivery{
		RunID: runID, DeliveryID: deliveryID, Event: "agentworks.function." + sched.Function.Name,
		ReceivedAt: time.Now().UTC(), Payload: append(json.RawMessage(nil), body...),
		Variables: variables, Group: group,
	}
	acceptedID, err := receiver.start(workspacePath, sched.ID, "", input)
	if err != nil {
		if run, lookupErr := receiver.existing(ctx, runID); lookupErr == nil {
			return internalTriggerDeliveryResult{RunID: run.RunID, DeliveryID: deliveryID, Duplicate: true, Status: string(run.State)}, nil
		}
		return internalTriggerDeliveryResult{}, err
	}
	return internalTriggerDeliveryResult{RunID: acceptedID, DeliveryID: deliveryID, Status: "accepted"}, nil
}
