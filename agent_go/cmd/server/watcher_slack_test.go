package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// A Watcher reads Slack, read-only (owner, 5 Nov): its check's bridge session may call only Slack read tools, sending is
// refused, the session ends with the check, and its token cannot reach another check's session.
func TestWatcherSlackSessionReadsOnlyAndOnlyItsOwnCheck(t *testing.T) {
	if !watcherSlackToolAllowed("slack_c-35605e3f__slack_read_channel") || !watcherSlackToolAllowed("slack_search_public_and_private") {
		t.Fatal("a Slack read tool was refused")
	}
	for _, tool := range []string{"slack_c-35605e3f__slack_send_message", "slack_send_message_draft", "slack_schedule_message", "slack_create_conversation", "slack_add_reaction"} {
		if watcherSlackToolAllowed(tool) {
			t.Fatalf("%s is allowed for a Watcher, but it changes Slack", tool)
		}
	}
	api := &StreamingAPI{}
	session := watcherSlackSessionPrefix + "check-a"
	api.watcherSlackSessions.Store(session, watcherSlackScope{jobID: "job-a", userID: "owner", workspacePath: "Crew/example", expires: time.Now().Add(time.Minute)})
	if _, isWatcher, err := api.resolveWatcherSlackMCPServer(context.Background(), session, "vault_slack", "slack_c-35605e3f__slack_send_message"); !isWatcher || err == nil {
		t.Fatal("a Watcher check could send a Slack message")
	}
	if person, _ := api.watcherSlackPerson(session); person != "owner" {
		t.Fatalf("the check's calls do not run as its owner: %q", person)
	}
	api.watcherSlackSessions.Delete(session)
	if _, isWatcher, err := api.resolveWatcherSlackMCPServer(context.Background(), session, "vault_slack", "slack_read_channel"); !isWatcher || err == nil {
		t.Fatal("a finished check's session still reads Slack")
	}
	// A check killed before its stop ran: the session still ends at the check's time limit.
	stale := watcherSlackSessionPrefix + "check-killed"
	api.watcherSlackSessions.Store(stale, watcherSlackScope{jobID: "job-a", userID: "owner", workspacePath: "Crew/example", expires: time.Now().Add(-time.Second)})
	if person, _ := api.watcherSlackPerson(stale); person != "" {
		t.Fatal("an expired check's session still runs as its owner")
	}
	if _, isWatcher, _ := api.resolveWatcherSlackMCPServer(context.Background(), "chat-session", "vault_slack", "slack_read_channel"); isWatcher {
		t.Fatal("an ordinary chat session was treated as a Watcher check")
	}
	// The check's token belongs to its own session: calling another check's session with it is refused.
	common.SetBridgeTokenSecret("server-secret")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })
	router := mux.NewRouter()
	scoped := router.PathPrefix("/s/{session_id}/tools").Subrouter()
	scoped.Use(bridgeAuthMiddleware("server-secret"))
	scoped.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	call := func(path string) int {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+common.BridgeTokenForSession(watcherSlackSessionPrefix+"check-a"))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	if code := call("/s/" + watcherSlackSessionPrefix + "check-b/tools/mcp/vault_slack/slack_read_channel"); code != http.StatusForbidden {
		t.Fatalf("one check's token reached another check's session: %d", code)
	}
	if code := call("/s/" + watcherSlackSessionPrefix + "check-a/tools/mcp/vault_slack/slack_read_channel"); code != http.StatusOK {
		t.Fatalf("a check's own token was refused on its own session: %d", code)
	}
}
