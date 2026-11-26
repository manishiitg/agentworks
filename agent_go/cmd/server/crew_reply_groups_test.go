package server

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// #4271: a stale cached PR returns immediately and refreshes in the background
// so 60 s card polls never block on GitHub.
func TestReplyGroupPullRequestCacheServesStaleWhileRevalidating(t *testing.T) {
	var state atomic.Value
	state.Store("open")
	gate := make(chan struct{})
	var gateActive atomic.Bool
	refreshed := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			if gateActive.Load() {
				<-gate
			}
			if state.Load().(string) == "merged" {
				w.Write([]byte(`[{"number":9,"html_url":"https://github.com/acme/swr/pull/9","state":"closed","merged_at":"2026-01-01T00:00:00Z","head":{"sha":"def"}}]`))
			} else {
				w.Write([]byte(`[{"number":9,"html_url":"https://github.com/acme/swr/pull/9","state":"open","head":{"sha":"abc"}}]`))
			}
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Write([]byte(`{"check_runs":[]}`))
		case strings.HasSuffix(r.URL.Path, "/status"):
			w.Write([]byte(`{"state":"success","total_count":0}`))
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/pulls/9"):
			w.Write([]byte(`{"mergeable_state":"clean"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	old := githubAPIURL
	githubAPIURL = srv.URL
	defer func() { githubAPIURL = old }()

	pr := cachedGitHubPullRequestStatus(context.Background(), "swr-token", "acme/swr", "feat-swr")
	if pr == nil || pr.State != "open" {
		t.Fatalf("initial pr = %+v, want open", pr)
	}

	// Mark the entry stale (past expires, still within staleUntil) and block
	// the fake GitHub server so a synchronous fetch would stall.
	key := sha256.Sum256([]byte(srv.URL + "\x00swr-token\x00acme/swr\x00feat-swr"))
	replyPRCache.Lock()
	entry := replyPRCache.entries[key]
	entry.expires = time.Now().Add(-time.Second)
	replyPRCache.entries[key] = entry
	replyPRCache.Unlock()

	state.Store("merged")
	gateActive.Store(true)

	start := time.Now()
	stale := cachedGitHubPullRequestStatus(context.Background(), "swr-token", "acme/swr", "feat-swr")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("stale hit blocked for %v", elapsed)
	}
	if stale == nil || stale.State != "open" {
		t.Fatalf("stale pr = %+v, want cached open", stale)
	}

	// Release the background refresh and wait for it to update the cache.
	close(gate)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		replyPRCache.Lock()
		cur := replyPRCache.entries[key]
		busy := replyPRCache.refreshing[key]
		replyPRCache.Unlock()
		if !busy && cur.pr != nil && cur.pr.State == "merged" {
			refreshed <- struct{}{}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-refreshed:
	default:
		t.Fatal("background refresh did not update cached PR to merged")
	}
}

// #4271: multiple repos in a reply card resolve their PR status concurrently.
func TestReplyGroupPullRequestsFetchConcurrently(t *testing.T) {
	var arrived atomic.Int32
	allArrived := make(chan struct{})
	var once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			if arrived.Add(1) == 3 {
				once.Do(func() { close(allArrived) })
			}
			select {
			case <-allArrived:
			case <-time.After(2 * time.Second):
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
			w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	old := githubAPIURL
	githubAPIURL = srv.URL
	defer func() { githubAPIURL = old }()

	groups := []filesnapshots.Group{
		{Kind: "repo", Branch: "feat-1", GitHubRepo: "acme/r1"},
		{Kind: "repo", Branch: "feat-2", GitHubRepo: "acme/r2"},
		{Kind: "repo", Branch: "feat-3", GitHubRepo: "acme/r3"},
	}
	attachPullRequests(context.Background(), groups, "conc-token", true)
	if arrived.Load() != 3 {
		t.Fatalf("arrived = %d, want 3", arrived.Load())
	}
	for i, g := range groups {
		if g.PR != nil {
			t.Fatalf("groups[%d].PR = %+v, want nil (no PR)", i, g.PR)
		}
	}
}
