package security

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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

// PLAT-746: converted browser scripts imported agentworks_db successfully but
// failed importing agentworks_playwright; host-installed packages hid the gap.
func TestScriptImportsBundledBrowserHelperWithoutHostPackages(t *testing.T) {
	if runtime.GOOS == "linux" {
		if _, ok := CLILandlockRunner(); !ok {
			t.Skip("needs a configured Landlock launcher; actual slot proof runs in the Linux harness")
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	assets := t.TempDir()
	build := exec.Command(python, "../../scripts/build-playwright-packages.py", assets)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("browser package build: %v\n%s", err, output)
	}
	t.Setenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR", assets)
	t.Setenv("NATIVE_WORKSPACE", "true")
	root := t.TempDir()
	iso := &Isolator{BaseDir: root, WorkDir: root, ReadPaths: []string{root}, WritePaths: []string{root}}
	if slot := os.Getenv("AGENTWORKS_BROWSER_HELPER_SLOT_E2E"); slot != "" {
		iso.Slot = slot
		// Match a prepared project's inherited group, not private test scratch.
		for _, dir := range []string{root, filepath.Dir(root)} {
			if err := os.Chmod(dir, os.ModeSetgid|0770); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// -S removes host site-packages: both modules must come from this command's
	// existing immutable helper directory, as in the reported import sequence.
	program := `import os,sys
from pathlib import Path
import agentworks_db
from agentworks_playwright import attach_live_browser, attach_live_browser_async
from agentworks_playwright.live import _endpoint
assert 'websocket' not in sys.modules
import websocket
import agentworks_playwright
helper=Path(agentworks_db.__file__).parent
assert Path(agentworks_playwright.__file__).parent.parent==helper
assert Path(websocket.__file__).parent.parent==helper
assert callable(attach_live_browser) and callable(attach_live_browser_async)
assert _endpoint('https://example.test/s/run', 'fixture-token')[0].startswith('wss://example.test/s/run/')
print('browser-helper-import-ok')
`
	if iso.Slot != "" {
		program += "\nimport pwd\nassert pwd.getpwuid(os.geteuid()).pw_name==" + strconv.Quote(iso.Slot) + "\n"
	}
	script := filepath.Join(root, "browser-import.py")
	if err := os.WriteFile(script, []byte(program), 0640); err != nil {
		t.Fatal(err)
	}
	// ExecuteIsolated accepts shell words, rather than a raw exec argv.
	quotedScript := "'" + strings.ReplaceAll(script, "'", "'\"'\"'") + "'"
	command, cleanup, err := iso.ExecuteIsolated(ctx, python, []string{"-S", "-B", quotedScript})
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil || strings.TrimSpace(stdout.String()) != "browser-helper-import-ok" {
		t.Fatalf("isolated helper import: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	t.Setenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR", filepath.Join(assets, "missing"))
	if err := stagePythonPlaywrightRuntime(t.TempDir(), 0600); err == nil || !strings.Contains(err.Error(), "update the app") {
		t.Fatalf("configured missing browser package was hidden: %v", err)
	}
}
