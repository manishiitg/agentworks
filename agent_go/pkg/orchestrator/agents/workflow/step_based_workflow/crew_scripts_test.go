package step_based_workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type savedCrewScriptFixture struct {
	configuration   CrewScriptConfiguration
	directWorkspace string
}

// PLAT-746-482: zero was silently mapped to one hour, not an unlimited run.
func TestCrewScriptTimeoutRejectsUnboundedDefinitions(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", 3600}, {"1", 1}, {"7200", 7200}, {"21600", 21600},
		{"0", 0}, {"-1", 0}, {"21601", 0}, {"1.5", 0}, {`"7200"`, 0},
	} {
		t.Run("timeout_"+tc.value, func(t *testing.T) {
			raw := `{"id":"report","name":"Report"`
			if tc.value != "" {
				raw += `,"timeout_seconds":` + tc.value
			}
			definition, err := ReadCrewScriptDefinition(t.Context(), "code/report", func(context.Context, string) (string, error) { return raw + "}", nil })
			if tc.want == 0 {
				if err == nil || !strings.Contains(err.Error(), "timeout_seconds") {
					t.Fatalf("invalid timeout admitted or wrong refusal: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := definition.executionTimeoutSeconds()
			if err != nil || got != tc.want {
				t.Fatalf("timeout=%d want=%d err=%v", got, tc.want, err)
			}
		})
	}
}

func (r *savedCrewScriptFixture) ResolveCrewScript(_ context.Context, workspace string, id string) (CrewScriptConfiguration, error) {
	if id == "" && workspace != r.directWorkspace {
		return CrewScriptConfiguration{}, nil
	}
	return r.configuration, nil
}

// PLAT-746-265/471: saved Python runs keep typed inputs, declared secrets and
// invocation-local frozen source; old plans no longer launch this runner.
func TestCrewSavedScriptRunsWithCanonicalInputsAndFrozenSource(t *testing.T) {
	if runtime.GOOS == "linux" {
		root, err := os.MkdirTemp(".", ".script-reference-fixture-")
		if err != nil {
			t.Fatal(err)
		}
		root, err = filepath.Abs(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		t.Setenv("TMPDIR", root)
	}
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
	const directWorkspace = "Workflow/direct-script"
	definition := CrewScriptDefinition{ID: "qualify", Name: "Qualify lead", Inputs: []Variable{{Name: "count", Type: "integer", Required: true, Value: "2"}}, Outputs: []string{"result.json"}, InputPresets: []VariableGroup{{Name: "first", Values: map[string]string{"count": "5"}}, {Name: "second", Values: map[string]string{"count": "9"}}}}
	definition.Secrets = []string{"DECLARED"}
	const secretValue = "fixture-environment-value-never-saved"
	// PLAT-746-338 / PR498: omitted optional non-string inputs must remain
	// absent when direct invocation reuses its resolved launch snapshot.
	for _, kind := range []string{"integer", "number", "boolean", "object", "array"} {
		definition.Inputs = append(definition.Inputs, Variable{Name: "optional_" + kind, Type: kind})
	}
	raw, _ := json.Marshal(definition)
	write(root+"/script.json", string(raw))
	source := `import json, os
from pathlib import Path
from agentworks import secret, AutomationError
Path(os.environ['STEP_OUTPUT_DIR'], 'script-started').write_text('started')
assert secret('DECLARED') == os.environ['SECRET_DECLARED']
original_secret = os.environ['SECRET_DECLARED']
os.environ['SECRET_DECLARED'] = original_secret + '-changed'
assert secret('DECLARED') == original_secret + '-changed', 'secret values must not be cached'
os.environ['SECRET_DECLARED'] = original_secret
try: secret('UNDECLARED')
except AutomationError as error: assert 'Declare' in str(error) and os.environ['SECRET_UNDECLARED'] not in str(error)
else: raise AssertionError('undeclared selected secret was admitted')
from helper.values import revision
params = json.loads(os.environ['STEP_PARAMS_JSON'])
assert isinstance(params['count'], int)
assert Path(os.environ['SCRIPT_SOURCE_DIR'], 'template.txt').read_text() == 'template data'
input_record = Path(os.environ['STEP_OUTPUT_DIR'], 'inputs.json')
if not input_record.is_file():
    input_record = Path(os.environ['WORKFLOW_RUN_DIR'], 'inputs.json')
assert json.loads(input_record.read_text()) == params
assert json.loads(Path(os.environ['SCRIPT_SOURCE_DIR'], 'script.json').read_text())['id'] == 'qualify'
assert Path(os.environ['SCRIPT_SOURCE_DIR'], 'nested/blob.bin').read_bytes() == bytes([0, 255, 128, 1])
# PLAT-746-481: a program can verify the actual complete frozen bundle before
# doing work, independently of the synthetic plan revision or mutable source.
import hashlib
metadata_file = Path(os.environ['WORKFLOW_RUN_DIR'], 'run_metadata.json')
metadata = json.loads(metadata_file.read_text()) if metadata_file.is_file() else {}
if metadata.get('source_snapshot_hash'):
    source = Path(os.environ['SCRIPT_SOURCE_DIR'])
    digest = hashlib.sha256()
    for file in sorted((p for p in source.rglob('*') if p.is_file() and '__pycache__' not in p.parts), key=lambda p: p.relative_to(source).as_posix()):
        digest.update(file.relative_to(source).as_posix().encode() + b'\0' + file.read_bytes() + b'\0')
    assert metadata['source_snapshot_hash'] == digest.hexdigest(), metadata
assert not any(key.startswith('optional_') for key in params)
Path(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps({'count':params['count'], 'revision':revision()}))
`
	write(root+"/main.py", source)
	resolver := &savedCrewScriptFixture{directWorkspace: directWorkspace, configuration: CrewScriptConfiguration{Definition: definition, Root: root, Files: map[string][]byte{"main.py": []byte(source), "helper/values.py": []byte("def revision(): return 'first'\n"), "template.txt": []byte("template data")}}}
	resolver.configuration.Files["nested/blob.bin"] = []byte{0, 255, 128, 1}
	// PR1587: depth-first directory traversal orders these differently from
	// globally sorted relative filenames. The program verifies the latter.
	resolver.configuration.Files["nested.py"] = []byte("# Filename ordering regression.\n")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	expectMissingSecret := false
	run := func(workspace string, args map[string]interface{}) string {
		t.Helper()
		previous := map[string]bool{}
		before, _ := os.ReadDir(filepath.Join(docs, workspace, "runs"))
		for _, folder := range before {
			previous[folder.Name()] = true
		}
		cfg := &WorkshopConfig{WorkspacePath: workspace, Logger: loggerv2.NewNoop(), CrewResources: &CrewResources{Scripts: resolver}}
		cfg.WorkspaceEnvRef = map[string]string{"SECRET_DECLARED": secretValue, "SECRET_UNDECLARED": "other-selected-fixture-value"}
		if expectMissingSecret {
			delete(cfg.WorkspaceEnvRef, "SECRET_DECLARED")
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
			if expectMissingSecret && err != nil && strings.Contains(err.Error(), "Select or add DECLARED in Vault") {
				return ""
			}
			t.Fatalf("admission %s %v", out, err)
		}
		for {
			snapshot, found, _ := session.StepRegistry.LatestSnapshotForStep("full-workflow")
			if found && snapshot.Status != WorkshopStepRunning {
				if snapshot.Status != WorkshopStepDone {
					if expectMissingSecret && snapshot.Err != nil && strings.Contains(snapshot.Err.Error(), "Select or add DECLARED in Vault") {
						return ""
					}
					t.Fatalf("script execution: %+v", snapshot)
				}
				if expectMissingSecret {
					t.Fatal("missing declared secret did not refuse before Python")
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
				return filepath.Join(docs, workspace, "runs", folder.Name())
			}
		}
		t.Fatal("no run record")
		return ""
	}
	direct := run(directWorkspace, map[string]interface{}{"inputs": map[string]interface{}{"count": "3"}})
	data, err := os.ReadFile(filepath.Join(direct, "execution/qualify/result.json"))
	if err != nil || !strings.Contains(string(data), `"count": 3`) {
		t.Fatalf("direct result %s %v", data, err)
	}
	resolver.configuration.Files["helper/values.py"] = []byte("def revision(): return 'second'\n")
	// PLAT-746: a converted schedule/function preset must select its saved
	// typed values rather than silently reverting to defaults.
	preset := run(directWorkspace, map[string]interface{}{"input_preset": "second"})
	data, err = os.ReadFile(filepath.Join(preset, "execution/qualify/result.json"))
	if err != nil || !strings.Contains(string(data), `"count": 9`) || preset == direct {
		t.Fatalf("preset result %s %v", data, err)
	}
	previousSource, err := os.ReadFile(filepath.Join(direct, "execution/qualify/code/helper/values.py"))
	if err != nil || !strings.Contains(string(previousSource), "'first'") || !strings.Contains(string(data), `"revision": "second"`) {
		t.Fatalf("next run changed historical source or ignored the edited source: %s %s %v", previousSource, data, err)
	}
	defaults := run(directWorkspace, nil)
	data, err = os.ReadFile(filepath.Join(defaults, "execution/qualify/result.json"))
	if err != nil || !strings.Contains(string(data), `"count": 2`) {
		t.Fatalf("default result %s %v", data, err)
	}
	// PLAT-746-468: equal resolved values cannot identify which preset a
	// person selected; diagnostics and rerun need the saved label as well.
	for folder, expected := range map[string]string{direct: "", preset: "second", defaults: ""} {
		raw, err := os.ReadFile(filepath.Join(folder, "run_metadata.json"))
		var metadata map[string]interface{}
		if err != nil || json.Unmarshal(raw, &metadata) != nil || metadata["input_preset"] != expected {
			t.Fatalf("run lost selected preset: want=%q metadata=%s err=%v", expected, raw, err)
		}
		if hash, _ := metadata["source_snapshot_hash"].(string); len(hash) != 64 {
			t.Fatalf("main Python run lost its frozen bundle hash: %s", raw)
		}
		filepath.WalkDir(folder, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				t.Fatal(err)
			}
			if !entry.IsDir() {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), secretValue) {
					t.Fatalf("environment secret persisted in run file %s", path)
				}
			}
			return nil
		})
	}
	// The backend must refuse before launching even the first source statement.
	startedBefore, _ := filepath.Glob(filepath.Join(docs, directWorkspace, "runs/*/execution/qualify/script-started"))
	expectMissingSecret = true
	run(directWorkspace, nil)
	expectMissingSecret = false
	startedAfter, _ := filepath.Glob(filepath.Join(docs, directWorkspace, "runs/*/execution/qualify/script-started"))
	if len(startedAfter) != len(startedBefore) {
		t.Fatal("missing secret still executed Python before refusing")
	}
	if _, err := os.Stat(filepath.Join(docs, root, "workflow.json")); !os.IsNotExist(err) {
		t.Fatal("script saved a duplicate workflow definition")
	}
}
