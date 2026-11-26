package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// In the Mac app an agent's list of the Crew's agents left out the teammates' agents the left menu shows, so a
// group of three read as two. They come from the Team's server, with the left menu's ":team:" key.
func TestMacTeammateChatsListsTeammatesAgentsFromTheTeamServer(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent-profiles/work/side-chats" || r.URL.Query().Get("project_id") != "server-crew" || r.Header.Get("Authorization") != "Bearer saved-sign-in" {
			http.Error(w, "unauthorized", 401)
			return
		}
		externalJSON(w, []AgentProfileSideChat{
			{ConversationKey: "server-crew:chat:mine", SessionID: "s1", Name: "Mine", CanWrite: true, TabOwnerID: "owner"},
			{ConversationKey: "server-crew:chat:theirs", SessionID: "s2", Name: "Their agent", TabOwnerID: "teammate", TabOwnerName: "Teammate", Purpose: "Reviews"},
			{ConversationKey: "server-crew:chat:root-teammate", SessionID: "s3", Name: "Main", TabOwnerID: "teammate", IsMain: true},
		})
	}))
	defer server.Close()
	if err := writeDesktopConnections("owner", []desktopConnection{{ID: "conn", Name: "Example company", Server: server.URL, Token: "saved-sign-in"}}); err != nil {
		t.Fatal(err)
	}
	if err := crewroot.WriteSync("mac-crew", crewroot.SyncBinding{PairID: "pair", Side: "local", Editing: "local", ConnectionID: "conn", PeerProjectID: "server-crew"}); err != nil {
		t.Fatal(err)
	}
	got := macTeammateChats(context.Background(), "owner", "mac-crew")
	if len(got) != 1 || got[0].Key != "mac-crew:team:server-crew:chat:theirs" || got[0].Name != "Their agent" || got[0].OwnerName != "Teammate" || !got[0].Teammate {
		t.Fatalf("teammates' agents = %+v", got)
	}
}
