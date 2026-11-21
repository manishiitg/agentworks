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
	"github.com/manishiitg/mcpagent/toolguard"
	"github.com/mark3labs/mcp-go/mcp"
)

// A Watcher only reads (owner, 5 Nov; every connected app since #3476): a check's app call runs only for Slack's
// known read tools or a tool its app marks read-only; writes, unmarked tools and platform tools are refused. The
// session ends with the check, and its token cannot reach another check's session.
func TestWatcherSessionReadsOnlyAndOnlyItsOwnCheck(t *testing.T) {
	session := watcherReadSessionPrefix + "check-a"
	hint := func(readOnly *bool, found bool) func(context.Context) (mcp.ToolAnnotation, bool, error) {
		return func(context.Context) (mcp.ToolAnnotation, bool, error) {
			return mcp.ToolAnnotation{ReadOnlyHint: readOnly}, found, nil
		}
	}
	yes, no := true, false
	for _, c := range []struct {
		name string
		call toolguard.Call
		runs bool
	}{
		{"Slack read tool", toolguard.Call{SessionID: session, Kind: toolguard.KindMCP, Tool: "slack_c-35605e3f__slack_read_channel", Annotations: hint(nil, true)}, true},
		{"Slack send", toolguard.Call{SessionID: session, Kind: toolguard.KindMCP, Tool: "slack_c-35605e3f__slack_send_message", Annotations: hint(nil, true)}, false},
		{"app tool marked read-only", toolguard.Call{SessionID: session, Kind: toolguard.KindMCP, Tool: "list_issues", Annotations: hint(&yes, true)}, true},
		{"app tool marked as writing", toolguard.Call{SessionID: session, Kind: toolguard.KindMCP, Tool: "create_issue", Annotations: hint(&no, true)}, false},
		{"app tool with no hint", toolguard.Call{SessionID: session, Kind: toolguard.KindMCP, Tool: "update_issue", Annotations: hint(nil, true)}, false},
		{"platform tool", toolguard.Call{SessionID: session, Kind: toolguard.KindCustom, Tool: "execute_shell_command"}, false},
		{"ordinary chat is untouched", toolguard.Call{SessionID: "chat-session", Kind: toolguard.KindMCP, Tool: "create_issue", Annotations: hint(&no, true)}, true},
	} {
		if runs := !watcherReadGuard(context.Background(), c.call).Stub; runs != c.runs {
			t.Errorf("%s: runs=%v, want %v", c.name, runs, c.runs)
		}
	}
	api := &StreamingAPI{}
	api.watcherReadSessions.Store(session, watcherReadScope{jobID: "job-a", userID: "owner", workspacePath: "Crew/example", expires: time.Now().Add(time.Minute)})
	if person, _ := api.watcherReadPerson(session); person != "owner" {
		t.Fatalf("the check's calls do not run as its owner: %q", person)
	}
	api.watcherReadSessions.Delete(session)
	if _, isWatcher, err := api.resolveWatcherReadMCPServer(context.Background(), session, "vault_slack", "slack_read_channel"); !isWatcher || err == nil {
		t.Fatal("a finished check's session still reads Slack")
	}
	// A check killed before its stop ran: the session still ends at the check's time limit.
	stale := watcherReadSessionPrefix + "check-killed"
	api.watcherReadSessions.Store(stale, watcherReadScope{jobID: "job-a", userID: "owner", workspacePath: "Crew/example", expires: time.Now().Add(-time.Second)})
	if person, _ := api.watcherReadPerson(stale); person != "" {
		t.Fatal("an expired check's session still runs as its owner")
	}
	if _, isWatcher, _ := api.resolveWatcherReadMCPServer(context.Background(), "chat-session", "vault_slack", "slack_read_channel"); isWatcher {
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
		r.Header.Set("Authorization", "Bearer "+common.BridgeTokenForSession(watcherReadSessionPrefix+"check-a"))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	if code := call("/s/" + watcherReadSessionPrefix + "check-b/tools/mcp/vault_slack/slack_read_channel"); code != http.StatusForbidden {
		t.Fatalf("one check's token reached another check's session: %d", code)
	}
	if code := call("/s/" + watcherReadSessionPrefix + "check-a/tools/mcp/vault_slack/slack_read_channel"); code != http.StatusOK {
		t.Fatalf("a check's own token was refused on its own session: %d", code)
	}
}
