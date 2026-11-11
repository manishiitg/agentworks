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
		if _, err = store.resolveOrCreate(ctx, "owner", profile, binding, ""); err != nil {
			t.Fatal(err)
		}
	}
	const schedule = `{"schedules":[{"id":"daily","name":"Daily check","project":{"kind":"crew","id":"team"},"action":{"type":"message","tab":"team:chat:remove"},"input":{"messages":["Check status"]},"enabled":false}]}`
	f.write(t, externalCommandCrewState+"/schedules.json", schedule)
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
	if len(preview.Dependencies) != 1 || preview.Dependencies[0].Name != "Daily check" {
		t.Fatalf("missing named dependency: %+v", preview.Dependencies)
	}
	if f.read(t, externalCommandCrewState+"/schedules.json") != schedule || !projectChatExists(ctx, "owner", "work", "team", "team:chat:remove") {
		t.Fatal("preview changed work")
	}
	response = call(AgentProfileConversationRequest{ConversationKey: "team:chat:remove", RemovalConfirmation: preview.Confirmation, RemovalChoices: map[string]string{preview.Dependencies[0].ID: "team:chat:keep"}})
	if response.Code != http.StatusNoContent {
		t.Fatalf("confirm: %d %s", response.Code, response.Body.String())
	}
	if projectChatExists(ctx, "owner", "work", "team", "team:chat:remove") || !projectChatExists(ctx, "owner", "work", "team", "team:chat:keep") {
		t.Fatal("removed wrong agent")
	}
	var saved struct {
		Schedules []struct {
			Enabled bool
			Action  struct{ Tab string }
		}
	}
	if err = json.Unmarshal([]byte(f.read(t, externalCommandCrewState+"/schedules.json")), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 1 || saved.Schedules[0].Action.Tab != "team:chat:keep" || saved.Schedules[0].Enabled {
		t.Fatal("linked work was lost, not reassigned, or enabled")
	}
}
