package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// A real signed-in recipient's dashboard cannot adopt the owner's chat or
// change its recorded owner; reopening resolves the same private recipient chat.
// A shared Crew needs no saved dashboard to open that conversation.
func TestProjectAskConversationKeepsCrewCallerOwnership(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AUTH_SECRET", "project-chat-ownership-test-secret")
	f.api.eventStore = storeevents.NewEventStore(50)
	router := mux.NewRouter()
	router.HandleFunc("/api/project-ask/conversation", f.api.handleProjectAskConversation).Methods(http.MethodPost)
	server := httptest.NewServer(AuthMiddleware(router))
	defer server.Close()
	call := func(user string) (int, map[string]any) {
		t.Helper()
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"workspace": externalCommandCrewRoot})
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/project-ask/conversation", bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		var out map[string]any
		if json.Unmarshal(body, &out) != nil {
			t.Fatalf("bad response: %s", body)
		}
		return response.StatusCode, out
	}
	status, owner := call("owner")
	if status != 200 {
		t.Fatalf("owner: %d %v", status, owner)
	}
	ownerID := owner["conversation"].(map[string]any)["session_id"].(string)
	f.api.eventStore.SetSessionOwner(ownerID, "owner")
	status, reader := call("other")
	if status != 200 || reader["mode"] != "run" {
		t.Fatalf("recipient: %d %v", status, reader)
	}
	readerID := reader["conversation"].(map[string]any)["session_id"].(string)
	if ownerID == readerID || f.api.eventStore.GetSessionOwner(ownerID) != "owner" {
		t.Fatal("recipient acquired or changed the owner's session")
	}
	_, again := call("other")
	if again["conversation"].(map[string]any)["session_id"] != readerID {
		t.Fatal("recipient chat identity changed on reopen")
	}

	listed, err := dashboardWorkspaceRequest(t.Context(), dashboards.Request{Root: externalCommandCrewRoot, Action: "list", IncludeDraft: true})
	if err != nil || len(listed.Dashboards) != 0 {
		t.Fatalf("Ask required or created a dashboard: %+v %v", listed, err)
	}
	// A conflicting stored owner is a refusal, never repaired by adoption.
	f.api.eventStore.SetSessionOwner(readerID, "owner")
	if status, _ := call("other"); status != 403 && status != 409 {
		t.Fatalf("conflicting session owner accepted: %d", status)
	}
	if f.api.eventStore.GetSessionOwner(readerID) != "owner" {
		t.Fatal("resolver rewrote the stored owner")
	}
}
