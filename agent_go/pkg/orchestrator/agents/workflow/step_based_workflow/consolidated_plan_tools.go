package step_based_workflow

// Builder-facing adapters reuse the native mutation handlers. Native schemas,
// privileged writes, dependency validation and changelog receipts remain owned
// by those handlers; compatibility names are not registered on the agent.
import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type capturedPlanTool struct {
	description string
	schema      map[string]interface{}
	validator   *jsonschema.Schema
	execute     func(context.Context, map[string]interface{}) (string, error)
}
type consolidatedPlanRegistrar struct {
	DefinitionToolRegistrar
	tools     map[string]capturedPlanTool
	workspace string
	readFile  func(context.Context, string) (string, error)
}
type consolidatedWorkshopRegistrar struct {
	DefinitionRegistrar
	capture *consolidatedPlanRegistrar
}

func (r consolidatedWorkshopRegistrar) RegisterCustomTool(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), g string) error {
	return r.capture.RegisterCustomTool(n, d, s, e, g)
}
func (r consolidatedWorkshopRegistrar) RegisterCustomToolWithTimeout(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), t time.Duration, g string) error {
	return r.capture.RegisterCustomToolWithTimeout(n, consolidatedPlanToolText(d), s, e, t, g)
}

var consolidatedStepTypes = []string{"scripted", "message_sequence", "routing", "branch", "human_input", "orchestrator", "crew"}
var consolidatedMaintenance = map[string]string{
	"cleanup_orphan_configs": "cleanup_orphan_step_configs",
}

func capturedPlanName(n string) bool {
	for _, t := range consolidatedStepTypes {
		if n == "add_"+t+"_step" || n == "update_"+t+"_step" {
			return true
		}
	}
	switch n {
	case "add_todo_task_step", "update_todo_task_step", "add_todo_task_route", "update_todo_task_route", "delete_todo_task_route",
		"add_orchestrator_route", "update_orchestrator_route", "delete_orchestrator_route",
		"add_group", "update_group", "delete_group", "change_step_type", "convert_routing_branch_step_type":
		return true
	}
	for _, v := range consolidatedMaintenance {
		if n == v {
			return true
		}
	}
	return false
}
func newConsolidatedPlanRegistrar(r DefinitionToolRegistrar, workspace string, read func(context.Context, string) (string, error)) *consolidatedPlanRegistrar {
	return &consolidatedPlanRegistrar{DefinitionToolRegistrar: r, tools: map[string]capturedPlanTool{}, workspace: workspace, readFile: read}
}
func compilePlanToolSchema(s map[string]interface{}) (*jsonschema.Schema, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var normalized map[string]interface{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://agentworks.invalid/consolidated-plan-tool.json", normalized); err != nil {
		return nil, err
	}
	return c.Compile("https://agentworks.invalid/consolidated-plan-tool.json")
}
func (r *consolidatedPlanRegistrar) RegisterCustomTool(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), g string) error {
	return r.RegisterCustomToolWithTimeout(n, d, s, e, 0, g)
}
func (r *consolidatedPlanRegistrar) RegisterCustomToolWithTimeout(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), t time.Duration, g string) error {
	if n == "review_plan" {
		return nil
	}
	// PLAT-561: plan edits report the dependents of what they changed.
	if referenceMapPlanEditTool(n) {
		e = withReferenceMapNotes(r.workspace, e)
	}
	if !capturedPlanName(n) {
		return r.DefinitionToolRegistrar.RegisterCustomToolWithTimeout(n, consolidatedPlanToolText(d), s, e, t, g)
	}
	// Work on a copy: native schemas also serve stable external API clients.
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	closeExternalPlanSchema(schema)
	schema["additionalProperties"] = false
	validator, err := compilePlanToolSchema(schema)
	if err != nil {
		return fmt.Errorf("compile %s: %w", n, err)
	}
	r.tools[n] = capturedPlanTool{d, schema, validator, e}
	return nil
}
func (r *consolidatedPlanRegistrar) invoke(ctx context.Context, n string, args map[string]interface{}) (string, error) {
	t, ok := r.tools[n]
	if !ok {
		return "", fmt.Errorf("operation %s is unavailable in this session", n)
	}
	// Normalize Go callers the same way as JSON transports before validation.
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	var normalized map[string]interface{}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return "", err
	}
	if err := t.validator.Validate(normalized); err != nil {
		return "", fmt.Errorf("invalid %s parameters: %w", n, err)
	}
	out, err := t.execute(ctx, normalized)
	return consolidatedPlanToolText(out), err
}
// addStepTypeError validates step against the named type's native schema so
// the error lists that type's missing or unknown fields only.
func (r *consolidatedPlanRegistrar) addStepTypeError(args map[string]interface{}) error {
	typ, _ := args["type"].(string)
	native, ok := r.tools["add_"+typ+"_step"]
	if !ok {
		return nil
	}
	step, ok := args["step"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid add_step arguments: step must be an object")
	}
	if err := native.validator.Validate(step); err != nil {
		return fmt.Errorf("invalid add_step arguments for type %q (fix only these fields): %w", typ, err)
	}
	return nil
}
func objectToolSchema(props map[string]interface{}, required ...string) map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func stringToolSchema(values ...string) map[string]interface{} {
	s := map[string]interface{}{"type": "string", "minLength": 1}
	if len(values) > 0 {
		s["enum"] = values
	}
	return s
}
func copyToolSchema(s map[string]interface{}) map[string]interface{} {
	raw, _ := json.Marshal(s)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}
