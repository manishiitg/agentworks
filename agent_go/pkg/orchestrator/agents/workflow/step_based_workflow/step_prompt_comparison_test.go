package step_based_workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Compare the real renderer and identity assembler against the committed
// parent revision. This runs only on CI; no local build is authorized by
// repository policy. Temporary checkouts and fixture files are removed afterward.
func TestStepPromptBeforeAfter(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("prompt comparison runs on remote verification")
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	root := run("", "git", "rev-parse", "--show-toplevel")
	baseline := run(root, "git", "rev-parse", "HEAD^")
	if baseline == "" {
		t.Fatal("parent baseline is missing")
	}
	old := filepath.Join(t.TempDir(), "before")
	run(root, "git", "worktree", "add", "--detach", old, baseline)
	defer func() {
		cmd := exec.Command("git", "worktree", "remove", "--force", old)
		cmd.Dir = root
		_ = cmd.Run()
	}()
	rel := filepath.Join("agent_go", "pkg", "orchestrator", "agents", "workflow", "step_based_workflow", "prompt_size_remote_fixture_test.go")
	for _, item := range []struct{ label, dir string }{{"before", old}, {"after", root}} {
		file := filepath.Join(item.dir, rel)
		if err := os.WriteFile(file, []byte(stepPromptSizeRemoteFixture), 0600); err != nil {
			t.Fatal(err)
		}
		out := run(filepath.Join(item.dir, "agent_go"), "go", "test", "./pkg/orchestrator/agents/workflow/step_based_workflow", "-run", "^TestPrintRemotePromptFixture$", "-v", "-count=1")
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "PROMPT_SIZE ") || strings.Contains(line, "PROMPT_REVIEW ") {
				t.Logf("%s %s", item.label, strings.TrimSpace(line))
			}
		}
	}
}

const stepPromptSizeRemoteFixture = `package step_based_workflow
import (
 "encoding/json"
 "strings"
 "testing"
 "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
)
func TestPrintRemotePromptFixture(t *testing.T) {
 c:=newAgentClosingTestOrchestrator(t)
 cfg:=&agents.OrchestratorAgentConfig{}
 identity,supplements:=c.assembleStepRuntimeIdentity(cfg,nil,[]string{"query_workflow_db","mutate_workflow_db","agent_browser"},false,true)
 var names []string
 for _, s:=range identity {names=append(names,s.Name)}
 for _, role:=range []string{"direct","manager","child"} {
  vars:=map[string]string{"DescriptionFirst":"true","StepTitle":"Research","DBAccess":DBAccessReadWrite,"KbAccess":KBAccessReadWrite,"KbAccessLabel":"read-write","WorkspacePath":"/workspace/Workflow/demo/runs/iteration-0/default/execution","WorkflowRoot":"/workspace/Workflow/demo","StepExecutionPath":"/workspace/Workflow/demo/runs/iteration-0/default/execution/research","DBPath":"/workspace/Workflow/demo/db","KnowledgebasePath":"/workspace/Workflow/demo/knowledgebase","FolderGuardReadPaths":"/workspace/Workflow/demo/soul, /workspace/Workflow/demo/db/README.md, /workspace/Workflow/demo/knowledgebase, /workspace/Workflow/demo/learnings, /workspace/Workflow/demo/runs/iteration-0/default/execution/fetch","FolderGuardWritePaths":"/workspace/Workflow/demo/runs/iteration-0/default/execution/research, /workspace/Workflow/demo/knowledgebase/notes","StepSystemRules":"Use verified sources only.","FollowUpMessage":"Research current facts.\n\n## Rules\nUse verified sources only.","StepContextOutput":"result.json","PlanPosition":"Goal: Produce verified evidence.\nPosition: step 2 of 3.\nBefore: Fetch evidence — Verified raw.json.\nAfter: Report.","StepContextDependencies":"/workspace/Workflow/demo/runs/iteration-0/default/execution/fetch/raw.json"}
  if role=="manager" {vars["PredefinedRoutes"]="- Worker (worker) — type: agent"}
  if role=="child" {vars["PlanPosition"]+="\nParent: Research manager\nDelegated task: Verify source evidence."}
  vars["KBGuidanceBlock"]=BuildStepKBGuidanceWithTarget(KBAccessReadWrite,"Record verified audience facts.","/workspace/Workflow/demo/knowledgebase/notes")
  core:=(&WorkflowExecutionOnlyAgent{}).executionOnlySystemPromptProcessor(vars)
  total:=core+strings.Join(supplements,"\n")
  rendered,_:=json.Marshal(core)
  t.Logf("PROMPT_REVIEW role=%s core=%s",role,rendered)
  user,_:=json.Marshal((&WorkflowExecutionOnlyAgent{}).executionOnlyUserMessageProcessor(vars))
  t.Logf("PROMPT_REVIEW role=%s user=%s",role,user)
  t.Logf("PROMPT_SIZE role=%s core_chars=%d system_and_supplements_chars=%d skills=%v",role,len([]rune(core)),len([]rune(total)),names)
 }
}
`
