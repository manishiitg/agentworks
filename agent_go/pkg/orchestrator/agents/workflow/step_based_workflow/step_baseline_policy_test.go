package step_based_workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Exercise the actual workspace reader and baseline registration. Explicit
// saved lists must not resurrect administrative tools, and a database alone
// must not make an agent believe it has a goal or a running Pulse.
func TestStepBaselineRolePolicy(t *testing.T) {
	c := newAgentClosingTestOrchestrator(t)
	manifest, soul := `{}`, ""
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var content string
		switch {
		case strings.HasSuffix(r.URL.Path, "/workflow.json"):
			content = manifest
		case strings.HasSuffix(r.URL.Path, "/soul/soul.md"):
			content = soul
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"content": content}})
	}))
	defer s.Close()
	c.WorkspaceClient = workspace.NewClient(s.URL)
	names := []string{"execute_shell_command", "human_feedback", "create_human_input_request", "query_workflow_db", "mutate_workflow_db", "notify_user", "query_workflow_costs", "apply_workflow_db_migration", "list_functions", "call_function", "get_function_call", "reply_function_call", "get_goal_metrics", "record_goal_observations", "ask_pulse", "ask_goal_lead"}
	c.WorkspaceToolExecutors = map[string]interface{}{}
	cfg := &AgentConfigs{}
	for _, name := range names {
		c.WorkspaceTools = append(c.WorkspaceTools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}})
		c.WorkspaceToolExecutors[name] = name
		cfg.EnabledCustomTools = append(cfg.EnabledCustomTools, "workflow:"+name)
	}
	for _, test := range []struct {
		name, manifest, soul string
		goal, pulse          bool
	}{
		{"no goal", `{}`, "", false, false},
		{"goal without Pulse", `{}`, "## Objective\nProduce verified research.\n## Success Criteria\nEvidence checked.", true, false},
		{"goal with Pulse", `{"pulse":{"enabled":true}}`, "## Objective\nProduce verified research.", true, true},
		{"Relay", `{"kind":"relay","pulse":{"enabled":true}}`, "## Objective\nTransform input.", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, soul = test.manifest, test.soul
			if err := c.loadCodeLayout(t.Context()); err != nil {
				t.Fatal(err)
			}
			tools, executors := c.prepareCustomTools(cfg)
			got := registeredToolNames(tools)
			for _, name := range names[5:12] {
				if slices.Contains(got, name) || executors[name] != nil {
					t.Fatalf("ordinary agent retained %s", name)
				}
			}
			for _, name := range []string{"execute_shell_command", "human_feedback", "create_human_input_request"} {
				if !slices.Contains(got, name) || executors[name] != name {
					t.Fatalf("baseline lost %s", name)
				}
			}
			if slices.Contains(got, "get_goal_metrics") != test.goal || slices.Contains(got, "record_goal_observations") != test.goal || slices.Contains(got, "ask_pulse") != test.pulse || slices.Contains(got, "ask_goal_lead") != test.pulse {
				t.Fatalf("wrong goal/Pulse surface: %v", got)
			}
		})
	}
}

// The same fixture is measured before and after procedural extraction by the
// remote verifier. Counts refer to the rendered system core, not template size.
func TestStepPromptProcedureSize(t *testing.T) {
	vars := map[string]string{"DescriptionFirst": "true", "StepTitle": "Research", "DBAccess": DBAccessReadWrite, "KbAccess": KBAccessReadWrite, "KbAccessLabel": "read-write", "WorkspacePath": "/workspace/Workflow/demo/runs/iteration-0/default/execution", "WorkflowRoot": "/workspace/Workflow/demo", "StepExecutionPath": "/workspace/Workflow/demo/runs/iteration-0/default/execution/research", "DBPath": "/workspace/Workflow/demo/db", "KnowledgebasePath": "/workspace/Workflow/demo/knowledgebase", "FolderGuardReadPaths": "/workspace/Workflow/demo/soul, /workspace/Workflow/demo/db/README.md, /workspace/Workflow/demo/knowledgebase", "FolderGuardWritePaths": "/workspace/Workflow/demo/runs/iteration-0/default/execution/research", "StepContextOutput": "result.json"}
	vars["KBGuidanceBlock"] = BuildStepKBGuidanceWithTarget(KBAccessReadWrite, "Record verified audience facts.", "/workspace/Workflow/demo/knowledgebase/notes")
	for _, mode := range []string{"false", "true"} {
		vars["IsCodeExecutionMode"] = mode
		prompt := (&WorkflowExecutionOnlyAgent{}).executionOnlySystemPromptProcessor(vars)
		t.Logf("rendered step core code_execution=%s: %d characters", mode, len([]rune(prompt)))
	}
}
