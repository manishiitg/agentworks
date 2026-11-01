package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// PLAT-746-265: selecting an agent reuses configuration only, and an ID from
// another Crew must fail even when the same person owns both Crews.
func TestCrewExecutableAgentConfigurationCannotCrossCrewOrImportChatHistory(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnvWithOptions(t, []agentprofiles.ProviderOption{{ID: "codex", Label: "Example coding agent", Provider: "codex-cli", ModelID: "example-model", Models: []string{"example-model"}, ReasoningEfforts: []string{"medium"}}})
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"stranger","username":"stranger","can_create":true}]}`)
	profile, err := svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	create := func(name string) (CreatedCrew, ProductConversationRecord) {
		t.Helper()
		crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: name, Purpose: "Write clear reports", Instructions: "Cite the sources", StepInstruction: "Prepare a report", Servers: []string{"github"}, IdempotencyKey: name})
		if err != nil {
			t.Fatal(err)
		}
		binding, err := resolveProductConversationBinding(ctx, "owner", profile, crew.CrewID)
		if err != nil {
			t.Fatal(err)
		}
		record, err := store.resolveOrCreate(ctx, "owner", profile, binding, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.setAgentModel(ctx, "owner", "work", record.ConversationKey, "codex", "example-model", "medium", "global:codex-cli", "owner"); err != nil {
			t.Fatal(err)
		}
		return crew, record
	}
	crew, chat := create("Reports")
	foreignCrew, foreign := create("Other")
	if err := writeFileToWorkspace(ctx, crew.WorkspacePath+"/PROJECT_INSTRUCTIONS.md", "Cite the sources"); err != nil {
		t.Fatal(err)
	}
	child := crewExecutableBinding{ID: "child", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/child", Kind: "workflow"}
	if err := registerCrewExecutable(child); err != nil {
		t.Fatal(err)
	}
	m := NewWorkflowManifest("Report steps")
	m.ID = child.ID
	if err := WriteWorkflowManifest(ctx, child.Root, m); err != nil {
		t.Fatal(err)
	}
	files.files["chat_history/"+chat.SessionID+".json"] = `{"messages":[{"text":"PERSONAL_HISTORY_MUST_NOT_BE_IMPORTED"}]}`
	// Exercise the editor's actual write API for a reusable specialist as well
	// as the runtime resolver. Neither path may accept another Crew as authority.
	if err := writeFileToWorkspace(ctx, child.Root+"/planning/plan.json", `{"steps":[],"orphan_steps":[{"type":"agent","id":"report","title":"Report","description":"Write a report","prompt_contract":"description-first-v1"}]}`); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	request := httptest.NewRequest(http.MethodPost, "/api/workflow/plan/update-step", strings.NewReader(`{"workspace_path":"Workflow/child","step_id":"report","updates":{"crew_agent":"`+chat.ConversationKey+`"}}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	requireWorkflowWriteAccess(api.handleUpdatePlanStep)(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("specialist selection: %d %s", response.Code, response.Body)
	}
	plan, err := readPlanFromWorkspace(ctx, child.Root)
	if err != nil || len(plan.OrphanSteps) != 1 {
		t.Fatal("specialist plan unavailable", err)
	}
	rawPlan, _ := json.Marshal(plan.OrphanSteps[0])
	if !strings.Contains(string(rawPlan), `"crew_agent":"`+chat.ConversationKey+`"`) {
		t.Fatal("saved specialist lost the selected agent")
	}
	// PLAT-746-265: the chat authoring tool uses the same validated workspace
	// writers, scoped to this Crew's children rather than shell folder grants.
	reg := &recordingRegistrar{}
	if err := api.registerCrewExecutableFileTools(reg, "owner", crew.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	edit := reg.tools["crew_workflow_files"].exec
	listed, err := edit(ctx, map[string]interface{}{"action": "list"})
	if err != nil || !strings.Contains(listed, child.Root) || !strings.Contains(listed, chat.ConversationKey) {
		t.Fatalf("own children and agents unavailable: %s %v", listed, err)
	}
	if _, err := edit(ctx, map[string]interface{}{"action": "write", "workspace_path": child.Root, "file": "code/report/main.py", "content": "print('report')"}); err != nil {
		t.Fatal(err)
	}
	read, err := edit(ctx, map[string]interface{}{"action": "read", "workspace_path": child.Root, "file": "code/report/main.py"})
	if err != nil || !strings.Contains(read, "print('report')") {
		t.Fatalf("saved definition unavailable: %s %v", read, err)
	}
	if _, err := edit(ctx, map[string]interface{}{"action": "write", "workspace_path": child.Root, "file": "planning/plan.json", "content": `{"steps":[],"orphan_steps":[{"type":"agent","id":"report","title":"Report","description":"Write a report","prompt_contract":"description-first-v1","crew_agent":"` + chat.ConversationKey + `"}]}`}); err != nil {
		t.Fatal(err)
	}
	foreignChild := crewExecutableBinding{ID: "foreign-child", CrewID: foreignCrew.CrewID, CrewRoot: foreignCrew.WorkspacePath, Root: "Workflow/foreign-child", Kind: "workflow"}
	if err := registerCrewExecutable(foreignChild); err != nil {
		t.Fatal(err)
	}
	for _, denied := range []struct{ root, file, reason string }{
		{foreignChild.Root, "code/report/main.py", "this Crew's own list"},
		{child.Root, "workflow.json", "definition file"},
		{child.Root, "runs/iteration-0/logs/log.txt", "definition file"},
		{child.Root, "code/../../chat_history/private.json", "definition file"},
	} {
		if _, err := edit(ctx, map[string]interface{}{"action": "write", "workspace_path": denied.root, "file": denied.file, "content": "forbidden"}); err == nil || !strings.Contains(err.Error(), denied.reason) {
			t.Fatalf("unexpected refusal for %s/%s: %v", denied.root, denied.file, err)
		}
	}
	strangerCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "stranger", Username: "stranger"})
	if _, err := edit(strangerCtx, map[string]interface{}{"action": "list"}); err == nil || !strings.Contains(err.Error(), "owners") {
		t.Fatalf("foreign principal could edit Crew: %v", err)
	}
	runner := newCrewStepRunner(svc, "owner")
	configuration, err := runner.ResolveCrewAgentConfiguration(ctx, child.Root, chat.ConversationKey)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Model == nil || configuration.Model.Provider != "codex-cli" || configuration.Model.ModelID != "example-model" || configuration.Model.ConnectionID != "global:codex-cli" || !strings.Contains(configuration.Instructions, "Cite the sources") || !strings.Contains(strings.Join(configuration.Servers, ","), "github") {
		t.Fatalf("saved configuration was not reused: %+v", configuration)
	}
	encoded, _ := json.Marshal(configuration)
	if strings.Contains(string(encoded), chat.SessionID) || strings.Contains(string(encoded), "PERSONAL_HISTORY") {
		t.Fatal("configuration imported a private chat")
	}
	if _, err := runner.ResolveCrewAgentConfiguration(ctx, child.Root, foreign.ConversationKey); err == nil || !strings.Contains(err.Error(), "selected Crew agent") {
		t.Fatalf("cross-Crew selection did not fail at the intended rule: %v", err)
	}
	if _, err := newCrewStepRunner(svc, "stranger").ResolveCrewAgentConfiguration(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "stranger", Username: "stranger"}), child.Root, chat.ConversationKey); err == nil || err.Error() != "this Crew is unavailable" {
		t.Fatalf("foreign user did not fail at access: %v", err)
	}
}
