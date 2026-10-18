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
	call := &crewFunctionCall{ID: "fn-published-path-regression", TargetPath: "Crew/example", Publication: &publishedProject{Root: "Crew/example", OwnerID: "owner", WorkspaceID: "example", AgentID: "agent", Revision: "revision"}, ResultSchema: map[string]interface{}{"type": "object"}}
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
	cwd := t.TempDir()
	publicationEnv := publicationShellEnvironment(call.Publication)
	source := publicationEnv["AGENTWORKS_PUBLICATION_SOURCE"]
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
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
	call.ResultSchema = nil
	if optional := structuredFunctionOutputInstructions(call); !strings.Contains(optional, env["FUNCTION_OUTPUT_DIR"]) || strings.Contains(optional, "result.json") {
		t.Fatal("optional result lost the output directory or required a JSON result")
	}
	// Run a real shell in a different linked-source CWD, as a native CLI does.
	cmd := exec.Command("sh", "-c", `test -d "$AGENTWORKS_PUBLICATION_SOURCE" && mkdir -p "$AGENTWORKS_PUBLICATION_RUNTIME/reports" && printf runtime > "$AGENTWORKS_PUBLICATION_RUNTIME/reports/proof.txt" && cat "$1" > "$FUNCTION_RESULT_FILE"`, "sh", reference)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "FUNCTION_RESULT_FILE="+env["FUNCTION_RESULT_FILE"])
	for name, value := range publicationEnv {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native paths: %v %s", err, output)
	}
	workingResult, err := os.ReadFile(filepath.Join(publicationEnv["AGENTWORKS_PUBLICATION_RUNTIME"], "reports", "proof.txt"))
	if err != nil || string(workingResult) != "runtime" {
		t.Fatalf("published runtime output %q: %v", workingResult, err)
	}
	if _, err := os.Stat(filepath.Join(source, "reports")); !os.IsNotExist(err) {
		t.Fatal("working result entered immutable source")
	}
	result, err := os.ReadFile(env["FUNCTION_RESULT_FILE"])
	if err != nil || string(result) != `{"marker":"offline"}` {
		t.Fatalf("result %q: %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(call.resultRelativePath()))); !os.IsNotExist(err) {
		t.Fatal("result was written in CLI state")
	}
}