func withoutStepID(s map[string]interface{}) map[string]interface{} {
	out := copyToolSchema(s)
	props := out["properties"].(map[string]interface{})
	delete(props, "existing_step_id")
	required := []interface{}{}
	if list, ok := out["required"].([]interface{}); ok {
		for _, v := range list {
			if v != "existing_step_id" {
				required = append(required, v)
			}
		}
	}
	out["required"] = required
	return out
}
func toolObjectArg(args map[string]interface{}, key string) (map[string]interface{}, error) {
	v, ok := args[key].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	out := map[string]interface{}{}
	for k, v := range v {
		out[k] = v
	}
	return out, nil
}
func (r *consolidatedPlanRegistrar) register(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error)) error {
	return r.registerRefined(n, d, s, e, nil)
}

// registerRefined lets a tool replace a generic schema error with a more
// specific one (add_step: the selected type's own validation error instead of
// every oneOf branch's complaints, which pointed agents at other types' fields).
func (r *consolidatedPlanRegistrar) registerRefined(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), refine func(map[string]interface{}) error) error {
	validator, err := compilePlanToolSchema(s)
	if err != nil {
		return err
	}
	return r.DefinitionToolRegistrar.RegisterCustomTool(n, d, s, func(ctx context.Context, args map[string]interface{}) (string, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		var value map[string]interface{}
		if err = json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		if err = validator.Validate(value); err != nil {
			if refine != nil {
				if specific := refine(value); specific != nil {
					return "", specific
				}
			}
			return "", fmt.Errorf("invalid %s arguments: %w", n, err)
		}
		return e(ctx, value)
	}, "workflow")
}
func (r *consolidatedPlanRegistrar) flush() error {
	addBranches := []interface{}{}
	updateSchemas := []interface{}{}
	addTypes := []string{}
	for _, t := range consolidatedStepTypes {
		if native, ok := r.tools["add_"+t+"_step"]; ok {
			addTypes = append(addTypes, t)
			addBranches = append(addBranches, objectToolSchema(map[string]interface{}{"type": map[string]interface{}{"const": t}, "step": native.schema}, "type", "step"))
		}
		if native, ok := r.tools["update_"+t+"_step"]; ok {
			updateSchemas = append(updateSchemas, withoutStepID(native.schema))
		}
	}
	if len(addBranches) > 0 {
		s := objectToolSchema(map[string]interface{}{"type": stringToolSchema(addTypes...), "step": map[string]interface{}{"type": "object"}}, "type", "step")
		s["oneOf"] = addBranches
		if err := r.registerRefined("add_step", "Add a typed plan step. Put the native step fields, including reason, in step. Discover the type-specific schema; scripted is deterministic code, message_sequence is conversational, routing selects a mode, branch selects a path, human_input captures free-form input, orchestrator delegates. Uses the existing validated mutation handlers.", s, func(ctx context.Context, args map[string]interface{}) (string, error) {
			v, err := toolObjectArg(args, "step")
			if err != nil {
				return "", err
			}
			return r.invoke(ctx, "add_"+args["type"].(string)+"_step", v)
		}, r.addStepTypeError); err != nil {
			return err
		}
	}
	if len(updateSchemas) > 0 {
		s := objectToolSchema(map[string]interface{}{"step_id": stringToolSchema(), "changes": map[string]interface{}{"anyOf": updateSchemas}}, "step_id", "changes")
		if err := r.register("update_step", "Update a step by ID. Its type is read from the current persisted plan, including nested and orphan steps. Put native update fields and reason in changes; omit existing_step_id. Type-specific fields are strictly validated before writing. Use change_step_type for conversions.", s, func(ctx context.Context, args map[string]interface{}) (string, error) {
			id := args["step_id"].(string)
			plan, err := readPlanFromFile(ctx, r.workspace, r.readFile)
			if err != nil {
				return "", err
			}
			step, _, _ := findStepByID(plan.Steps, id)
			if step == nil {
				step, _, _ = findStepByID(plan.OrphanSteps, id)
			}
			if step == nil {
				return "", fmt.Errorf("step %q not found", id)
			}
			typ := string(step.StepType())
			if typ == "regular" {
				typ = "scripted"
			}
			if typ == "todo_task" {
				typ = "orchestrator"
			}
			v, err := toolObjectArg(args, "changes")
			if err != nil {
				return "", err
			}
			v["existing_step_id"] = id
			return r.invoke(ctx, "update_"+typ+"_step", v)
		}); err != nil {
			return err
		}
	}
	if err := r.registerActions("manage_step_route", "Manage predefined_routes for a message-sequence agent or persisted legacy orchestrator record with action=add/update/delete. parameters uses that action's native route schema, including parent_step_id and reason. Legacy todo_task names are not exposed.", map[string]string{"add": "add_orchestrator_route", "update": "update_orchestrator_route", "delete": "delete_orchestrator_route"}); err != nil {
		return err
	}
	if err := r.registerActions("manage_group", "Manage a variable group with action=add/update/delete. parameters uses that action's native fields. Existing group validation, current-session refresh and last-group deletion checks are preserved.", map[string]string{"add": "add_group", "update": "update_group", "delete": "delete_group"}); err != nil {
		return err
	}
	if err := r.registerActions("maintain_plan", "Explicit plan maintenance only. Select a supported action and its parameters; normal edits use add_step/update_step/change_step_type. Keeps existing migration validation and changelog behavior.", consolidatedMaintenance); err != nil {
		return err
	}
	if _, ok := r.tools["change_step_type"]; ok {
		s := objectToolSchema(map[string]interface{}{"step_id": stringToolSchema(), "target_type": stringToolSchema("scripted", "message_sequence", "routing", "branch"), "reason": stringToolSchema()}, "step_id", "target_type", "reason")
		if err := r.register("change_step_type", "Convert a step in place, preserving its identity and the existing conversion checks. Supported pairs: scripted <-> message_sequence and routing <-> branch only; arbitrary cross-type conversion is rejected. Scripted conversion drops conversational items. reason is required and recorded.", s, func(ctx context.Context, args map[string]interface{}) (string, error) {
			target := args["target_type"].(string)
			if target == "routing" || target == "branch" {
				v := map[string]interface{}{"existing_step_id": args["step_id"], "target_type": target, "reason": args["reason"]}
				return r.invoke(ctx, "convert_routing_branch_step_type", v)
			}
			return r.invoke(ctx, "change_step_type", args)
		}); err != nil {
			return err
		}
	}
	return nil
}
func (r *consolidatedPlanRegistrar) registerActions(name, description string, operations map[string]string) error {
	actions := []string{}
	for action, native := range operations {
		if _, ok := r.tools[native]; ok {
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		return nil
	}
	sort.Strings(actions)
	branches := []interface{}{}
	for _, action := range actions {
		t := r.tools[operations[action]]
		branches = append(branches, objectToolSchema(map[string]interface{}{"action": map[string]interface{}{"const": action}, "parameters": t.schema}, "action", "parameters"))
	}
	s := objectToolSchema(map[string]interface{}{"action": stringToolSchema(actions...), "parameters": map[string]interface{}{"type": "object"}}, "action", "parameters")
	s["oneOf"] = branches
	return r.register(name, description, s, func(ctx context.Context, args map[string]interface{}) (string, error) {
		v, err := toolObjectArg(args, "parameters")
		if err != nil {
			return "", err
		}
		return r.invoke(ctx, operations[args["action"].(string)], v)
	})
}

// ConsolidatedPlanToolName projects legacy names for construction-time catalogs
// and documentation. Native operation names remain only in internal receipts.
func ConsolidatedPlanToolName(n string) string {
	if strings.HasPrefix(n, "add_") && strings.HasSuffix(n, "_step") && capturedPlanName(n) {
		return "add_step"
	}
	if strings.HasPrefix(n, "update_") && strings.HasSuffix(n, "_step") && capturedPlanName(n) {
		return "update_step"
	}
	if strings.HasSuffix(n, "_route") && capturedPlanName(n) {
		return "manage_step_route"
	}
	if n == "add_group" || n == "update_group" || n == "delete_group" {
		return "manage_group"
	}
	if n == "convert_routing_branch_step_type" {
		return "change_step_type"
	}
	for _, v := range consolidatedMaintenance {
		if n == v {
			return "maintain_plan"
		}
	}
	return n
}

func consolidatedPlanToolText(text string) string {
	names := []string{}
	for _, typ := range append(append([]string{}, consolidatedStepTypes...), "todo_task") {
		names = append(names, "add_"+typ+"_step", "update_"+typ+"_step")
	}
	for _, typ := range []string{"orchestrator", "todo_task"} {
		for _, action := range []string{"add", "update", "delete"} {
			names = append(names, action+"_"+typ+"_route")
		}
	}
	names = append(names, "add_group", "update_group", "delete_group", "convert_routing_branch_step_type")
	for _, n := range consolidatedMaintenance {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, n := range names {
		text = strings.ReplaceAll(text, n, ConsolidatedPlanToolName(n))
	}
	return text
}
