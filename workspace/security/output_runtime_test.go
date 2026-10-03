package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStructuredOutputHelper(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "agentworks_output.py"), outputRuntime, 0600); err != nil {
		t.Fatal(err)
	}
	script := `import json, os, pathlib, stat
from agentworks_output import set_output
root = pathlib.Path(os.environ["TEST_OUTPUT_ROOT"])
os.environ["STEP_OUTPUT_DIR"] = str(root)
set_output({"text":"café", "nested":{"ok":True}, "items":[1,None]})
result = root / "result.json"
assert json.loads(result.read_text()) == {"text":"café", "nested":{"ok":True}, "items":[1,None]}
assert stat.S_IMODE(result.stat().st_mode) == 0o660
previous = result.read_bytes()
for invalid in [float("nan"), float("inf"), object()]:
    try:
        set_output(invalid)
    except (ValueError, TypeError):
        pass
    else:
        raise AssertionError("invalid JSON accepted")
    assert result.read_bytes() == previous
assert not list(root.glob(".result-*"))
set_output(["replacement"])
assert json.loads(result.read_text()) == ["replacement"]
for invalid_dir in ["", "relative/path"]:
    os.environ["STEP_OUTPUT_DIR"] = invalid_dir
    try:
        set_output({"ok":True})
    except RuntimeError:
        pass
    else:
        raise AssertionError("unassigned output allowed")
os.environ["STEP_OUTPUT_DIR"] = str(root / "missing")
try:
    set_output({"ok":True})
except FileNotFoundError:
    pass
else:
    raise AssertionError("write failure swallowed")
assert not list(root.glob(".result-*"))
`
	cmd := exec.Command(python, "-B", "-c", script)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+root, "TEST_OUTPUT_ROOT="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("structured output helper: %v\n%s", err, output)
	}
}
