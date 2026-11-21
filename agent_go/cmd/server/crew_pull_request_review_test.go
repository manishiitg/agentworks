package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// The PR panel reads one pull request with the owner's token: plain status,
// whether GitHub says it can merge, its files and its review comments with
// file and line (an outdated one keeps its original line).
func TestPullRequestReviewReadsStatusFilesAndComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owner-token" {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/12"):
			w.Write([]byte(`{"number":12,"html_url":"https://github.com/acme/app/pull/12","state":"open","title":"Fix login","body":"Why","mergeable":true,"mergeable_state":"clean","additions":3,"deletions":1,"changed_files":2,"user":{"login":"dev"},"head":{"sha":"abc","ref":"chat/fix-login"},"base":{"ref":"main"}}`))
		case strings.HasSuffix(r.URL.Path, "/pulls/12/files"):
			w.Write([]byte(`[{"filename":"a.go","status":"modified","additions":2,"deletions":1,"patch":"@@ -1 +1,2 @@\n-x\n+y\n+z"},{"filename":"b.png","status":"added","additions":0,"deletions":0}]`))
		case strings.HasSuffix(r.URL.Path, "/pulls/12/reviews"):
			w.Write([]byte(`[{"state":"CHANGES_REQUESTED","body":"Please rename","user":{"login":"rev"}},{"state":"COMMENTED","body":"","user":{"login":"rev"}}]`))
		case strings.HasSuffix(r.URL.Path, "/pulls/12/comments"):
			w.Write([]byte(`[{"path":"a.go","line":2,"body":"use y2","user":{"login":"rev"}},{"path":"a.go","line":null,"original_line":9,"body":"old","user":{"login":"rev"}},{"path":"a.go","line":2,"body":"ok","in_reply_to_id":1,"user":{"login":"dev"}}]`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"total_count":1,"check_runs":[{"name":"test","status":"completed","conclusion":"success"}]}`))
		case strings.HasSuffix(r.URL.Path, "/status"):
			w.Write([]byte(`{"state":"success","total_count":0}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	old := githubAPIURL
	githubAPIURL = srv.URL
	defer func() { githubAPIURL = old }()

	pr, ok := githubPullRequestReview(context.Background(), "owner-token", "acme/app", 12)
	if !ok || pr.Title != "Fix login" || pr.Head != "chat/fix-login" || pr.Base != "main" || !pr.Mergeable {
		t.Fatalf("pr = %+v", pr)
	}
	if pr.Status.State != "open" || pr.Status.Checks != "passing" || pr.Status.Review != "changes_requested" {
		t.Fatalf("status = %+v", pr.Status)
	}
	if len(pr.Files) != 2 || pr.Files[0].Patch == "" || pr.Files[1].Patch != "" || pr.MoreFiles {
		t.Fatalf("files = %+v", pr.Files)
	}
	if len(pr.Comments) != 4 {
		t.Fatalf("comments = %+v", pr.Comments)
	}
	if c := pr.Comments[0]; c.Review != "changes_requested" || c.Path != "" || c.Body != "Please rename" {
		t.Fatalf("review summary = %+v", c)
	}
	if c := pr.Comments[1]; c.Path != "a.go" || c.Line != 2 || c.Outdated {
		t.Fatalf("line comment = %+v", c)
	}
	if c := pr.Comments[2]; c.Line != 9 || !c.Outdated {
		t.Fatalf("outdated comment = %+v", c)
	}
	if !pr.Comments[3].Reply {
		t.Fatal("a reply must be marked")
	}
	if _, ok := githubPullRequestReview(context.Background(), "other-token", "acme/app", 12); ok {
		t.Fatal("a refused read must not return a pull request")
	}
}

// A chat reader may open PRs only in repositories the chat worked in (its
// saved reply groups) or whose PRs its turns linked, never any repository the
// owner's GitHub login can see.
func TestPullRequestReviewOnlyThisChatsRepositories(t *testing.T) {
	api := &StreamingAPI{eventStore: storeevents.NewEventStore(100)}
	summary, _ := json.Marshal(map[string]any{"summary": map[string]any{"groups": []map[string]any{{"kind": "repo", "root": "app", "name": "app", "github_repo": "Acme/App", "new_checkout": false, "files": []any{}, "total_files": 0, "more_count": 0}}}})
	history := chatFileReview{Replies: []chatSnapshotReference{{ID: "r1"}}, replyData: map[string]json.RawMessage{"r1": summary}}
	link := func(root, repo string) {
		data := storeevents.NewGenericEventData("reply_pull_requests", map[string]interface{}{
			"pull_requests": []linkedPullRequest{{Repository: repo, Number: 3, URL: "https://github.com/" + repo + "/pull/3", State: "open"}}, "workspace_path": root,
		})
		event := unifiedevents.NewAgentEvent(data)
		api.eventStore.AddEvent("s1", storeevents.Event{ID: "reply-prs:" + repo, Type: "reply_pull_requests", Timestamp: time.Now(), SessionID: "s1", Data: event})
	}
	link("Crew/one", "acme/lib")
	link("Crew/other", "acme/secret")
	repos := api.chatPullRequestRepos(history, "s1", "Crew/one")
	if !repos["acme/app"] || !repos["acme/lib"] {
		t.Fatalf("repos = %v", repos)
	}
	if repos["acme/secret"] || len(repos) != 2 {
		t.Fatalf("another Crew's linked PR leaked: %v", repos)
	}
	for _, bad := range []string{"acme", "acme/app/x", "../x", "acme/..", "a b/c", ""} {
		if validGitHubRepo(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
}
