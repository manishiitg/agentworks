package step_based_workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type savedCrewScriptFixture struct{ configuration CrewScriptConfiguration }

func (r *savedCrewScriptFixture) ResolveCrewScript(_ context.Context, workspace string, id string) (CrewScriptConfiguration, error) {
	if id == "" && workspace != r.configuration.Root {
		return CrewScriptConfiguration{}, nil
	}
	return r.configuration, nil
}

// PLAT-746-265: direct script execution and two referencing workflows use the
// same Python source, typed contract, and invocation-local recorded source.
func TestCrewSavedScriptRunsDirectlyAndAcrossWorkflowReferences(t *testing.T) {
	docs, _ := workflowHTTPTestWorkspace(t)
	write := func(file, content string) {
		t.Helper()
		file = filepath.Join(docs, file)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const root = "Crew/lab/code/qualify"
	definition := CrewScriptDefinition{ID: "qualify", Name: "Qualify lead", Inputs: []Variable{{Name: "count", Type: "integer", Required: true, Value: "2"}}, Outputs: []string{"result.json"}, InputPresets: []VariableGroup{{Name: "first", Values: map[string]string{"count": "5"}}, {Name: "second", Values: map[string]string{"count": "9"}}}}
	// PLAT-746-338 / PR498: omitted optional non-string inputs must remain
	// absent when direct invocation reuses its resolved launch snapshot.
	for _, kind := range []string{"integer", "number", "boolean", "object", "array"} {
		definition.Inputs = append(definition.Inputs, Variable{Name: "optional_" + kind, Type: kind})
	}
	raw, _ := json.Marshal(definition)
	write(root+"/script.json", string(raw))
	source := `import json, os
from pathlib import Path
from helper.values import revision
params = json.loads(os.environ['STEP_PARAMS_JSON'])
assert isinstance(params['count'], int)
assert Path(os.environ['SCRIPT_SOURCE_DIR'], 'template.txt').read_text() == 'template data'
assert json.loads(Path(os.environ['STEP_OUTPUT_DIR'], 'inputs.json').read_text()) == params
assert json.loads(Path(os.environ['SCRIPT_SOURCE_DIR'], 'script.json').read_text())['id'] == 'qualify'
assert not any(key.startswith('optional_') for key in params)
Path(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps({'count':params['count'], 'revision':revision()}))
`
	write(root+"/main.py", source)
	resolver := &savedCrewScriptFixture{configuration: CrewScriptConfiguration{Definition: definition, Root: root, Files: map[string][]byte{"main.py": []byte(source), "helper/values.py": []byte("def revision(): return 'first'\n"), "template.txt": []byte("template data")}}}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	run := func(workspace string, args map[string]interface{}) string {
		t.Helper()
		previous := map[string]bool{}
		before, _ := os.ReadDir(filepath.Join(docs, workspace, "runs"))
		for _, folder := range before {
			previous[folder.Name()] = true
		}
		cfg := &WorkshopConfig{WorkspacePath: workspace, Logger: loggerv2.NewNoop(), CrewResources: &CrewResources{Scripts: resolver}}
		if workspace == "Workflow/second" {
			// PLAT-746: scheduler options must retain the saved-script resolver.
			cfg.ScheduleInvocation = &ScheduleInvocation{ScheduleID: "timer", RunID: "scheduled-script", RunFolder: "run-scheduled-script"}
		}
		session, err := NewWorkshopChatSession(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		draft := newWorkshopDefinitionDraft()
		RegisterRunFullWorkflowTool(draft, session, loggerv2.NewNoop())
		out, err := draft.tools["run_full_workflow"].Execute(ctx, args)
		if err != nil || !strings.Contains(out, "execution_id:") {
			t.Fatalf("admission %s %v", out, err)
		}
		for {
			snapshot, found, _ := session.StepRegistry.LatestSnapshotForStep("full-workflow")
			if found && snapshot.Status != WorkshopStepRunning {
				if snapshot.Status != WorkshopStepDone {
					t.Fatalf("script execution: %+v", snapshot)
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
		folders, err := os.ReadDir(filepath.Join(docs, workspace, "runs"))
		if err != nil {
			t.Fatal(err)
		}
		for _, folder := range folders {
			if folder.IsDir() && strings.HasPrefix(folder.Name(), "run-") && !previous[folder.Name()] {
				if strings.HasPrefix(workspace, "Workflow/") {
					// PR498: inspecting an actual flat run must not require a retired group.
					manager := &InteractiveWorkshopManager{controller: session.controller}
					output, err := manager.debugStep(ctx, map[string]interface{}{"step_id": "use-script", "run_folder": folder.Name()})
					if err != nil || !strings.Contains(output, "### Scripted execution") || !strings.Contains(output, "runs/"+folder.Name()+"/logs/") {
						t.Fatalf("inspect saved script run: %s %v", output, err)
					}
					for _, invalid := range []string{"", "../other", "run-one/preset"} {
						if _, err := manager.debugStep(ctx, map[string]interface{}{"step_id": "use-script", "run_folder": invalid}); err == nil {
							t.Fatalf("invalid run path accepted: %q", invalid)
						}
					}
				}
				return filepath.Join(docs, workspace, "runs", folder.Name())
			}
		}
		t.Fatal("no run record")
		return ""
	}
	direct := run(root, map[string]interface{}{"inputs": map[string]interface{}{"count": "3"}})
	data, err := os.ReadFile(filepath.Join(direct, "execution/qualify/result.json"))
	if err != nil || !strings.Contains(string(data), `"count": 3`) {
		t.Fatalf("direct result %s %v", data, err)
	}
	// PLAT-746: a converted schedule/function preset must select its saved
	// typed values rather than silently reverting to defaults.
	preset := run(root, map[string]interface{}{"input_preset": "second"})
	data, err = os.ReadFile(filepath.Join(preset, "execution/qualify/result.json"))
	if err != nil || !strings.Contains(string(data), `"count": 9`) || preset == direct {
		t.Fatalf("preset result %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(docs, root, "workflow.json")); !os.IsNotExist(err) {
		t.Fatal("script saved a duplicate workflow definition")
	}
	var previousSource string
	for _, workspace := range []string{"Workflow/first", "Workflow/second"} {
		write(workspace+"/workflow.json", `{"code_layout_version":1}`)
		write(workspace+"/variables/variables.json", `{"variables":[{"name":"COUNT","type":"integer","value":"7"}]}`)
		write(workspace+"/planning/plan.json", `{"steps":[{"id":"use-script","type":"scripted","title":"Qualify","description":"Run the saved script","script":"qualify","script_inputs":{"count":{"input":"COUNT"}},"context_output":"result.json"}]}`)
		expectedCount := `"count": 7`
		if workspace == "Workflow/second" {
			resolver.configuration.Files["helper/values.py"] = []byte("def revision(): return 'second'\n")
			write(workspace+"/planning/plan.json", `{"steps":[{"id":"produce","type":"scripted","script_only":true,"title":"Prior output","description":"Write the prior count","context_output":"prior.json"},{"id":"use-script","type":"scripted","title":"Qualify","description":"Run the saved script","script":"qualify","context_dependencies":["prior.json"],"script_inputs":{"count":{"output":"prior.json","field":"count"}},"context_output":"result.json"}]}`)
			write(workspace+"/code/produce/main.py", "import json, os\nfrom pathlib import Path\nPath(os.environ['STEP_OUTPUT_DIR'],'prior.json').write_text(json.dumps({'count':11}))\n")
			expectedCount = `"count": 11`
		}
		runRoot := run(workspace, nil)
		result, err := os.ReadFile(filepath.Join(runRoot, "execution/use-script/result.json"))
		if err != nil || !strings.Contains(string(result), expectedCount) {
			t.Fatalf("workflow %s result %s %v", workspace, result, err)
		}
		saved, err := os.ReadFile(filepath.Join(runRoot, "execution/use-script/code/main.py"))
		if err != nil || string(saved) != source {
			t.Fatal("executed source not retained", err)
		}
		if workspace == "Workflow/first" {
			previousSource = filepath.Join(runRoot, "execution/use-script/code/helper/values.py")
		} else {
			old, _ := os.ReadFile(previousSource)
			if !strings.Contains(string(old), "'first'") || !strings.Contains(string(result), `"revision": "second"`) {
				t.Fatalf("updated source changed history or was not used on next run: %s %s", old, result)
			}
		}
	}
	// PLAT-746-353: a required script inside an agent must use the same saved
	// definition and typed runner, without a duplicate inline parameter schema.
	const requiredRoot = "Workflow/required"
	write(requiredRoot+"/workflow.json", `{"code_layout_version":1}`)
	write(requiredRoot+"/planning/plan.json", `{"steps":[{"id":"agent","type":"agent","title":"Agent","description":"Read inputs","items":[{"id":"batch","type":"scripted","scripted_steps":[{"id":"one","step_id":"saved","parameters":{"count":9}}]}]}],"orphan_steps":[{"id":"saved","type":"scripted","title":"Saved","description":"Use saved source","script":"qualify","script_only":true}]}`)
	session, err := NewWorkshopChatSession(ctx, &WorkshopConfig{WorkspacePath: requiredRoot, Logger: loggerv2.NewNoop(), CrewResources: &CrewResources{Scripts: resolver}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	plan, err := session.controller.ReadCurrentPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parent := plan.Steps[0].(*AgentPlanStep)
	runtime := &agentSequenceSession{scriptedPlan: plan}
	session.controller.selectedRunFolder = "run-required"
	item := parent.Items[0]
	item.ScriptedSteps[0].Parameters["count"] = "invalid"
	if _, err := session.controller.executeAgentScripts(ctx, parent, item, 0, "agent", runtime); err == nil {
		t.Fatal("required script accepted wrong input type")
	}
	item.ScriptedSteps[0].Parameters["count"] = 9
	if _, err := session.controller.executeAgentScripts(ctx, parent, item, 0, "agent", runtime); err != nil {
		t.Fatal(err)
	}
	outputs, _ := filepath.Glob(filepath.Join(docs, requiredRoot, "runs/run-required/execution/*/scripts/items/batch/calls/one/result.json"))
	if len(outputs) != 1 {
		t.Fatalf("required script result missing: %v", outputs)
	}
	result, _ := os.ReadFile(outputs[0])
	if !strings.Contains(string(result), `"count": 9`) {
		t.Fatalf("required script result: %s", result)
	}

}
