package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An inbox participant may resolve current names, including after a rename;
// another person cannot probe that inbox. Same-account messages use their
// actual message ID rather than guessing which of the person's agents sent it.
func TestAgentMessageIdentityAdmissionAndRename(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"sender","username":"Sender Person"},{"id":"recipient","username":"Recipient Person"},{"id":"outsider","username":"Outside Person"}]}`)
	state := agentMessageStore{Agents: []namedMessageAgent{{ID: "source", UserID: "sender", Name: "reviewer", Label: "Reviewer"}, {ID: "destination", UserID: "recipient", Name: "worker", Label: "Worker"}}, Conversations: []agentConversation{{ID: "inbox-example", Endpoints: [2]agentMessageEndpoint{{UserID: "sender", NamedAgentID: "source"}, {UserID: "recipient", NamedAgentID: "destination"}}, Messages: []agentMessage{{ID: "message-example", From: 0, Text: "private body"}}}}}
	save := func() {
		t.Helper()
		if err := saveAgentMessageStore(t.Context(), state); err != nil {
			t.Fatal(err)
		}
	}
	save()
	api := &StreamingAPI{}
	read := func(user string) (int, string) {
		request := httptest.NewRequest(http.MethodGet, "/api/agent-message-identity?inbox_id=inbox-example&message_id=message-example", nil)
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: user}))
		response := httptest.NewRecorder()
		api.handleAgentMessageIdentity(response, request)
		return response.Code, response.Body.String()
	}
	code, body := read("recipient")
	if code != 200 || !strings.Contains(body, "Sender Person · Reviewer") || strings.Contains(body, "private body") {
		t.Fatalf("wrong authorized identity: %d %s", code, body)
	}
	if code, _ := read("outsider"); code != 404 {
		t.Fatalf("nonparticipant read identity: %d", code)
	}
	state.Agents[0].Label = "Updated reviewer"
	save()
	if code, body := read("recipient"); code != 200 || !strings.Contains(body, "Updated reviewer") {
		t.Fatalf("rename stale: %d %s", code, body)
	}
	state.Agents[1].UserID = "sender"
	state.Conversations[0].Endpoints[1].UserID = "sender"
	save()
	if code, body := read("sender"); code != 200 || !strings.Contains(body, "Updated reviewer") {
		t.Fatalf("same-person sender guessed: %d %s", code, body)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	// Display metadata only (name, route and avatar), never the message itself.
	for key := range decoded {
		if key != "label" && key != "address" && !strings.HasPrefix(key, "avatar_") {
			t.Fatalf("identity read returned more than display metadata: %s", body)
		}
	}
}
