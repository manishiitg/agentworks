package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Workflow functions: a workflow offers typed functions to Crews, other
// workflows and external MCP/CLI callers through triggers of kind
// "function". A function trigger is an ordinary workflow trigger (fixed
// route, allowed groups) plus a name, a description and typed inputs, each
// bound to a declared workflow variable. It has no public URL and no secret:
// the platform identifies the caller (a Crew or workflow session, or an
// access token) and checks it may run the workflow. A GitHub or CI webhook on
// the same route keeps its own strict provider checks; a function checks only
// its declared inputs, then sets them as that run's variables.

const triggerKindFunction = "function"

var workflowFunctionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// WorkflowFunctionSpec is the callable contract of a function trigger.
type WorkflowFunctionSpec struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Inputs      []WorkflowFunctionInput `json:"inputs,omitempty"`
	// AllowedCallers limits who may call; empty means anyone who can run
	// the workflow.
	AllowedCallers []triggerCaller `json:"allowed_callers,omitempty"`
}

// WorkflowFunctionInput is one argument, set as the declared workflow
// variable of the same name for that run.
type WorkflowFunctionInput struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"` // string (default), integer, number, boolean, object
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

func isFunctionTriggerKind(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), triggerKindFunction)
}

// IsFunctionTrigger reports whether the schedule is a callable workflow
// function.
func (s WorkflowSchedule) IsFunctionTrigger() bool {
	return s.ScheduleType == "webhook" && isFunctionTriggerKind(s.Kind) && s.Function != nil
}

func workflowFunctionInputType(input WorkflowFunctionInput) string {
	switch strings.ToLower(strings.TrimSpace(input.Type)) {
	case "integer", "number", "boolean", "object":
		return strings.ToLower(strings.TrimSpace(input.Type))
	default:
		return "string"
	}
}

