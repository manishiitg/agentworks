package step_based_workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestDirectExecutionLoadsCodeLayoutWithoutBuilder(t *testing.T) {
	for _, version := range []int{0, 1, 99} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			base := newFakeWorkspaceAPIWithContent(t, map[string]string{
				"Workflow/instagram/workflow.json":      fmt.Sprintf(`{"code_layout_version":%d}`, version),
				"Workflow/instagram/planning/plan.json": `{"steps":[]}`,
			})
			c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, variableManager: NewVariableManager(base)}
			// Exercise the real entry point; deliberately stop at an empty plan,
			// before agents are created. No Builder/ReadCurrentPlan warm-up.
			_, err := c.CreateTodoList(context.Background(), "test", "Workflow/instagram")
			if version == 99 {
				if err == nil || !strings.Contains(err.Error(), "unsupported code_layout_version") {
					t.Fatalf("unknown layout accepted: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "no steps") {
				t.Fatalf("did not reach plan validation: %v", err)
			}
			want := "learnings/auth"
			if version == 1 {
				want = "code/auth"
			}
			if got := c.scriptedSourceDir("auth"); got != want {
				t.Fatalf("direct execution selected %s, want %s", got, want)
			}
			if env := c.codeRuntimeEnv(nil); version == 1 && (env["WORKFLOW_CODE_DEPS"] == "" || env["PYTHONPATH"] == "") {
				t.Fatal("direct execution lost dependency paths")
			}
		})
	}
}

func codeLayoutController(t *testing.T, version int32) *StepBasedWorkflowOrchestrator {
	t.Helper()
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/testing")
	c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0/group"}
	c.codeLayoutVersion.Store(version)
	return c
}

func TestCodeLayoutSourceCompatibility(t *testing.T) {
	for _, version := range []int32{0, 1} {
		c := codeLayoutController(t, version)
		source, working := "learnings/step", "Workflow/testing/runs/run/execution/step/code"
		if version == 1 {
			source, working = "code/step", "Workflow/testing/code/step"
		}
		if c.scriptedSourceDir("step") != source || c.scriptedWorkingDir("step", "Workflow/testing/runs/run/execution/step") != working {
			t.Fatalf("version=%d source/working mismatch", version)
		}
	}
	for _, tc := range []struct{ manifest, want string }{{`{}`, "learnings/step"}, {`{"code_layout_version":1}`, "code/step"}} {
		got := savedCodeDirectory(context.Background(), "Workflow/testing", "step", func(context.Context, string) (string, error) { return tc.manifest, nil })
		if got != tc.want {
			t.Fatalf("review source=%s want=%s", got, tc.want)
		}
	}
}

func TestCodeLayoutVersionOneNeverFallsBackToLegacyScript(t *testing.T) {
	base := newFakeWorkspaceAPIWithContent(t, map[string]string{
		"Workflow/testing/learnings/step/main.py": "print('legacy')",
	})
	base.SetWorkspacePath("Workflow/testing")
	c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	c.codeLayoutVersion.Store(1)
	if c.hasValidLearnedScriptAPI(context.Background(), "step") {
		t.Fatal("version 1 accepted a script that exists only in the legacy learnings tree")
	}

	base = newFakeWorkspaceAPIWithContent(t, map[string]string{
		"Workflow/testing/code/step/main.py": "print('canonical')",
	})
	base.SetWorkspacePath("Workflow/testing")
	c = &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	c.codeLayoutVersion.Store(1)
	if !c.hasValidLearnedScriptAPI(context.Background(), "step") {
		t.Fatal("version 1 did not resolve the canonical code tree")
	}
}

func TestCodeLayoutGrantsRespectLock(t *testing.T) {
	c := codeLayoutController(t, 1)
	// code/ is writable only for a run the Builder started; lock_code is an extra lock on top.
	c.codeWritableByRun.Store(true)
	for _, locked := range []bool{false, true} {
		reads, writes := c.setupExecutionFolderGuard("step", "out", KBAccessNone, LearningsAccessNone, resolveDBAccess(nil), &AgentConfigs{LockCode: &locked})
		if !slices.Contains(reads, "Workflow/testing/code") {
			t.Fatal("missing code read grant")
		}
		if slices.Contains(writes, "Workflow/testing/code") == locked {
			t.Fatalf("wrong code write grant for lock=%v", locked)
		}
		for _, p := range append(reads, writes...) {
			if p == "Workflow" || p == "Workflow/other/code" {
				t.Fatalf("overbroad grant %s", p)
			}
		}
	}
}

// Execute the actual runner command with nested shared imports and persistent
// outputs, then repair a helper in place. No execution bundle is created.
func TestCodeLayoutCanonicalPythonRepair(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	c := codeLayoutController(t, 1)
	env := c.codeRuntimeEnv(nil)
	code := env["WORKFLOW_CODE_ROOT"]
	step := filepath.Join(code, "step")
	helper := filepath.Join(code, "shared", "nested")
	output := filepath.Join(root, "runs", "result.txt")
	for _, dir := range []string{step, helper, filepath.Dir(output)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	main := filepath.Join(step, "main.py")
	source := "import os, pathlib, sys\nfrom shared.nested.utils import value\npathlib.Path(os.environ['STEP_OUTPUT_DIR'], 'result.txt').write_text(value + sys.argv[1])\n"
	if err := os.WriteFile(main, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "repaired"} {
		if err := os.WriteFile(filepath.Join(helper, "utils.py"), []byte("value = '"+value+"'\n"), 0644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("/bin/sh", "-c", scriptedCommand(main, []string{" argument with spaces"}))
		cmd.Dir = step
		cmd.Env = append(os.Environ(), "STEP_OUTPUT_DIR="+filepath.Dir(output))
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("runner: %v\n%s", err, out)
		}
		got, err := os.ReadFile(output)
		if err != nil || string(got) != value+" argument with spaces" {
			t.Fatalf("output=%s err=%v", got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "code")); !os.IsNotExist(err) {
		t.Fatal("unexpected execution copy")
	}
}

func TestCodeLayoutRuntimeEnvAndPrompt(t *testing.T) {
	c := codeLayoutController(t, 1)
	env := c.codeRuntimeEnv(map[string]string{"VAR_BASE_URL": "https://example.test"})
	if env["VAR_BASE_URL"] != "https://example.test" || !strings.HasSuffix(env["WORKFLOW_CODE_ROOT"], "Workflow/testing/code") || !strings.Contains(env["PYTHONPATH"], env["WORKFLOW_CODE_DEPS"]) {
		t.Fatalf("incorrect runtime env keys")
	}
	prompt := GetScriptedModeInstructions("/docs/Workflow/testing/code/step", "/docs/Workflow/testing/runs/output", false, "", "", nil, nil, nil, "", false, false, true, true)
	if strings.Contains(prompt, "To test, just run") || !strings.Contains(prompt, "controller executes") {
		t.Fatal("canonical prompt must use controller execution")
	}
}
