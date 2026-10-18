package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Pin the live failure: project/ points at published source, so a native
// command must use live input/output paths rather than paths under its CWD.
func TestPublishedFunctionPathsAreIndependentOfCLIWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the deployment CLI uses a Linux shell")
	}
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	call := &crewFunctionCall{ID: "fn-published-path-regression", TargetPath: "Crew/example", Publication: &publishedProject{Root: "Crew/example"}, ResultSchema: map[string]interface{}{"type": "object"}}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	t.Cleanup(func() { crewFunctionCalls.Lock(); delete(crewFunctionCalls.m, call.ID); crewFunctionCalls.Unlock() })
	rel := "triggers/deliveries/example.json"
	input := filepath.Join(docs, "Crew", "example", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(input), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte(`{"marker":"offline"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cwd, source := t.TempDir(), t.TempDir()
	if err := os.Symlink(source, filepath.Join(cwd, "project")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "project", filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Fatal("source unexpectedly contains live input")
	}
	reference := publishedFunctionPayloadReference(call.ID, call.TargetPath+"/"+rel, rel)
	if reference != input || !strings.Contains(triggerTurnMessage("Run", "Function", reference), "`"+input+"`") {
		t.Fatalf("input reference = %q", reference)
	}
	if got := publishedFunctionPayloadReference("", call.TargetPath+"/"+rel, rel); got != rel {
		t.Fatalf("ordinary trigger reference changed: %s", got)
	}
	env := structuredFunctionShellEnvironment(call)
	if err := os.MkdirAll(env["FUNCTION_OUTPUT_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	instructions := structuredFunctionOutputInstructions(call)
	if !strings.Contains(instructions, "`"+env["FUNCTION_RESULT_FILE"]+"`") || !strings.Contains(instructions, "Crew-relative") {
		t.Fatal("output guidance omits absolute and bridge paths")
	}
	// Run a real shell in a different linked-source CWD, as a native CLI does.
	cmd := exec.Command("sh", "-c", `cat "$1" > "$FUNCTION_RESULT_FILE"`, "sh", reference)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "FUNCTION_RESULT_FILE="+env["FUNCTION_RESULT_FILE"])
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native paths: %v %s", err, output)
	}
	result, err := os.ReadFile(env["FUNCTION_RESULT_FILE"])
	if err != nil || string(result) != `{"marker":"offline"}` {
		t.Fatalf("result %q: %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(call.resultRelativePath()))); !os.IsNotExist(err) {
		t.Fatal("result was written in CLI state")
	}
}