// validateWorkflowFunctionSpec checks the spec's own shape; that each input
// is a declared, non-secret variable is checked on save against the
// workspace (validateWebhookVariableNames).
func validateWorkflowFunctionSpec(spec *WorkflowFunctionSpec) error {
	if spec == nil {
		return errors.New("a function trigger needs function.name and function.inputs")
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if !workflowFunctionNamePattern.MatchString(spec.Name) {
		return fmt.Errorf("function name %q must be snake_case (a-z, 0-9, _), starting with a letter", spec.Name)
	}
	if spec.Name == crewFunctionAskName {
		return errors.New(`"ask" is reserved: every workflow's ask goes to its assistant`)
	}
	seen := map[string]bool{}
	for i := range spec.Inputs {
		input := &spec.Inputs[i]
		input.Name = strings.TrimSpace(input.Name)
		if input.Name == "" {
			return errors.New("every function input needs a name (a declared workflow variable)")
		}
		if input.Name == "group" {
			return errors.New(`"group" is reserved for choosing the variable group`)
		}
		if seen[input.Name] {
			return fmt.Errorf("function input %q is listed twice", input.Name)
		}
		seen[input.Name] = true
		switch strings.ToLower(strings.TrimSpace(input.Type)) {
		case "", "string", "integer", "number", "boolean", "object":
		default:
			return fmt.Errorf("function input %q: type must be string, integer, number, boolean or object", input.Name)
		}
	}
	for _, caller := range spec.AllowedCallers {
		c := caller
		if err := validateAnyTriggerCaller(&c); err != nil {
			return fmt.Errorf("allowed caller: %w", err)
		}
	}
	return nil
}

func workflowFunctionInputNames(spec *WorkflowFunctionSpec) []string {
	if spec == nil {
		return nil
	}
	names := make([]string, 0, len(spec.Inputs))
	for _, input := range spec.Inputs {
		names = append(names, input.Name)
	}
	return names
}

// workflowFunctionInputSchema is the JSON Schema callers see and that call
// arguments are validated against.
func workflowFunctionInputSchema(sched WorkflowSchedule) map[string]interface{} {
	properties := map[string]interface{}{}
	required := []interface{}{}
	for _, input := range sched.Function.Inputs {
		property := map[string]interface{}{"type": workflowFunctionInputType(input)}
		if strings.TrimSpace(input.Description) != "" {
			property["description"] = input.Description
		}
		if len(input.Enum) > 0 {
			values := make([]interface{}, 0, len(input.Enum))
			for _, value := range input.Enum {
				values = append(values, value)
			}
			property["enum"] = values
		}
		properties[input.Name] = property
		if input.Required {
			required = append(required, input.Name)
		}
	}
	if len(sched.GroupNames) > 1 {
		values := make([]interface{}, 0, len(sched.GroupNames))
		for _, group := range sched.GroupNames {
			values = append(values, group)
		}
		properties["group"] = map[string]interface{}{"type": "string", "enum": values, "description": "Variable group to run with; omit for the default."}
	}
	schema := map[string]interface{}{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
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
		if manifest.Kind == "relay" {
			// The authored Relay output is JSON chosen by its author. A future
			// output-schema field can narrow this for specific integrations.
			resultSchema = nil
		}
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

// workflowFunctionArgs checks call arguments against the function's inputs
// and turns them into the run's variables and group. Unknown, missing or
// mistyped inputs fail before anything runs.
func workflowFunctionArgs(sched WorkflowSchedule, args map[string]interface{}) (map[string]string, string, error) {
	inputs := map[string]WorkflowFunctionInput{}
	for _, input := range sched.Function.Inputs {
		inputs[input.Name] = input
	}
	variables := map[string]string{}
	group := ""
	problems := []string{}
	for name, raw := range args {
		if name == "group" && len(sched.GroupNames) > 1 {
			value, ok := raw.(string)
			if !ok || !slices.Contains(sched.GroupNames, value) {
				problems = append(problems, fmt.Sprintf("group must be one of %s", strings.Join(sched.GroupNames, ", ")))
				continue
			}
			group = value
			continue
		}
		input, known := inputs[name]
		if !known {
			problems = append(problems, fmt.Sprintf("unknown input %q", name))
			continue
		}
		value, err := workflowFunctionInputValue(input, raw)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		variables[name] = value
	}
	for _, input := range sched.Function.Inputs {
		if _, given := variables[input.Name]; input.Required && !given {
			problems = append(problems, fmt.Sprintf("missing required input %s", input.Name))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, "", fmt.Errorf("%s: %s", sched.Function.Name, strings.Join(problems, "; "))
	}
	return variables, group, nil
}

func workflowFunctionInputValue(input WorkflowFunctionInput, raw interface{}) (string, error) {
	var value string
	switch workflowFunctionInputType(input) {
	case "object":
		if _, ok := raw.(map[string]interface{}); !ok {
			return "", fmt.Errorf("input %s must be an object", input.Name)
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return "", fmt.Errorf("input %s must be a JSON object", input.Name)
		}
		value = string(encoded)
	case "integer":
		switch n := raw.(type) {
		case float64:
			if n != float64(int64(n)) {
				return "", fmt.Errorf("input %s must be an integer", input.Name)
			}
			value = strconv.FormatInt(int64(n), 10)
		case int:
			value = strconv.Itoa(n)
		case string:
			if _, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err != nil {
				return "", fmt.Errorf("input %s must be an integer", input.Name)
			}
			value = strings.TrimSpace(n)
		default:
			return "", fmt.Errorf("input %s must be an integer", input.Name)
		}
	case "number":
		switch n := raw.(type) {
		case float64:
			value = strconv.FormatFloat(n, 'f', -1, 64)
		case int:
			value = strconv.Itoa(n)
		default:
			return "", fmt.Errorf("input %s must be a number", input.Name)
		}
	case "boolean":
		b, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("input %s must be true or false", input.Name)
		}
		value = strconv.FormatBool(b)
	default:
		s, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("input %s must be a string", input.Name)
		}
		if input.Required && strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("input %s must not be empty", input.Name)
		}
		value = s
	}
	if len(value) > 16384 {
		return "", fmt.Errorf("input %s exceeds 16 KiB", input.Name)
	}
	if len(input.Enum) > 0 && !slices.Contains(input.Enum, value) {
		return "", fmt.Errorf("input %s must be one of %s", input.Name, strings.Join(input.Enum, ", "))
	}
	return value, nil
}

// workflowFunctionCall is one call of a workflow function.
type workflowFunctionCall struct {
	WorkflowID   string
	Function     string
	RelayVersion string
	Caller       triggerCaller
	DeliveryID   string
	Args         map[string]interface{}
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
	if manifest.Kind == "relay" {
		// Revoking a function or caller on the live Relay takes effect at once.
		// The published copy still supplies the versioned input contract and
		// executable graph, but never grants authority on its own.
		liveSchedule, liveErr := findWorkflowFunctionTrigger(manifest, call.Function)
		if liveErr != nil {
			return "", internalTriggerDeliveryResult{}, liveErr
		}
		if !workflowFunctionCallerAllowed(liveSchedule.Function, call.Caller) {
			return liveSchedule.ID, internalTriggerDeliveryResult{}, fmt.Errorf("%w: function %q does not allow this caller", ErrInternalCallerMismatch, call.Function)
		}
		release, releaseWorkspace, releaseErr := resolveRelayRelease(ctx, workspacePath, call.RelayVersion)
		if releaseErr != nil {
			return "", internalTriggerDeliveryResult{}, releaseErr
		}
		if err := verifyRelayRelease(ctx, release, releaseWorkspace); err != nil {
			return "", internalTriggerDeliveryResult{}, err
		}
		workspacePath = releaseWorkspace
		manifest, _, err = ReadWorkflowManifest(ctx, workspacePath)
		if err != nil || manifest == nil {
			return "", internalTriggerDeliveryResult{}, fmt.Errorf("read published Relay %s: %w", release.Version, err)
		}
		if call.Payload == nil {
			call.Payload = map[string]interface{}{}
		}
		call.Payload["relay_version"] = release.Version
	}
	sched, err := findWorkflowFunctionTrigger(manifest, call.Function)
	if err != nil {
		return "", internalTriggerDeliveryResult{}, err
	}
	if manifest.Kind != "relay" && !workflowFunctionCallerAllowed(sched.Function, call.Caller) {
		return sched.ID, internalTriggerDeliveryResult{}, fmt.Errorf("%w: function %q does not allow this caller", ErrInternalCallerMismatch, sched.Function.Name)
	}
	variables, group, err := workflowFunctionArgs(*sched, call.Args)
	if err != nil {
		return sched.ID, internalTriggerDeliveryResult{}, err
	}
	payload := call.Payload
	if payload == nil {
		payload = map[string]interface{}{}
	}
	payload["function"] = sched.Function.Name
	if manifest.Kind == "relay" {
		payload["relay_trigger_caller"] = call.Caller
	}
	payload["args"] = call.Args
	deliveryID := strings.TrimSpace(call.DeliveryID)
	if deliveryID == "" {
		deliveryID = uuid.NewString()
	}
	if manifest.Kind == "relay" {
		// A replay must return its original run even if the author edited the
		// plan after dispatch. Keep the existing function-trigger idempotency.
		if run, lookupErr := s.existingWebhookRun(ctx, webhookDeliveryRunID(manifest.ID, sched.ID, deliveryID)); lookupErr == nil {
			return sched.ID, internalTriggerDeliveryResult{RunID: run.RunID, DeliveryID: deliveryID, Duplicate: true, Status: string(run.State)}, nil
		}
		if strings.TrimSpace(manifest.RelayOutputStepID) == "" {
			return sched.ID, internalTriggerDeliveryResult{}, fmt.Errorf("Relay %q has no relay_output_step_id", manifest.Label)
		}
		if err := validateRelayOutputStep(ctx, workspacePath, manifest.RelayOutputStepID); err != nil {
			return sched.ID, internalTriggerDeliveryResult{}, err
		}
		payload["relay_output_step_id"] = manifest.RelayOutputStepID
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

func validateRelayOutputStep(ctx context.Context, workspacePath, outputStepID string) error {
	plan, err := readPlanFromWorkspace(ctx, workspacePath)
	if err != nil {
		return fmt.Errorf("read Relay plan: %w", err)
	}
	return stepworkflow.ValidateRelayPlanStructure(plan, outputStepID)
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
