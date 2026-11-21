package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PLAT-746-886: no channel/file changes happen until every dependency has an
// explicit valid choice and the confirmation belongs to the current preview.
func TestAgentRemovalRequiresCurrentPreviewAndEveryChoice(t *testing.T) {
	for _, tc := range []struct {
		name, confirmation string
		choices            map[string]string
		allowed            bool
	}{
		{"no confirmation", "", map[string]string{"schedule": "crew:chat:other"}, false},
		{"stale confirmation", "old", map[string]string{"schedule": "crew:chat:other"}, false},
		{"missing choice", "current", nil, false},
		{"foreign destination", "current", map[string]string{"schedule": "another:chat:private"}, false},
		{"remove", "current", map[string]string{"schedule": "remove"}, true},
		{"reassign", "current", map[string]string{"schedule": "crew:chat:other"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := false
			plan := crewAgentRemovalPlan{preview: crewAgentRemovalPreview{Confirmation: "current", Dependencies: []crewAgentRemovalDependency{{ID: "schedule", Name: "Daily check", CanReassign: true}}, Destinations: []crewAgentRemovalDestination{{Key: "crew:chat:other"}}}, applyChannels: []func(context.Context, map[string]string) error{func(context.Context, map[string]string) error { changed = true; return nil }}}
			err := plan.apply(context.Background(), tc.confirmation, tc.choices, "crew")
			if (err == nil) != tc.allowed || changed != tc.allowed {
				t.Fatalf("allowed=%v changed=%v err=%v", tc.allowed, changed, err)
			}
		})
	}
}

