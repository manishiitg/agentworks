package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-265: selecting an agent reuses configuration only, and an ID from
// another Crew must fail even when the same person owns both Crews.
func TestCrewExecutableAgentConfigurationCannotCrossCrewOrImportChatHistory(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	svc, files, ctx := newCrewCreationTestEnvWithOptions(t, []agentprofiles.ProviderOption{{ID: "codex", Label: "Example coding agent", Provider: "codex-cli", ModelID: "example-model", Models: []string{"example-model"}, ReasoningEfforts: []string{"medium"}}})
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"stranger","username":"stranger","can_create":true}]}`)
	profile, err := svc.registry.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	create := func(name string) (CreatedCrew, ProductConversationRecord) {
		t.Helper()
		crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: name, Purpose: "Write clear reports", Instructions: "Cite the sources", Servers: []string{"github"}, IdempotencyKey: name})
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
	for _, root := range []string{crew.WorkspacePath, foreignCrew.WorkspacePath, filepath.Join(crewroot.PrivateDirectory, crew.CrewID), filepath.Join(crewroot.PrivateDirectory, foreignCrew.CrewID)} {
		if err := os.MkdirAll(filepath.Join(docs, root), 0700); err != nil {
			t.Fatal(err)
		}
	}
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
	// Keep an old plan readable while its editors are retired (PLAT-746-471).
	if err := writeFileToWorkspace(ctx, child.Root+"/planning/plan.json", `{"steps":[],"orphan_steps":[{"type":"agent","id":"report","title":"Report","description":"Write a report","prompt_contract":"description-first-v1"},{"type":"scripted","id":"untouched","title":"Untouched","description":"Keep this step"}]}`); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	plan, err := readPlanFromWorkspace(ctx, child.Root)
	if err != nil || len(plan.OrphanSteps) != 2 {
		t.Fatal("specialist plan unavailable", err)
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
	var authoring struct {
		Executables []struct {
			Environment map[string]string `json:"code_environment"`
		} `json:"executables"`
	}
	if err := json.Unmarshal([]byte(listed), &authoring); err != nil || len(authoring.Executables) != 1 {
		t.Fatalf("authoring paths unavailable: %s %v", listed, err)
	}
	for key, want := range stepworkflow.CodeRuntimeEnvironment(child.Root) {
		if got := authoring.Executables[0].Environment[key]; got == "" || got != want {
			t.Fatalf("chat installs dependencies somewhere other than the runner: %s=%q want %q", key, got, want)
		}
	}
	// PLAT-746 (owner, 8 Nov): an older plan workflow keeps running but is no longer
	// edited, from code files to steps and inputs; changing it means converting it.
	for _, call := range []map[string]interface{}{
		{"action": "write", "workspace_path": child.Root, "file": "code/report/main.py", "content": "print('report')"},
		{"action": "write", "workspace_path": child.Root, "file": "planning/plan.json", "content": "{}"},
		{"action": "write", "workspace_path": child.Root, "file": "variables/variables.json", "content": "{}"},
		{"action": "edit", "workspace_path": child.Root, "operation": "update_step", "arguments": map[string]interface{}{"step_id": "report", "changes": map[string]interface{}{"title": "Updated report"}}},
		{"action": "edit", "workspace_path": child.Root, "operation": "update_variable", "arguments": map[string]interface{}{"action": "add", "name": "count", "type": "integer", "value": "1"}},
	} {
		if out, err := edit(ctx, call); err == nil || !strings.Contains(err.Error(), "can no longer be edited") {
			t.Fatalf("old plan workflow was edited: %v %s %v", call, out, err)
		}
	}
	if after, err := readPlanFromWorkspace(ctx, child.Root); err != nil || len(after.OrphanSteps) != len(plan.OrphanSteps) || after.OrphanSteps[0].GetTitle() == "Updated report" {
		t.Fatalf("refused edits changed the plan: %+v %v", after, err)
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
	runner := newCrewWorkflowResources(svc, "owner")
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
	if _, err := newCrewWorkflowResources(svc, "stranger").ResolveCrewAgentConfiguration(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "stranger", Username: "stranger"}), child.Root, chat.ConversationKey); err == nil || err.Error() != "this Crew is unavailable" {
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
