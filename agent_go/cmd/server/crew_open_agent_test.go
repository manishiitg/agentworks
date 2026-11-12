package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// PLAT-746-885: deleting Main must not break Crew opening or expose private
// owner agents to a Run person. Opening must not save a replacement Main.
func TestCrewOpeningWithoutMainUsesAuthorizedExistingAgent(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	ctx := internalBotRequestContext(t.Context(), "owner")
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	for _, key := range []string{"team:chat:alpha", "team:chat:beta"} {
		binding, _, e := resolveConversationBindingForUser(ctx, "owner", profile, key)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.resolveOrCreate(ctx, "owner", profile, binding, ""); e != nil {
			t.Fatal(e)
		}
	}
	// A stale deleted Main pointer is not repaired or adopted.
	f.write(t, externalCommandCrewState+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Test Team","main_chat_key":"team:chat:deleted"}`)
	before := f.read(t, externalCommandCrewState+"/product.json")
	open := func(user, key, requested string) (int, AgentProfileConversationResponse) {
		t.Helper()
		raw, _ := json.Marshal(AgentProfileConversationRequest{ConversationKey: key, RequestedAgent: requested})
		r := httptest.NewRequest(http.MethodPost, "/api/agent-profiles/work/conversation", strings.NewReader(string(raw)))
		r = r.WithContext(internalBotRequestContext(t.Context(), user))
		r = mux.SetURLVars(r, map[string]string{"id": "work"})
		w := httptest.NewRecorder()
		f.api.handleResolveAgentProfileConversation(w, r)
		var body AgentProfileConversationResponse
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	for _, requested := range []string{"", "team:chat:beta"} {
		status, body := open("owner", "team", requested)
		want := requested
		if want == "" {
			want = "team:chat:alpha"
		}
		if status != 200 || body.ConversationKey != want || body.SessionID == "" || body.IsMainChat {
			t.Fatalf("owner open: status=%d result=%+v", status, body)
		}
	}
	if status, body := open("other", "team", ""); status != 200 || !body.ChooseAgent || body.SessionID != "" {
		t.Fatalf("private agents exposed to Run person: %d %+v", status, body)
	}
	if status, _ := open("owner", "team", "team:chat:deleted"); status == 200 {
		t.Fatal("deleted selection fell back")
	}
	if f.read(t, externalCommandCrewState+"/product.json") != before {
		t.Fatal("opening saved a replacement Main")
	}
}
