package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-265: selecting an agent reuses configuration only, and an ID from
// another Crew must fail even when the same person owns both Crews.
func TestCrewExecutableAgentConfigurationCannotCrossCrewOrImportChatHistory(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnvWithOptions(t, []agentprofiles.ProviderOption{{ID: "codex", Label: "Example coding agent", Provider: "codex-cli", ModelID: "example-model", Models: []string{"example-model"}, ReasoningEfforts: []string{"medium"}}})
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"stranger","username":"stranger","can_create":true}]}`)
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
	if err := writeFileToWorkspace(ctx, userProductAccessFilePath(), `{"owner":{"products":["work"]},"stranger":{"products":["work"]}}`); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		user   string
		status int
	}{{"owner", http.StatusOK}, {"stranger", http.StatusNotFound}} {
		request := httptest.NewRequest(http.MethodGet, "/api/workflow/step-inspections?workspace_path=Workflow%2Fchild", nil)
		request = request.WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: check.user, Username: check.user}))
		reply := httptest.NewRecorder()
		(&StreamingAPI{projectActions: svc}).handleWorkspaceStepInspections(reply, request)
		if reply.Code != check.status {
			t.Fatalf("Work-only inspection user=%s: %d %s", check.user, reply.Code, reply.Body)
		}
	}
	files.files["chat_history/"+chat.SessionID+".json"] = `{"messages":[{"text":"PERSONAL_HISTORY_MUST_NOT_BE_IMPORTED"}]}`
	// Exercise the editor's actual write API for a reusable specialist as well
	// as the runtime resolver. Neither path may accept another Crew as authority.
	if err := writeFileToWorkspace(ctx, child.Root+"/planning/plan.json", `{"steps":[],"orphan_steps":[{"type":"agent","id":"report","title":"Report","description":"Write a report","prompt_contract":"description-first-v1"},{"type":"scripted","id":"untouched","title":"Untouched","description":"Keep this step"}]}`); err != nil {
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
	if err != nil || len(plan.OrphanSteps) != 2 {
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
	// PLAT-746-345/346: whole-file replacement is refused; native edits keep
	// unrelated plan state and still work for an authorized owner.
	for _, file := range []string{"planning/plan.json", "planning/step_config.json", "variables/variables.json"} {
		if _, err := edit(ctx, map[string]interface{}{"action": "write", "workspace_path": child.Root, "file": file, "content": "{}"}); err == nil || !strings.Contains(err.Error(), "whole plan") {
			t.Fatalf("whole replacement admitted: %s %v", file, err)
		}
	}
	if _, err := edit(ctx, map[string]interface{}{"action": "edit", "workspace_path": child.Root, "operation": "update_step", "arguments": map[string]interface{}{"step_id": "report", "changes": map[string]interface{}{"title": "Updated report", "reason": "Clarify report label"}}}); err != nil {
		t.Fatal(err)
	}
	plan, err = readPlanFromWorkspace(ctx, child.Root)
	if err != nil || len(plan.OrphanSteps) != 2 || plan.OrphanSteps[0].GetTitle() != "Updated report" {
		t.Fatalf("native edit lost plan: %+v %v", plan, err)
	}
	if plan.OrphanSteps[1].GetID() != "untouched" {
		t.Fatal("unrelated step was lost")
	}
	rawPlan, _ = json.Marshal(plan.OrphanSteps[0])
	if !strings.Contains(string(rawPlan), chat.ConversationKey) {
		t.Fatal("editing title lost saved agent")
	}
	for _, name := range []string{"count", "untouched"} {
		out, err := edit(ctx, map[string]interface{}{"action": "edit", "workspace_path": child.Root, "operation": "update_variable", "arguments": map[string]interface{}{"action": "add", "name": name, "type": "integer", "value": "2"}})
		if err != nil {
			t.Fatalf("input add: %s %v", out, err)
		}
	}
	if _, err := edit(ctx, map[string]interface{}{"action": "edit", "workspace_path": child.Root, "operation": "update_variable", "arguments": map[string]interface{}{"action": "update", "existing_variable_name": "count", "value": "3"}}); err != nil {
		t.Fatal(err)
	}
	inputs, err := readWorkflowInputDefinitions(ctx, child.Root)
	if err != nil || len(inputs.Variables) != 2 || inputs.Variables[1].Value != "2" {
		t.Fatalf("input edit lost other inputs: %+v %v", inputs, err)
	}
	// PLAT-746-355: removing duplicate settings must leave the same child timers
	// and webhooks editable from Crew chat, using the real save handlers.
	api.scheduler = NewSchedulerService(api)
	configure := func(operation string, arguments map[string]interface{}) string {
		t.Helper()
		out, err := edit(ctx, map[string]interface{}{"action": "edit", "workspace_path": child.Root, "operation": operation, "arguments": arguments})
		if err != nil {
			t.Fatalf("%s: %s %v", operation, out, err)
		}
		return out
	}
	out := configure("manage_schedules", map[string]interface{}{"action": "create", "schedule": map[string]interface{}{"name": "Daily report", "schedule_type": "cron", "cron_expression": "0 9 * * *", "timezone": "UTC", "enabled": false, "messages": []string{"Prepare report"}}})
	var timer struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &timer); err != nil || timer.ID == "" {
		t.Fatalf("schedule result: %s %v", out, err)
	}
	configure("manage_schedules", map[string]interface{}{"action": "update", "schedule_id": timer.ID, "schedule": map[string]interface{}{"name": "Updated daily report"}})
	if got := configure("manage_schedules", map[string]interface{}{"action": "list"}); !strings.Contains(got, "Updated daily report") {
		t.Fatal("chat edit was not saved", got)
	}
	configure("manage_schedules", map[string]interface{}{"action": "delete", "schedule_id": timer.ID})
	out = configure("manage_webhooks", map[string]interface{}{"action": "create", "trigger": map[string]interface{}{"name": "Inbound report", "enabled": false, "chat_id": chat.ConversationKey, "auth_mode": "bearer", "route_selections": map[string]interface{}{}}})
	var hook struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &hook); err != nil || hook.ID == "" {
		t.Fatal("webhook did not return an identity", err)
	}
	savedManifest, found, err := ReadWorkflowManifest(ctx, child.Root)
	if err != nil || !found || len(savedManifest.Schedules) != 1 || savedManifest.Schedules[0].ChatID != chat.ConversationKey {
		t.Fatal("webhook lost its requested chat destination", err)
	}
	configure("manage_webhooks", map[string]interface{}{"action": "delete", "id": hook.ID})
	originalDirectory := *directory
	*directory = `{"users":[{"id":"owner","username":"owner","role":"viewer"},{"id":"stranger","username":"stranger","can_create":true}]}`
	invalidateUserDirectoryCache()
	for _, action := range []string{"create", "write", "edit"} {
		if _, err := edit(ctx, map[string]interface{}{"action": action, "workspace_path": child.Root, "file": "code/report/main.py", "content": "denied"}); err == nil || !strings.Contains(err.Error(), "account cannot edit") {
			t.Fatalf("demoted owner %s: %v", action, err)
		}
	}
	*directory = originalDirectory
	invalidateUserDirectoryCache()

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
	// The Team roster includes other people's agents. Seeing
	// one must not copy its model/account into the main owner's workflow.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: crew.CrewID, OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "stranger"}}}); err != nil {
		t.Fatal(err)
	}
	memberBinding, allowed, err := resolveConversationBindingForUser(strangerCtx, "stranger", profile, crew.CrewID)
	if err != nil || !allowed {
		t.Fatal("member fixture unavailable", allowed, err)
	}
	memberChat, err := store.resolveOrCreate(strangerCtx, "stranger", profile, memberBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	visible, err := crewOwnerChatRecords(ctx, "owner", "work", crew.CrewID, crew.WorkspacePath)
	if err != nil || len(visible) != 2 {
		t.Fatal("expected both owners' visible agents", visible, err)
	}
	choices, err := crewExecutableAgentChoices(ctx, "owner", "owner", crew.CrewID, crew.WorkspacePath)
	if err != nil || len(choices) != 1 || choices[0].ID != chat.ConversationKey {
		t.Fatal("automation offered another person's configuration", choices, err)
	}
	if _, err := runner.ResolveCrewAgentConfiguration(ctx, child.Root, memberChat.ConversationKey); err == nil {
		t.Fatal("automation reused a teammate's private account configuration")
	}
	// Private side agents now use the Team's visibility record. Main remains
	// shared on this upstream revision; keep it alongside the private agent.
	privateKey := crew.CrewID + codeChatSideMarker + "private"
	privateBinding, err := resolveProductConversationBinding(ctx, "owner", profile, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateChat, err := store.resolveOrCreate(ctx, "owner", profile, privateBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: crew.CrewID, OwnerID: "owner", On: true,
		People:       []crewTeamPerson{{UserID: "stranger"}},
		AgentChoices: map[string]map[string]bool{"owner": {privateChat.ConversationKey: false}},
	}); err != nil {
		t.Fatal(err)
	}
	choices, err = crewExecutableAgentChoices(strangerCtx, "stranger", "owner", crew.CrewID, crew.WorkspacePath)
	if err != nil || len(choices) != 1 || choices[0].ID != chat.ConversationKey {
		t.Fatal("member discovered the owner's Only me agent", choices, err)
	}
	if _, err := runner.ResolveCrewAgentConfiguration(ctx, child.Root, privateChat.ConversationKey); err != nil {
		t.Fatal("owner lost their own agent", err)
	}
	// The same configuration operations refuse a server turn for a Mac-owned
	// Crew, but remain usable on the owner's local copy.
	if err := crewroot.WriteSync(crew.CrewID, crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "local-crew"}); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"manage_schedules", "manage_webhooks"} {
		_, err := edit(ctx, map[string]interface{}{"action": "edit", "workspace_path": child.Root, "operation": operation, "arguments": map[string]interface{}{"action": "list"}})
		if err == nil || err.Error() != crewChangeOnMacLine {
			t.Fatalf("%s bypassed Mac configuration rule: %v", operation, err)
		}
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	for _, operation := range []string{"manage_schedules", "manage_webhooks"} {
		configure(operation, map[string]interface{}{"action": "list"})
	}

}
