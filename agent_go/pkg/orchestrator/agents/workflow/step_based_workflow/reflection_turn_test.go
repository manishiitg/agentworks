package step_based_workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// Exercise the real metadata assembler and installed reader, plus the request's
// contribution gates. This replaces checks that restated every sentence in the
// formerly inline procedure body.
func TestReflectionRequestLoadsProceduresOnDemand(t *testing.T) {
	c := newAgentClosingTestOrchestrator(t)
	identity, supplements := c.assembleStepRuntimeIdentity(&agents.OrchestratorAgentConfig{}, nil, false, []string{"query_workflow_db"}, false, false)
	found := false
	for _, skill := range identity {
		found = found || skill.Name == "workflow-reflection"
	}
	if !found {
		t.Fatal("reflection procedure skill was not attached")
	}
	guide, err := skills.NewInstalledSkillReader("", c.GetWorkspacePath())("workflow-reflection", "SKILL.md")
	if err != nil || !strings.Contains(guide.Content, "diff_patch_workspace_file") || !strings.Contains(guide.Content, "organized by topic") {
		t.Fatalf("reflection procedures cannot be opened: %v", err)
	}
	if strings.Contains(strings.Join(supplements, "\n"), guide.Content) {
		t.Fatal("reflection procedures were injected eagerly")
	}
	for _, mode := range []string{"learnings", "knowledgebase", "both", "none", "kb-without-write"} {
		t.Run(mode, func(t *testing.T) {
			in := StepReflectionTurnInput{StepID: "research", LearningsTargetPath: "/workspace/Workflow/example/learnings/_global", KBNotesTargetPath: "/workspace/Workflow/example/knowledgebase/notes"}
			if mode == "learnings" || mode == "both" {
				in.LearningObjective = "Record verified source lookup technique."
			}
			if mode == "knowledgebase" || mode == "both" || mode == "kb-without-write" {
				in.KBContribution = "Record verified domain context."
				in.KBAccess = KBAccessReadWrite
				if mode == "kb-without-write" {
					in.KBAccess = KBAccessRead
				}
			}
			message := BuildStepReflectionTurn(in)
			if mode == "none" || mode == "kb-without-write" {
				if message != "" {
					t.Fatal("unrequested or ungranted reflection was started")
				}
				return
			}
			if len(message) > 1500 || strings.Contains(message, "old_run_counts") || strings.Contains(message, guide.Content) {
				t.Fatalf("request repeated task/evidence/procedures: %d bytes", len(message))
			}
			if strings.Contains(message, in.LearningsTargetPath) != in.writesLearnings() || strings.Contains(message, in.KBNotesTargetPath) != in.writesKB() || !strings.Contains(message, "nothing new") {
				t.Fatal("reflection did not preserve actual contribution gates or no-edit outcome")
			}
			t.Logf("REFLECTION_REVIEW mode=%s bytes=%d\n%s", mode, len(message), message)
		})
	}
}

// KB-only reflection previously widened the global learnings directory too.
// Check both the agent and the real per-session shell guard, then restoration.
type observingReflectionAgent struct {
	*fakeDirectLearningAgent
	observe func()
}

func (a *observingReflectionAgent) GetBaseAgent() *agents.BaseAgent {
	a.observe()
	return nil // Stop at the model boundary, after the real reflection grant was applied.
}

func TestKnowledgeReflectionDoesNotGrantLearnings(t *testing.T) {
	testKnowledgeReflectionWriteScopes(t, false)
}
func TestRecoveredKnowledgeReflectionDoesNotGrantLearnings(t *testing.T) {
	testKnowledgeReflectionWriteScopes(t, true)
}

func testKnowledgeReflectionWriteScopes(t *testing.T, recovered bool) {
	c := newAgentClosingTestOrchestrator(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
	}))
	defer server.Close()
	c.WorkspaceClient = workspace.NewClient(server.URL)
	const session = "kb-only-reflection"
	defer common.ClearSessionShellConfig(session)
	root := c.GetWorkspacePath()
	stepPath, learningPath, notesPath := root+"/runs/iteration-0/default/execution/research", root+"/learnings/_global", root+"/knowledgebase/notes"
	common.SetSessionFolderGuard(session, []string{stepPath}, []string{stepPath})
	cfg := &agents.OrchestratorAgentConfig{MCPSessionID: session, FolderGuardReadPaths: []string{stepPath}, FolderGuardWritePaths: []string{stepPath}}
	observed := false
	agent := &observingReflectionAgent{fakeDirectLearningAgent: &fakeDirectLearningAgent{cfg: cfg}}
	agent.observe = func() {
		if !testContainsString(cfg.FolderGuardWritePaths, notesPath) {
			return
		}
		observed = true
		for _, writes := range [][]string{cfg.FolderGuardWritePaths, common.GetSessionShellConfig(session).WritePaths} {
			if !testContainsString(writes, notesPath) || testContainsString(writes, learningPath) {
				t.Errorf("wrong reflection write grant: %v", writes)
			}
		}
	}
	stepCfg := &AgentConfigs{LearningsAccess: LearningsAccessNone, KnowledgebaseAccess: KBAccessReadWrite, KnowledgebaseContribution: "Record verified context."}
	step := &AgentPlanStep{Type: StepTypeAgent, CommonStepFields: CommonStepFields{ID: "research", Description: "Produce verified research."}, AgentConfigs: stepCfg}
	if recovered {
		state := &WorkflowContinuationState{StepID: "research", StepPath: "research"}
		runtime := &workflowContinuationStepRuntime{Step: step, StepPath: "research", TurnCount: 1}
		run := c.prepareRecoveredReflection(state, runtime, nil)
		if run == nil {
			t.Fatal("recovery skipped a knowledgebase-only contribution")
		}
		_, _ = run(t.Context(), agent)
	} else {
		c.runStepReflectionTurn(t.Context(), step, 0, "research", "research", "research", stepCfg, agent, nil, false, 1, "")
	}
	if !observed {
		t.Fatal("real reflection path never applied knowledgebase grant")
	}
	for _, writes := range [][]string{cfg.FolderGuardWritePaths, common.GetSessionShellConfig(session).WritePaths} {
		if len(writes) != 1 || writes[0] != stepPath {
			t.Fatalf("reflection grants were not restored: %v", writes)
		}
	}
}

func TestLoadWorkflowDBTableNamesExcludesPlatformTables(t *testing.T) {
	// The list exists to show a step where its own observations belong. Naming
	// harness bookkeeping would invite a step to write into Pulse's records.
	for _, name := range []string{"pulse_finding_details", "pulse_review_log", "run_concerns", "report_human_inputs", "eval_results"} {
		if !isPlatformOwnedTable(name) {
			t.Errorf("%q should be filtered out of the step-facing table list", name)
		}
	}
	for _, name := range []string{"replies_posted", "latency_baselines", "cost_daily_metrics", "job_candidates"} {
		if isPlatformOwnedTable(name) {
			t.Errorf("%q is workflow-owned and must stay in the list", name)
		}
	}
}
