package step_based_workflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestScriptedRouteToolName(t *testing.T) {
	for routeID, want := range map[string]string{
		"lookup-customer":         "lookup_customer",
		"Lookup Customer":         "lookup_customer",
		"fetch.prices/v2":         "fetch_prices_v2",
		"2fa-check":               "route_2fa_check",
		"---":                     "route_",
		strings.Repeat("a", 80):   strings.Repeat("a", 64),
		"already_ok_name_123":     "already_ok_name_123",
		"  spaced  route  name  ": "spaced_route_name",
	} {
		if got := scriptedRouteToolName(routeID); got != want {
			t.Errorf("scriptedRouteToolName(%q) = %q, want %q", routeID, got, want)
		}
	}
}

func TestScriptedRouteInputSchemaMirrorsScriptParameters(t *testing.T) {
	schema := scriptedRouteInputSchema(map[string]ScriptParameterDefinition{
		"customer_id": {Type: "string", Description: "Customer id", Required: true},
		"region":      {Type: "string", Description: "Region", Required: true, Default: "eu", Enum: []interface{}{"eu", "us"}},
		"tags":        {Type: "array", Description: "Tags"},
	})
	if schema["additionalProperties"] != false || schema["type"] != "object" {
		t.Fatalf("schema must be a closed object: %v", schema)
	}
	// A required parameter with a default may be omitted: the runtime fills it.
	if !reflect.DeepEqual(schema["required"], []string{"customer_id"}) {
		t.Errorf("required = %v, want only customer_id", schema["required"])
	}
	properties := schema["properties"].(map[string]interface{})
	region := properties["region"].(map[string]interface{})
	if region["default"] != "eu" || len(region["enum"].([]interface{})) != 2 {
		t.Errorf("region = %v, want its default and enum carried over", region)
	}
	if _, ok := properties["tags"].(map[string]interface{})["items"]; !ok {
		t.Error("an array parameter needs items for providers that require it")
	}
	if empty := scriptedRouteInputSchema(nil); empty["required"] != nil || len(empty["properties"].(map[string]interface{})) != 0 {
		t.Errorf("a route without parameters takes an empty object: %v", empty)
	}
}

func TestReadScriptedRouteResult(t *testing.T) {
	dir := t.TempDir()
	if got, err := readScriptedRouteResult(dir, dir); got != "" || err != nil {
		t.Fatalf("no file = %q %v, want nothing", got, err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, ScriptedRouteResultFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("  {\"customer\": {\"id\": \"c1\", \"name\": \"Ada\"}}\n")
	if got, err := readScriptedRouteResult(dir, dir); err != nil || got != `{"customer": {"id": "c1", "name": "Ada"}}` {
		t.Fatalf("valid JSON = %q %v", got, err)
	}
	write("{not json")
	if _, err := readScriptedRouteResult(dir, dir); err == nil {
		t.Error("invalid JSON must be reported")
	}
	write(`"` + strings.Repeat("x", maxScriptedRouteResultBytes) + `"`)
	if _, err := readScriptedRouteResult(dir, dir); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("an oversized result must be reported: %v", err)
	}
}

func TestScriptedRouteDirectToolsOfferOnlyScriptedRoutesWithoutShadowing(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/demo")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	scripted := func(id string, params map[string]ScriptParameterDefinition) *RegularPlanStep {
		return &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: id, Title: id, Description: "Look up one customer by id", ScriptParameters: params}}
	}
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "agent"}, PredefinedRoutes: []PlanOrchestrationRoute{
		{RouteID: "lookup-customer", Condition: "When a customer id is known", SubAgentStep: scripted("lookup", map[string]ScriptParameterDefinition{"customer_id": {Type: "string", Description: "Id", Required: true}})},
		{RouteID: "research", SubAgentStep: &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "research"}}},
		{RouteID: "execute_shell_command", SubAgentStep: scripted("shadow", nil)},
		{RouteID: "broken", SubAgentStep: scripted("broken", map[string]ScriptParameterDefinition{"bad name": {Type: "string", Description: "x"}})},
	}}
	reserved := map[string]bool{"execute_shell_command": true}
	tools, toolsErr := hcpo.scriptedRouteDirectTools(&SubAgentExecutionContext{OrchestratorStep: step}, reserved)
	if toolsErr != nil {
		t.Fatal(toolsErr)
	}
	if len(tools) != 1 || tools[0].Name != "lookup_customer" {
		names := []string{}
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("tools = %v, want only lookup_customer (agent route, platform-name collision and invalid contract are skipped)", names)
	}
	tool := tools[0]
	if tool.Execute == nil || tool.Timeout <= 0 {
		t.Error("the tool needs an executor and a timeout")
	}
	for _, want := range []string{"When a customer id is known", "Look up one customer by id", `call_scripted_sub_agent(route_id="lookup-customer")`, ScriptedRouteResultFile} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("description missing %q:\n%s", want, tool.Description)
		}
	}
	if !reserved["lookup_customer"] {
		t.Error("the new name must be reserved so a second route cannot take it")
	}
	if again, _ := hcpo.scriptedRouteDirectTools(&SubAgentExecutionContext{OrchestratorStep: step}, reserved); len(again) != 0 {
		t.Errorf("a name already taken is never offered twice: %d tools", len(again))
	}
}

