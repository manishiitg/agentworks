package pythontools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDefinition = `{"description":"Look up a customer","parameters":{"type":"object","properties":{"id":{"type":"string","minLength":1}},"required":["id"],"additionalProperties":false},"timeout_seconds":2}`

func fixture(t *testing.T, definition, source string) (Tool, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "customer's tool")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "main.py"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	tools, err := Load(context.Background(), []string{"python_tools:lookup_customer"}, func(_ context.Context, filename string) (string, error) {
		if strings.HasSuffix(filename, "tool.json") {
			return definition, nil
		}
		return source, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tools[0], directory
}

// This test adapter executes the generated command locally; production Bind
// receives only the existing guarded workspace executor, never os/exec.
func testShell(ctx context.Context, args map[string]interface{}) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(args["timeout"].(int))*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", "exec "+args["command"].(string))
	command.Dir = args["working_directory"].(string)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	exitCode := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", err
		}
		exitCode = exit.ExitCode()
	}
	raw, err := json.Marshal(map[string]interface{}{"stdout": stdout.String(), "stderr": stderr.String(), "exit_code": exitCode})
	return string(raw), err
}

func TestPythonToolReturnsJSONAndTreatsInputAsData(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	tool, directory := fixture(t, testDefinition, `print("import log")
def run(input):
    print("lookup log")
    return {"id": input["id"], "customer": "Zoë", "items": [True, None, 3]}
`)
	input := `'; $(touch should_not_exist); \"` + "\n" + "hello"
	value, err := tool.Bind(testShell, filepath.Join(directory, "main.py"), directory)(context.Background(), map[string]interface{}{"id": input})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(value), &result); err != nil || result["id"] != input || result["customer"] != "Zoë" {
		t.Fatalf("unexpected JSON result %q: %v", value, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "should_not_exist")); !os.IsNotExist(err) {
		t.Fatal("input was interpreted as shell code")
	}
	if _, err := os.Stat(filepath.Join(directory, "__pycache__")); !os.IsNotExist(err) {
		t.Fatal("tool created bytecode beside immutable source")
	}
}

func TestPythonToolValidatesArgumentsBeforeSandboxCall(t *testing.T) {
	tool, directory := fixture(t, testDefinition, "def run(input): return input\n")
	calls := 0
	execute := tool.Bind(func(context.Context, map[string]interface{}) (string, error) {
		calls++
		return `{"stdout":"{}","exit_code":0}`, nil
	}, filepath.Join(directory, "main.py"), directory)
	for _, args := range []map[string]interface{}{nil, {}, {"id": ""}, {"id": 2}, {"id": "ok", "extra": true}, {"id": strings.Repeat("x", maxInputBytes)}} {
		if _, err := execute(context.Background(), args); err == nil {
			t.Errorf("invalid arguments accepted: %v", args)
		}
	}
	if calls != 0 {
		t.Fatalf("sandbox was called for invalid inputs: %d", calls)
	}
}

func TestPythonToolPropagatesFailureAndCancellation(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	for name, source := range map[string]string{
		"exception":   "def run(input): raise RuntimeError('database unavailable')\n",
		"non-json":    "def run(input): return {1, 2}\n",
		"nan":         "def run(input): return float('nan')\n",
		"missing-run": "print('no entry point')\n",
	} {
		t.Run(name, func(t *testing.T) {
			tool, directory := fixture(t, testDefinition, source)
			_, err := tool.Bind(testShell, filepath.Join(directory, "main.py"), directory)(context.Background(), map[string]interface{}{"id": "x"})
			if err == nil || !strings.Contains(err.Error(), "failed (exit") {
				t.Fatalf("Python failure was swallowed: %v", err)
			}
		})
	}
	tool, directory := fixture(t, testDefinition, "import time\ndef run(input): time.sleep(10)\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := tool.Bind(testShell, filepath.Join(directory, "main.py"), directory)(ctx, map[string]interface{}{"id": "x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}

func TestPythonToolRejectsInvalidOrTruncatedSandboxResult(t *testing.T) {
	tool, directory := fixture(t, testDefinition, "def run(input): return input\n")
	for _, response := range []string{`{}`, `{"stdout":"{truncated","exit_code":0}`, `{"stdout":"{}","exit_code":1,"stderr":"boom"}`, `{"stdout":"{}","exit_code":0,"error":"SANDBOX_UNAVAILABLE"}`} {
		_, err := tool.Bind(func(context.Context, map[string]interface{}) (string, error) { return response, nil }, filepath.Join(directory, "main.py"), directory)(context.Background(), map[string]interface{}{"id": "x"})
		if err == nil {
			t.Errorf("bad sandbox response accepted: %s", response)
		}
	}
}

func TestPythonToolSelectionsAndDefinitions(t *testing.T) {
	for _, selection := range []string{"python_tools:*", "python_tools:../other", "python_tools:/tmp/tool", "python_tools:mcp_private", "python_tools:UPPER", "python_tools:"} {
		if _, err := SelectedNames([]string{selection}); err == nil {
			t.Errorf("invalid selection accepted: %q", selection)
		}
	}
	readCalls := 0
	if tools, err := Load(context.Background(), []string{"workspace_advanced:*"}, func(context.Context, string) (string, error) { readCalls++; return "", nil }); err != nil || len(tools) != 0 || readCalls != 0 {
		t.Fatal("agents without Python tools must remain unchanged")
	}
	for name, definition := range map[string]string{
		"bad-json":        "{",
		"trailing":        testDefinition + " {}",
		"unknown-command": `{"description":"x","parameters":{"type":"object"},"command":"host"}`,
		"array-schema":    `{"description":"x","parameters":{"type":"array"}}`,
		"external-ref":    `{"description":"x","parameters":{"type":"object","properties":{"id":{"$ref":"file:///etc/passwd"}}}}`,
		"bad-schema":      `{"description":"x","parameters":{"type":"object","required":"id"}}`,
		"timeout":         `{"description":"x","parameters":{"type":"object"},"timeout_seconds":301}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(context.Background(), []string{"python_tools:lookup_customer"}, func(_ context.Context, p string) (string, error) {
				if strings.HasSuffix(p, "tool.json") {
					return definition, nil
				}
				return "def run(input): return input", nil
			}); err == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
	if _, err := Load(context.Background(), []string{"python_tools:lookup_customer"}, func(_ context.Context, p string) (string, error) {
		if strings.HasSuffix(p, "tool.json") {
			return testDefinition, nil
		}
		return "", errors.New("not found")
	}); err == nil {
		t.Fatal("missing main.py accepted")
	}
}
