package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Exercise the registered Builder tool against the real workspace HTTP reader.
// Any mutation/model/tool call is a failure; the explicit platform selection
// must still yield exactly one capability-filtered reference identity.
func TestStepRuntimePreviewReadsCurrentConfigWithoutExecuting(t *testing.T) {
	controller := newAgentClosingTestOrchestrator(t)
	controller.selectedRunFolder = "iteration-0/default"
	controller.variableValues = map[string]string{"TOPIC": "private runtime topic"}
	controller.SetSecrets([]orchestrator.SecretEntry{{Name: "EXAMPLE_TOKEN", Value: "synthetic-secret-for-preview"}})
	files := map[string]string{
		"workflow.json":             `{"execution_defaults":{"disable_parallel_tool_execution":true}}`,
		"planning/plan.json":        `{"steps":[{"id":"research","title":"Research","type":"agent","description":"Research {{TOPIC}}. synthetic-secret-for-preview","context_output":"research.json","items":[{"id":"work","type":"user_message","message":"Research and verify {{TOPIC}}."}]}]}`,
		"planning/step_config.json": `{"steps":[{"id":"research","agent_configs":{"enabled_skills":["builder-reference","agent-browser"],"enabled_custom_tools":["workspace_browser:agent_browser"],"knowledgebase_access":"none","learnings_access":"none"}}]}`,
	}
	before := map[string]string{}
	for key, value := range files {
		before[key] = value
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("preview mutated workspace: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/Workflow/test-flow/")
		body, exists := files[path]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"content": body}})
	}))
	defer server.Close()
	controller.WorkspaceClient = workspace.NewClient(server.URL)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	bindAgentFunctionTools(controller, func(_ context.Context, scope AgentFunctionScope) ([]llmtypes.Tool, map[string]interface{}, error) {
		if scope.WorkspacePath != controller.GetWorkspacePath() || scope.StepID != "research" {
			t.Fatal("preview lost function source identity")
		}
		return []llmtypes.Tool{{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "call_function"}}}, map[string]interface{}{
			"call_function": func(context.Context, map[string]interface{}) (string, error) {
				t.Error("preview executed a function")
				return "", nil
			},
		}, nil
	})
	for _, name := range []string{"agent_browser", "query_workflow_db", "mutate_workflow_db", "create_human_input_request"} {
		controller.WorkspaceTools = append(controller.WorkspaceTools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}})
		if controller.WorkspaceToolExecutors == nil {
			controller.WorkspaceToolExecutors = map[string]interface{}{}
		}
		controller.WorkspaceToolExecutors[name] = func(context.Context, map[string]interface{}) (string, error) {
			t.Error("preview executed a workspace tool")
			return "", nil
		}
	}
	draft := newWorkshopDefinitionDraft()
	RegisterWorkshopChatTools(draft, &WorkshopChatSession{controller: controller, StepRegistry: NewWorkshopStepRegistry(), config: &WorkshopConfig{WorkspacePath: controller.GetWorkspacePath()}}, workshopToolTestLogger{})
	tool := draft.tools["preview_step_runtime"]
	if tool.Execute == nil {
		t.Fatal("Builder preview tool missing")
	}
	stale := &PlanningResponse{Steps: []PlanStepInterface{&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "old"}}}}
	ctx := withExecutionPlan(t.Context(), stale)
	result, err := tool.Execute(ctx, map[string]interface{}{"step_id": "research"})
	if err != nil {
		t.Fatal(err)
	}
	var preview stepRuntimePreview
	if err := json.Unmarshal([]byte(result), &preview); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "synthetic-secret-for-preview") || strings.Contains(result, "private runtime topic") {
		t.Fatal("preview exposed a secret or resolved variable value")
	}
	if preview.ParallelToolCalls || preview.ItemID != "work" || !strings.Contains(preview.UserMessage, "<VARIABLE:TOPIC>") || !strings.Contains(preview.UserMessage, "Research and verify") || len(preview.Unresolved) == 0 {
		t.Fatal("preview omitted current config, selected item, instructions or unresolved context")
	}
	if strings.Contains(strings.Join(preview.Tools, ","), "call_function") {
		t.Fatal("ordinary step preview exposed Crew function tools")
	}
	counts := map[string]int{}
	for _, skill := range preview.Skills {
		counts[skill.Name]++
		if skill.Name == "builder-reference" && !strings.Contains(strings.Join(skill.References, "\n"), "browser-usage.md") {
			t.Fatal("runtime reference lost browser instructions")
		}
	}
	if counts["builder-reference"] != 1 || counts["agent-browser"] != 1 {
		t.Fatalf("wrong assembled identities: %v", counts)
	}
	identity, _ := controller.assembleStepRuntimeIdentity(&agents.OrchestratorAgentConfig{}, []string{"builder-reference", "agent-browser"}, false, preview.Tools, false, false)
	if len(identity) != len(preview.Skills) {
		t.Fatal("preview and execution identity assembly diverged")
	}
	for i, skill := range identity {
		if skill.Name != preview.Skills[i].Name {
			t.Fatal("preview did not use execution skill assembly")
		}
	}
	if !reflect.DeepEqual(files, before) || executionPlanFromContext(ctx) != stale {
		t.Fatal("preview changed saved state or an active execution snapshot")
	}
	// The same registered tool renders scripted helper contracts and specialist
	// route instructions without invoking either scripts or children.
	files["planning/plan.json"] = `{"steps":[{"id":"research","title":"Research","type":"scripted","description":"Measure {{TOPIC}}."}]}`
	result, err = tool.Execute(ctx, map[string]interface{}{"step_id": "research"})
	if err != nil || !strings.Contains(result, "agentworks_db") {
		t.Fatalf("scripted runtime preview failed: %v", err)
	}
	if err := json.Unmarshal([]byte(result), &preview); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(preview.Tools, ","), "call_function") {
		t.Fatal("scripted preview exposed agent function tools")
	}
	files["planning/plan.json"] = `{"steps":[{"id":"research","title":"Research","type":"agent","description":"Coordinate {{TOPIC}}.","items":[{"id":"work","type":"user_message","message":"Verify the work."}],"predefined_routes":[{"route_id":"worker","route_name":"Worker","sub_agent_step":{"id":"worker","title":"Work","type":"scripted","description":"Produce verified data."}}]}]}`
	result, err = tool.Execute(ctx, map[string]interface{}{"step_id": "research"})
	if err != nil || !strings.Contains(result, "Scripted helpers") || !strings.Contains(result, "conditional_invocation_tools") {
		t.Fatalf("delegating runtime preview failed: %v", err)
	}
	files["workflow.json"] = `{"code_layout_version":1}`
	files["planning/plan.json"] = `{"steps":[{"id":"research","title":"Research","type":"scripted","description":"Measure."}]}`
	result, err = tool.Execute(ctx, map[string]interface{}{"step_id": "research"})
	if err != nil || !strings.Contains(result, "/Workflow/test-flow/code/") {
		t.Fatalf("canonical scripted source preview failed: %v", err)
	}
	files["planning/plan.json"] = before["planning/plan.json"]
	files["planning/step_config.json"] = strings.Replace(before["planning/step_config.json"], `"learnings_access":"none"`, `"learnings_access":"read-write"`, 1)
	files["learnings/_global/SKILL.md"] = "# Workflow learnings\nUse verified sources."
	result, err = tool.Execute(ctx, map[string]interface{}{"step_id": "research"})
	if err != nil || !strings.Contains(result, "workflow-learnings") {
		t.Fatalf("learnings-writer preview failed: %v", err)
	}
	// Migration notes must survive saved plans without becoming task instructions.
	for _, typ := range []string{"agent", "scripted"} {
		notePlan := `{"steps":[{"id":"research","title":"Research","type":"` + typ + `","prompt_contract":"description-first-v1","description":"Measure the current source."`
		if typ == "agent" {
			notePlan += `,"predefined_routes":[{"route_id":"retired","route_name":"Retired","sub_agent_step":{"id":"retired","title":"Retired","type":"agent","description":"ARCHIVE_ONLY_ROUTE_INSTRUCTION"}}]`
		}
		notePlan += `}]}`
		noteConfig := `{"steps":[{"id":"research","agent_configs":{"learning_objective":"ARCHIVE_ONLY_OBJECTIVE_INSTRUCTION","learnings_access":"read-write"}}]}`
		migrated, config, err := migrateGoalAgentContent(notePlan, noteConfig)
		if err != nil {
			t.Fatal(err)
		}
		files["planning/plan.json"], files["planning/step_config.json"] = migrated, config
		result, err = tool.Execute(ctx, map[string]interface{}{"step_id": "research"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, "ARCHIVE_ONLY_") {
			t.Fatalf("%s runtime injected retired audit instructions: %s", typ, result)
		}
	}
	// Migrated zero-item agents preview their task, rather than requiring item zero.
	files["planning/plan.json"] = `{"steps":[{"id":"research","title":"Research","type":"agent","prompt_contract":"description-first-v1","system_rules":"No paid calls.","description":"Research {{TOPIC}}."}]}`
	result, err = tool.Execute(ctx, map[string]interface{}{"step_id": "research"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(result), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ItemID != AgentDescriptionTurnID || preview.UserMessage != "Research <VARIABLE:TOPIC>.\n\n## Rules\nNo paid calls." || strings.Contains(preview.SystemPrompt, "Research <VARIABLE:TOPIC>.") || strings.Contains(preview.SystemPrompt, "No paid calls.") {
		t.Fatal("description-only preview has wrong task/rules")
	}
	// Invalid persisted config is reported rather than silently repaired.
	files["planning/step_config.json"] = `{"steps":[{"id":"research"},{"id":"research"}]}`
	if _, err := tool.Execute(ctx, map[string]interface{}{"step_id": "research"}); err == nil {
		t.Fatal("preview accepted duplicate config IDs")
	}
	if files["planning/step_config.json"] != `{"steps":[{"id":"research"},{"id":"research"}]}` {
		t.Fatal("preview repaired config")
	}
}

func TestStepPromptsRespectExplicitPermissionsAndScriptedDBHelpers(t *testing.T) {
	renderer := &WorkflowExecutionOnlyAgent{runtimeSupplements: []string{"Standing runtime scope for this step."}}
	for _, access := range []string{DBAccessNone, DBAccessRead, DBAccessReadWrite} {
		vars := map[string]string{"DBAccess": access, "KbAccess": KBAccessNone, "IsScriptedMode": "true", "DBDirectAccess": "true"}
		system := renderer.executionOnlySystemPromptProcessor(vars)
		// An ordinary step is not a decision handler: the saved-decision lifecycle and Builder/optimizer
		// process text belong to the agents that hold those tools, not to every step's system prompt.
		for _, leak := range []string{"mark_human_input_consumed", "outcome_evidence", "apply_workflow_db_migration", "optimizer", "Unmigrated", "Pulse"} {
			if strings.Contains(system, leak) {
				t.Fatalf("ordinary step prompt carries platform process text %q", leak)
			}
		}
		if strings.Count(system, "Standing runtime scope for this step.") != 1 || strings.Count(renderer.executionOnlySystemPromptProcessor(vars), "Standing runtime scope for this step.") != 1 {
			t.Fatal("runtime supplements disappeared or grew across prompt renders")
		}
		user := renderer.executionOnlyUserMessageProcessor(vars)
		if !strings.Contains(system, "never try a forbidden action to test it") || strings.Contains(system, `Never conclude "read-only" or "not allowed" from the instructions`) {
			t.Fatalf("access %s conflicts with explicit restrictions", access)
		}
		if strings.Contains(system+user, "persist through the absolute `$DB_PATH`") || strings.Contains(user, "persist scripted results through `$DB_PATH`") || strings.Contains(user, "6. Create the output file.") {
			t.Fatalf("access %s retained conflicting persistence/output instructions", access)
		}
		if access == DBAccessReadWrite && !strings.Contains(system, "Use `agentworks_db` in saved scripts") {
			t.Fatal("scripted persistence must consistently use the helper")
		}
		if access == DBAccessNone && strings.Contains(user, "persist results with `mutate_workflow_db`") {
			t.Fatal("no-DB step told to mutate database")
		}
	}
}
