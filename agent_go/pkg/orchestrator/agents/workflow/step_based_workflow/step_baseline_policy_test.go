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
	names := []string{"execute_shell_command", "human_feedback", "create_human_input_request", "query_workflow_db", "mutate_workflow_db", "query_workflow_costs", "apply_workflow_db_migration", "list_functions", "call_function", "get_function_call", "reply_function_call", "get_goal_metrics", "record_goal_observations", "send_email", "send_slack_message"}
	c.WorkspaceToolExecutors = map[string]interface{}{}
	cfg := &AgentConfigs{}
	for _, name := range names {
		c.WorkspaceTools = append(c.WorkspaceTools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}})
		c.WorkspaceToolExecutors[name] = name
		if name != "send_email" && name != "send_slack_message" {
			cfg.EnabledCustomTools = append(cfg.EnabledCustomTools, "workflow:"+name)
		}
	}
	for _, test := range []struct {
		name, manifest, soul string
		goal                 bool
	}{
		{"no goal", `{}`, "", false},
		{"goal", `{}`, "## Objective\nProduce verified research.\n## Success Criteria\nEvidence checked.", true},
		{"Relay", `{"kind":"relay"}`, "## Objective\nTransform input.", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, soul = test.manifest, test.soul
			if err := c.loadCodeLayout(t.Context()); err != nil {
				t.Fatal(err)
			}
			tools, executors := c.prepareCustomTools(cfg)
			managerTools, managerExecutors := c.prepareOrchestratorCustomTools(cfg, c.resolveDBAccess(cfg))
			for _, name := range names[5:11] {
				if slices.Contains(registeredToolNames(managerTools), name) || managerExecutors[name] != nil {
					t.Fatalf("manager re-added %s", name)
				}
			}
			got := registeredToolNames(tools)
			for _, name := range names[5:11] {
				if slices.Contains(got, name) || executors[name] != nil {
					t.Fatalf("ordinary agent retained %s", name)
				}
			}
			for _, name := range []string{"execute_shell_command", "human_feedback", "create_human_input_request"} {
				if !slices.Contains(got, name) || executors[name] != name || !slices.Contains(registeredToolNames(managerTools), name) || managerExecutors[name] != name {
					t.Fatalf("baseline lost %s", name)
				}
			}
			// Email and Slack are not baseline tools: only a step that names them gets them.
			for _, name := range []string{"send_email", "send_slack_message"} {
				if slices.Contains(got, name) || executors[name] != nil {
					t.Fatalf("baseline includes %s", name)
				}
			}
			if slices.Contains(got, "get_goal_metrics") != test.goal || slices.Contains(got, "record_goal_observations") != test.goal {
				t.Fatalf("wrong Goal measurement surface: %v", got)
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
		if len([]rune(prompt)) > 9000 {
			t.Fatalf("step core exceeds 9000 characters: %d", len([]rune(prompt)))
		}
		for _, procedure := range []string{"param_sets:", "**Topic ID conventions:**", "never place SQL containing single quotes"} {
			if strings.Contains(prompt, procedure) {
				t.Fatalf("core contains procedural body: %s", procedure)
			}
		}
		t.Logf("rendered step core code_execution=%s: %d characters", mode, len([]rune(prompt)))
	}
}
