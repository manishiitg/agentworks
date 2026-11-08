package security

import (
	"os"
	"os/exec"
	"testing"
)

// PLAT-746-431: child arguments are typed JSON, not per-field VAR_* exports.
func TestPythonAutomationInputsAreOptionalObjectsAndIsolated(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	root := t.TempDir()
	if err := stagePythonAutomationRuntime(root, 0600); err != nil {
		t.Fatal(err)
	}
	script := `import importlib, json, os
os.environ['WORKFLOW_RUN_DIR'] = os.getcwd()
os.environ.pop('STEP_PARAMS_JSON', None)
import agentworks
assert dict(agentworks.inputs) == {}
for empty in ('', '   '):
    os.environ['STEP_PARAMS_JSON'] = empty
    importlib.reload(agentworks)
    assert dict(agentworks.inputs) == {}
parent = {'count': 7, 'nested': {'items': [1, 2]}, 'enabled': True}
os.environ['STEP_PARAMS_JSON'] = json.dumps(parent)
importlib.reload(agentworks)
assert dict(agentworks.inputs) == parent
try:
    agentworks.inputs['count'] = 9
except TypeError:
    pass
else:
    raise AssertionError('inputs allowed a top-level write')
agentworks.inputs['nested']['items'].append(3)
assert parent['nested']['items'] == [1, 2]
assert json.loads(os.environ['STEP_PARAMS_JSON']) == parent
importlib.reload(agentworks)
assert agentworks.inputs['nested']['items'] == [1, 2]
for value in ('null', '[]', '7', '"text"', 'true', '{broken'):
    os.environ['STEP_PARAMS_JSON'] = value
    try:
        importlib.reload(agentworks)
    except agentworks.AutomationError as error:
        assert str(error) == 'Script inputs must be a JSON object.'
    else:
        raise AssertionError('invalid inputs admitted: ' + value)
`
	command := exec.Command(python, "-B", "-c", script)
	command.Env = append(os.Environ(), "PYTHONPATH="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("script inputs: %v\n%s", err, output)
	}
}