// PLAT-746-886: exercise the HTTP preview/confirm endpoint with real workspace
// storage. Preview leaves the agent and schedule untouched; confirm preserves
// unrelated agents and reassigns only the selected linked work.
func TestAgentRemovalHTTPPreviewsThenReassignsAndDeletes(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	for _, key := range []string{"team:chat:remove", "team:chat:keep"} {
		binding, _, err := resolveConversationBindingForUser(ctx, "owner", profile, key)
		if err != nil {
			t.Fatal(err)
		}
		record, createErr := store.resolveOrCreate(ctx, "owner", profile, binding, "")
		if createErr != nil {
			t.Fatal(createErr)
		}
		if key == "team:chat:remove" {
			if err = store.writeMainChatPointer(ctx, externalCommandCrewSettings+"/product.json", key, record.SessionID); err != nil {
				t.Fatal(err)
			}
		}
	}
	const schedule = `{"schedules":[{"id":"daily","name":"Daily check","project":{"kind":"crew","id":"team"},"action":{"type":"message","tab":"team:chat:remove"},"input":{"messages":["Check status"]},"enabled":false}]}`
	f.write(t, externalCommandCrewSettings+"/schedules.json", schedule)
	var workflow map[string]any
	if err = json.Unmarshal([]byte(f.read(t, externalCommandCrewSettings+"/workflow.json")), &workflow); err != nil {
		t.Fatal(err)
	}
	workflow["schedules"] = []any{map[string]any{"id": "script-notify", "name": "Script notification", "chat_id": "team:chat:remove", "enabled": false}}
	rawWorkflow, _ := json.Marshal(workflow)
	f.write(t, externalCommandCrewSettings+"/workflow.json", string(rawWorkflow))
	call := func(input AgentProfileConversationRequest) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(input)
		req := httptest.NewRequest(http.MethodPost, "/close", bytes.NewReader(body)).WithContext(ctx)
		req = mux.SetURLVars(req, map[string]string{"id": "work"})
		response := httptest.NewRecorder()
		f.api.handleCloseAgentProfileSideChat(response, req)
		return response
	}
	response := call(AgentProfileConversationRequest{ConversationKey: "team:chat:remove", PreviewRemoval: true})
	if response.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", response.Code, response.Body.String())
	}
	var preview crewAgentRemovalPreview
	if err = json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Dependencies) != 2 || preview.Dependencies[0].Name != "Daily check" {
		t.Fatalf("missing named dependency: %+v", preview.Dependencies)
	}
	if f.read(t, externalCommandCrewSettings+"/schedules.json") != schedule || !projectChatExists(ctx, "owner", "work", "team", "team:chat:remove") {
		t.Fatal("preview changed work")
	}
	response = call(AgentProfileConversationRequest{ConversationKey: "team:chat:remove", RemovalConfirmation: preview.Confirmation, RemovalChoices: map[string]string{preview.Dependencies[0].ID: "team:chat:keep", preview.Dependencies[1].ID: "team:chat:keep"}})
	if response.Code != http.StatusNoContent {
		t.Fatalf("confirm: %d %s", response.Code, response.Body.String())
	}
	if projectChatExists(ctx, "owner", "work", "team", "team:chat:remove") || !projectChatExists(ctx, "owner", "work", "team", "team:chat:keep") {
		t.Fatal("removed wrong agent")
	}
	var manifest productProjectManifest
	if err = json.Unmarshal([]byte(f.read(t, externalCommandCrewSettings+"/product.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.MainChatKey != "" || manifest.SessionID != "" {
		t.Fatal("deleted Main was retained or replaced")
	}
	var saved struct {
		Schedules []struct {
			Enabled bool
			Action  struct{ Tab string }
		}
	}
	if err = json.Unmarshal([]byte(f.read(t, externalCommandCrewSettings+"/schedules.json")), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 1 || saved.Schedules[0].Action.Tab != "team:chat:keep" || saved.Schedules[0].Enabled {
		t.Fatal("linked work was lost, not reassigned, or enabled")
	}
	if err = json.Unmarshal([]byte(f.read(t, externalCommandCrewSettings+"/workflow.json")), &workflow); err != nil {
		t.Fatal(err)
	}
	notify := workflow["schedules"].([]any)[0].(map[string]any)
	if notify["chat_id"] != "team:chat:keep" || notify["enabled"] != false {
		t.Fatal("script destination not reassigned with its full physical key")
	}
}

// Removing a prerequisite exposes the dependent schedule as an explicit choice;
// reassigning the prerequisite must not remove the dependent schedule.
func TestAgentRemovalRequiresDependentScheduleChoiceOnlyWhenNeeded(t *testing.T) {
	plan := crewAgentRemovalPlan{preview: crewAgentRemovalPreview{Confirmation: "current", Dependencies: []crewAgentRemovalDependency{{ID: "parent", Name: "First", CanReassign: true}}, Destinations: []crewAgentRemovalDestination{{Key: "crew:chat:other"}}}, files: []*crewAgentRemovalFile{{path: "schedules.json", collection: "schedules", choices: map[int]string{0: "parent"}, document: map[string]any{"schedules": []any{
		map[string]any{"id": "first", "name": "First"},
		map[string]any{"id": "second", "name": "Second", "when": map[string]any{"after": map[string]any{"schedules": []any{"first"}}}},
	}}}}}
	plan.addDependentSchedules()
	if len(plan.preview.Dependencies) != 2 || len(plan.preview.Dependencies[1].WhenRemoved) != 1 {
		t.Fatal("dependent schedule was omitted")
	}
	child := plan.preview.Dependencies[1].ID
	// Test policy without mutating fixture files.
	plan.files = nil
	if err := plan.apply(context.Background(), "current", map[string]string{"parent": "crew:chat:other"}, "crew"); err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(context.Background(), "current", map[string]string{"parent": "remove"}, "crew"); err == nil {
		t.Fatal("dependent schedule deleted without a choice")
	}
	if err := plan.apply(context.Background(), "current", map[string]string{"parent": "remove", child: "remove"}, "crew"); err != nil {
		t.Fatal(err)
	}
}

// The input file can list a child before its second parent. Removing only that
// later parent must still require the child's explicit removal choice.
func TestAgentRemovalDependentGraphDoesNotDependOnRecordOrder(t *testing.T) {
	plan := crewAgentRemovalPlan{preview: crewAgentRemovalPreview{Confirmation: "current", Dependencies: []crewAgentRemovalDependency{{ID: "root", Name: "Root", CanReassign: true}}, Destinations: []crewAgentRemovalDestination{{Key: "crew:chat:other"}}}, files: []*crewAgentRemovalFile{{path: "schedules.json", collection: "schedules", choices: map[int]string{0: "root"}, document: map[string]any{"schedules": []any{
		map[string]any{"id": "root"},
		map[string]any{"id": "child", "when": map[string]any{"after": map[string]any{"schedules": []any{"root", "later"}}}},
		map[string]any{"id": "later", "when": map[string]any{"after": map[string]any{"schedules": []any{"root"}}}},
	}}}}}
	plan.addDependentSchedules()
	if len(plan.preview.Dependencies) != 3 || len(plan.preview.Dependencies[1].WhenRemoved) != 2 {
		t.Fatal("later parent omitted")
	}
	child, later := plan.preview.Dependencies[1].ID, plan.preview.Dependencies[2].ID
	// Reverse preview order to exercise the public confirmation policy as well.
	plan.preview.Dependencies[0], plan.preview.Dependencies[2] = plan.preview.Dependencies[2], plan.preview.Dependencies[0]
	plan.files = nil
	if err := plan.apply(context.Background(), "current", map[string]string{"root": "remove", later: "remove"}, "crew"); err == nil {
		t.Fatal("child removed without choice")
	}
	if err := plan.apply(context.Background(), "current", map[string]string{"root": "remove", later: "remove", child: "remove"}, "crew"); err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(context.Background(), "current", map[string]string{"root": "crew:chat:other", later: "remove"}, "crew"); err != nil {
		t.Fatal("inactive stale choice activated a child", err)
	}
}

func TestAgentRemovalFunctionCallersRequireTheirOwnChoice(t *testing.T) {
	plan := crewAgentRemovalPlan{preview: crewAgentRemovalPreview{Confirmation: "current", Dependencies: []crewAgentRemovalDependency{{ID: "fn", Name: "Function", CanReassign: true}}, Destinations: []crewAgentRemovalDestination{{Key: "crew:chat:other"}}}, files: []*crewAgentRemovalFile{
		{path: "crew/functions.json", collection: "functions", choices: map[int]string{0: "fn"}, document: map[string]any{"functions": []any{map[string]any{"id": "fn", "name": "report"}}}},
		{path: "crew/webhooks.json", collection: "webhooks", choices: map[int]string{}, document: map[string]any{"webhooks": []any{map[string]any{"id": "hook", "name": "Incoming report", "action": map[string]any{"function": "report"}}}}},
	}}
	plan.addDependentFunctionCalls()
	if len(plan.preview.Dependencies) != 2 {
		t.Fatal("stored caller omitted")
	}
	caller := plan.preview.Dependencies[1].ID
	plan.files = nil
	if err := plan.apply(context.Background(), "current", map[string]string{"fn": "remove"}, "crew"); err == nil {
		t.Fatal("function deleted with a dangling unconfirmed caller")
	}
	if err := plan.apply(context.Background(), "current", map[string]string{"fn": "crew:chat:other"}, "crew"); err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(context.Background(), "current", map[string]string{"fn": "remove", caller: "remove"}, "crew"); err != nil {
		t.Fatal(err)
	}
}