func TestScriptParametersSchemaContract(t *testing.T) {
	schema := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []interface{}{"customer"},
		"properties": map[string]interface{}{
			"customer": map[string]interface{}{
				"type":       "object",
				"required":   []interface{}{"id"},
				"properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "pattern": "^c-[0-9]+$"}},
			},
			"limit": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 50},
		},
	}
	step := &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "lookup", ScriptParametersSchema: schema}}
	if err := validateScriptParameterContract(&step.CommonStepFields); err != nil {
		t.Fatalf("valid schema refused: %v", err)
	}
	resolved, err := validateAndResolveScriptParameters(step, map[string]interface{}{"customer": map[string]interface{}{"id": "c-42"}, "limit": float64(5)})
	if err != nil || resolved["limit"] != float64(5) {
		t.Fatalf("valid nested input = %v %v", resolved, err)
	}
	for name, bad := range map[string]map[string]interface{}{
		"pattern":       {"customer": map[string]interface{}{"id": "42"}},
		"missing":       {"limit": float64(5)},
		"extra field":   {"customer": map[string]interface{}{"id": "c-1"}, "debug": true},
		"out of range":  {"customer": map[string]interface{}{"id": "c-1"}, "limit": float64(99)},
		"nested object": {"customer": "c-1"},
	} {
		if _, err := validateAndResolveScriptParameters(step, bad); err == nil {
			t.Errorf("%s: invalid input accepted", name)
		}
	}

	both := CommonStepFields{ID: "x", ScriptParametersSchema: schema, ScriptParameters: map[string]ScriptParameterDefinition{"a": {Type: "string", Description: "a"}}}
	if err := validateScriptParameterContract(&both); err == nil {
		t.Error("declaring both forms must be refused")
	}
	notObject := CommonStepFields{ID: "x", ScriptParametersSchema: map[string]interface{}{"type": "string"}}
	if err := validateScriptParameterContract(&notObject); err == nil {
		t.Error("a non-object schema must be refused")
	}
	external := CommonStepFields{ID: "x", ScriptParametersSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"a": map[string]interface{}{"$ref": "file:///etc/passwd"}}}}
	if err := validateScriptParameterContract(&external); err == nil {
		t.Error("an external $ref must never be loaded")
	}
}

func TestScriptedRouteToolUsesTheFullSchema(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/demo")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}, "additionalProperties": false}
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "agent"}, PredefinedRoutes: []PlanOrchestrationRoute{
		{RouteID: "lookup", SubAgentStep: &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "lookup", Description: "d", ScriptParametersSchema: schema}}},
	}}
	execCtx := &SubAgentExecutionContext{OrchestratorStep: step}
	tools, toolsErr := hcpo.scriptedRouteDirectTools(execCtx, map[string]bool{})
	if toolsErr != nil {
		t.Fatal(toolsErr)
	}
	if len(tools) != 1 || !reflect.DeepEqual(tools[0].InputSchema, schema) {
		t.Fatalf("the named tool must take the route's full schema: %+v", tools)
	}
	if paths := hcpo.scriptedRouteSourceReadPaths(execCtx); len(paths) != 1 || !strings.HasSuffix(paths[0], "/lookup") || !strings.HasPrefix(paths[0], "Workflow/demo/") {
		t.Errorf("the Agent step must be able to read its route's saved code: %v", paths)
	}
}

