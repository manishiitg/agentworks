package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
)

// The Crew owner's connected GitHub account decides PR status: merged,
// failing checks and a requested change map to the card's fields, and the
// token travels only in the request header.
func TestReplyGroupPullRequestStatusFromOwnersConnection(t *testing.T) {
	reviewsBody := `[{"state":"APPROVED","user":{"login":"a"}},{"state":"CHANGES_REQUESTED","user":{"login":"b"}},{"state":"COMMENTED","user":{"login":"c"}}]`
	reviewsUnavailable := false
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer owner-token" {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			if r.URL.Query().Get("head") != "acme:feature-x" {
				t.Errorf("head = %q", r.URL.Query().Get("head"))
			}
			w.Write([]byte(`[{"number":7,"html_url":"https://github.com/acme/app/pull/7","state":"closed","merged_at":"2026-01-01T00:00:00Z","head":{"sha":"abc"}}]`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"check_runs":[{"status":"completed","conclusion":"success"},{"name":"build","status":"completed","conclusion":"failure"}]}`))
		case strings.HasSuffix(r.URL.Path, "/status"):
			w.Write([]byte(`{"state":"success","total_count":0}`))
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			if reviewsUnavailable {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Write([]byte(reviewsBody))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	old := githubAPIURL
	githubAPIURL = srv.URL
	defer func() { githubAPIURL = old }()

	groups := []filesnapshots.Group{{Kind: "repo", Branch: "feature-x", GitHubRepo: "acme/app"}, {Kind: "crew_files"}}
	attachPullRequests(context.Background(), groups, "owner-token", true)
	pr := groups[0].PR
	if pr == nil || pr.Number != 7 || pr.State != "merged" || pr.Checks != "failing" || pr.Review != "changes_requested" || len(pr.FailingChecks) != 1 || pr.FailingChecks[0] != "build" {
		t.Fatalf("pr = %+v", pr)
	}
	if groups[1].PR != nil {
		t.Fatal("non-Git group must not get a PR")
	}
	// Multiple cards reuse the same owner connection's read for 45 seconds.
	readCount := requests
	attachPullRequests(context.Background(), groups, "owner-token", true)
	if requests != readCount {
		t.Fatal("cached cards repeated the GitHub read")
	}
	attachPullRequests(context.Background(), groups, "another-token", true)
	if requests == readCount || groups[0].PR.State != "unavailable" {
		t.Fatal("another connection reused cached owner status")
	}
	// PLAT-746-220: Codex submits COMMENTED reviews, not approval votes.
	// Keep those visible without upgrading them to approvals or change requests.
	for _, tc := range []struct {
		body, want  string
		unavailable bool
	}{
		{`[{"state":"COMMENTED","user":{"login":"review-bot"}}]`, "commented", false},
		{`[{"state":"APPROVED","user":{"login":"a"}},{"state":"COMMENTED","user":{"login":"a"}}]`, "approved", false},
		{`[]`, "none", false},
		{`[]`, "unavailable", true},
	} {
		reviewsBody, reviewsUnavailable = tc.body, tc.unavailable
		got := githubPullRequestStatus(context.Background(), "owner-token", "acme/app", "feature-x")
		if got == nil || got.Review != tc.want {
			t.Fatalf("review=%+v want %s", got, tc.want)
		}
	}

}

// Without a GitHub connection for the Crew owner the server's own login is
// never used: the group says to connect GitHub.
func TestReplyGroupWithoutOwnerConnectionAsksToConnectGitHub(t *testing.T) {
	groups := []filesnapshots.Group{{Kind: "repo", Branch: "feature-x", GitHubRepo: "acme/app"}}
	attachPullRequests(context.Background(), groups, "", false)
	if groups[0].PR == nil || groups[0].PR.State != "connect_github" {
		t.Fatalf("pr = %+v", groups[0].PR)
	}
}