// PLAT-443: the result file is read from the route's own output folder only.
func TestReadScriptedRouteResultNeverFollowsLinksOutOfTheFolder(t *testing.T) {
	workflow := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"fixture_secret":"outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(workflow, "runs", "execution", "route")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A regular file is still returned.
	if err := os.WriteFile(filepath.Join(outputDir, ScriptedRouteResultFile), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readScriptedRouteResult(workflow, outputDir); err != nil || got != `{"ok":true}` {
		t.Fatalf("regular file = %q %v", got, err)
	}

	// A leaf symlink to a file outside is refused, never read.
	_ = os.Remove(filepath.Join(outputDir, ScriptedRouteResultFile))
	if err := os.Symlink(secret, filepath.Join(outputDir, ScriptedRouteResultFile)); err != nil {
		t.Skip("symlinks are unavailable")
	}
	if got, err := readScriptedRouteResult(workflow, outputDir); err == nil || strings.Contains(got, "fixture_secret") {
		t.Fatalf("a leaf symlink was followed: %q %v", got, err)
	}
	// Even a link to another file inside the workflow is not the script's own file.
	_ = os.Remove(filepath.Join(outputDir, ScriptedRouteResultFile))
	inside := filepath.Join(workflow, "db.json")
	_ = os.WriteFile(inside, []byte(`{"inside":1}`), 0o600)
	_ = os.Symlink(inside, filepath.Join(outputDir, ScriptedRouteResultFile))
	if got, err := readScriptedRouteResult(workflow, outputDir); err == nil || got != "" {
		t.Fatalf("a link to another workflow file was accepted: %q %v", got, err)
	}

	// A symlinked ancestor that leaves the workflow is refused.
	escaped := filepath.Join(workflow, "runs", "escaped")
	if err := os.Symlink(outside, escaped); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(outside, ScriptedRouteResultFile), []byte(`{"fixture_secret":"outside"}`), 0o600)
	if got, err := readScriptedRouteResult(workflow, escaped); err == nil || strings.Contains(got, "fixture_secret") {
		t.Fatalf("a symlinked output folder outside the workflow was read: %q %v", got, err)
	}

	// A special file (a directory here) is not a result.
	if err := os.Mkdir(filepath.Join(workflow, "runs", "execution", "dir-route"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirRoute := filepath.Join(workflow, "runs", "execution", "dir-route")
	if err := os.Mkdir(filepath.Join(dirRoute, ScriptedRouteResultFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := readScriptedRouteResult(workflow, dirRoute); err == nil {
		t.Error("a directory named route_result.json was accepted")
	}
}

// PLAT-444: an authored agent is never told about a tool that was not registered.
func TestAuthoredScriptToolCollisionsFailInsteadOfBeingAdvertised(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/demo")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	script := func(id string) PlanOrchestrationRoute {
		return PlanOrchestrationRoute{RouteID: id, RouteName: id, Condition: "when " + id, SubAgentStep: &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: id, Description: "d"}, ScriptOnly: true}}
	}
	authored := func(routes ...PlanOrchestrationRoute) *SubAgentExecutionContext {
		return &SubAgentExecutionContext{OrchestratorStep: &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "agent"}, AuthoredPrompt: true, SystemPrompt: "x", PredefinedRoutes: routes}}
	}

	// A platform-name collision is an error, not a silently missing tool.
	if _, err := hcpo.scriptedRouteDirectTools(authored(script("execute-shell-command")), map[string]bool{"execute_shell_command": true}); err == nil || !strings.Contains(err.Error(), "execute_shell_command") {
		t.Fatalf("a platform-name collision on an authored agent must fail: %v", err)
	}
	// Two routes normalizing to one name: the second is an error.
	if _, err := hcpo.scriptedRouteDirectTools(authored(script("a-b"), script("a_b")), map[string]bool{}); err == nil {
		t.Fatal("two routes with the same tool name must fail for an authored agent")
	}
	// In an ordinary workflow the old behaviour holds: skipped, still reachable
	// through call_scripted_sub_agent.
	ordinary := authored(script("execute-shell-command"))
	ordinary.OrchestratorStep.AuthoredPrompt = false
	tools, err := hcpo.scriptedRouteDirectTools(ordinary, map[string]bool{"execute_shell_command": true})
	if err != nil || len(tools) != 0 {
		t.Fatalf("an ordinary workflow keeps the skip behaviour: %v %v", tools, err)
	}

	// The prompt lists only what was registered, by its registered name.
	good := authored(script("lookup-customer"), script("check-price"))
	if _, err := hcpo.scriptedRouteDirectTools(good, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	block := authoredRoutesPromptBlock(good.OrchestratorStep.PredefinedRoutes, good.ScriptToolNames)
	for _, want := range []string{"`lookup_customer`: when lookup-customer", "`check_price`: when check-price"} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
	partial := map[string]string{"lookup-customer": "lookup_customer"}
	if block := authoredRoutesPromptBlock(good.OrchestratorStep.PredefinedRoutes, partial); strings.Contains(block, "check_price") || strings.Contains(block, "check-price") {
		t.Errorf("an unregistered route must not be advertised:\n%s", block)
	}
	if authoredRoutesPromptBlock(good.OrchestratorStep.PredefinedRoutes, nil) != "" {
		t.Error("no registered tools means no block")
	}
}
