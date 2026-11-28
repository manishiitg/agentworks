package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestDesktopTeamStatusSWRServesFreshAndStaleAndInvalidates(t *testing.T) {
	var teamCalls atomic.Int32
	var clashCalls atomic.Int32
	var returnForbidden atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if returnForbidden.Load() {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"revoked"}}`))
			return
		}
		switch r.URL.Path {
		case "/api/crew-teams/proj-1":
			n := teamCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(crewTeam{
				ProjectID:    "proj-1",
				FolderAccess: true,
				Folder:       crewTeamFolder{ID: "folder-1"},
				Name:         "Team-" + string(rune('0'+n)),
			})
		case "/api/crew-teams/proj-1/clashes":
			clashCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"clashes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	conn := desktopConnection{ID: "conn-team-swr-test", Server: srv.URL, Token: "tok"}
	t.Cleanup(func() {
		invalidateDesktopTeamStatusCache("owner-1", conn.ID, "proj-1")
		clearDesktopProxyUnreachable(conn.ID)
	})
	invalidateDesktopTeamStatusCache("owner-1", conn.ID, "proj-1")
	clearDesktopProxyUnreachable(conn.ID)

	// Cold fetch hits remote once for team + once for clashes.
	first, reachable, err := cachedDesktopTeamStatus(context.Background(), "owner-1", conn, "proj-1")
	if err != nil || !reachable || !first.team.FolderAccess || !first.clashesOK {
		t.Fatalf("first fetch = %+v reachable=%t err=%v", first, reachable, err)
	}
	if teamCalls.Load() != 1 || clashCalls.Load() != 1 {
		t.Fatalf("cold calls = (%d, %d), want (1, 1)", teamCalls.Load(), clashCalls.Load())
	}

	// Fresh cache hit skips remote completely.
	second, reachable, err := cachedDesktopTeamStatus(context.Background(), "owner-1", conn, "proj-1")
	if err != nil || !reachable || second.team.Name != first.team.Name {
		t.Fatalf("fresh hit = %+v reachable=%t err=%v", second, reachable, err)
	}
	if teamCalls.Load() != 1 || clashCalls.Load() != 1 {
		t.Fatalf("fresh hit called remote: (%d, %d)", teamCalls.Load(), clashCalls.Load())
	}

	// Expire fresh TTL (keep stale valid): serves stale immediately and refreshes in background.
	key := desktopTeamStatusCacheKey("owner-1", conn.ID, "proj-1")
	desktopTeamStatusCache.Lock()
	entry := desktopTeamStatusCache.entries[key]
	entry.expires = time.Now().Add(-time.Second)
	desktopTeamStatusCache.entries[key] = entry
	desktopTeamStatusCache.Unlock()

	stale, reachable, err := cachedDesktopTeamStatus(context.Background(), "owner-1", conn, "proj-1")
	if err != nil || !reachable || stale.team.Name != first.team.Name {
		t.Fatalf("stale hit = %+v reachable=%t err=%v", stale, reachable, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && teamCalls.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if teamCalls.Load() < 2 {
		t.Fatalf("background refresh did not run, teamCalls=%d", teamCalls.Load())
	}

	// Explicit invalidation forces a fresh fetch; 403 error is not cached.
	returnForbidden.Store(true)
	invalidateDesktopTeamStatusCache("owner-1", conn.ID, "proj-1")
	if _, _, err := cachedDesktopTeamStatus(context.Background(), "owner-1", conn, "proj-1"); err == nil {
		t.Fatal("expected error after invalidation and 403")
	}
}

func TestCachedReplyGroupChangesSWRServesStaleAndRefreshes(t *testing.T) {
	origFn := replyGroupChangesFn
	t.Cleanup(func() {
		replyGroupChangesFn = origFn
		replyGroupCache.Lock()
		replyGroupCache.entries = make(map[string]replyGroupCacheEntry)
		replyGroupCache.refreshing = make(map[string]bool)
		replyGroupCache.Unlock()
	})
	replyGroupCache.Lock()
	replyGroupCache.entries = make(map[string]replyGroupCacheEntry)
	replyGroupCache.refreshing = make(map[string]bool)
	replyGroupCache.Unlock()

	var runs atomic.Int32
	blockRefresh := make(chan struct{})
	refreshStarted := make(chan struct{}, 1)

	replyGroupChangesFn = func(_ context.Context, _ string, changes []filesnapshots.Change, _ []string, allow func(string) bool) []filesnapshots.Group {
		n := runs.Add(1)
		if n == 2 {
			select {
			case refreshStarted <- struct{}{}:
			default:
			}
			<-blockRefresh
		}
		branch := "v1"
		if n >= 2 {
			branch = "v2"
		}
		return []filesnapshots.Group{{
			Kind:   "repo",
			Root:   "repo-a",
			Name:   "repo-a",
			Branch: branch,
			Files:  changes,
		}}
	}

	roots := crewroot.Roots{Files: "/tmp/reply-group-swr-test"}
	comp := filesnapshots.Comparison{
		Record:  filesnapshots.Record{ID: "snap-swr-1"},
		Changes: []filesnapshots.Change{{Path: "repo-a/a.go", Status: "M"}},
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice"})

	g1 := cachedReplyGroupChanges(ctx, roots, comp, func(string) bool { return true })
	if len(g1) != 1 || g1[0].Branch != "v1" || runs.Load() != 1 {
		t.Fatalf("cold = %+v runs=%d", g1, runs.Load())
	}

	// Expire fresh TTL but keep staleUntil valid.
	key := replyGroupCacheKey("alice", roots.Files, comp)
	replyGroupCache.Lock()
	entry := replyGroupCache.entries[key]
	entry.expires = time.Now().Add(-time.Second)
	replyGroupCache.entries[key] = entry
	replyGroupCache.Unlock()

	start := time.Now()
	g2 := cachedReplyGroupChanges(ctx, roots, comp, func(string) bool { return true })
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("stale hit blocked for %v", elapsed)
	}
	if len(g2) != 1 || g2[0].Branch != "v1" {
		t.Fatalf("stale hit = %+v, want v1", g2)
	}

	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background refresh did not start")
	}
	close(blockRefresh)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		replyGroupCache.Lock()
		refreshed := replyGroupCache.entries[key]
		replyGroupCache.Unlock()
		if len(refreshed.groups) == 1 && refreshed.groups[0].Branch == "v2" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background refresh did not update cached entry to v2")
}

func TestLoadProviderKeysCachesAndInvalidatesOnSave(t *testing.T) {
	var getCalls atomic.Int32
	var stored string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			if stored == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"content": stored},
			})
		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			stored = body.Content
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	t.Setenv("WORKSPACE_API_URL", srv.URL)
	invalidateProviderKeysCache()
	t.Cleanup(invalidateProviderKeysCache)

	if err := SaveProviderKeys(context.Background(), &StoredProviderKeys{OpenAI: "sk-test-1"}); err != nil {
		t.Fatalf("SaveProviderKeys() err = %v", err)
	}
	k1, err := LoadProviderKeys(context.Background())
	if err != nil || k1 == nil || k1.OpenAI != "sk-test-1" {
		t.Fatalf("first LoadProviderKeys = %+v err=%v", k1, err)
	}
	// Mutating returned struct must not corrupt cached value.
	k1.OpenAI = "mutated"

	k2, err := LoadProviderKeys(context.Background())
	if err != nil || k2 == nil || k2.OpenAI != "sk-test-1" {
		t.Fatalf("second LoadProviderKeys = %+v err=%v", k2, err)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("workspace GET calls = %d, want 1 (cached)", getCalls.Load())
	}

	// Saving invalidates cache so next LoadProviderKeys sees the updated key.
	if err := SaveProviderKeys(context.Background(), &StoredProviderKeys{OpenAI: "sk-test-2"}); err != nil {
		t.Fatalf("second SaveProviderKeys() err = %v", err)
	}
	k3, err := LoadProviderKeys(context.Background())
	if err != nil || k3 == nil || k3.OpenAI != "sk-test-2" {
		t.Fatalf("third LoadProviderKeys = %+v err=%v", k3, err)
	}
	if getCalls.Load() != 2 {
		t.Fatalf("workspace GET calls after save = %d, want 2", getCalls.Load())
	}
}

func TestIdleCompactionHoldsTurnQueueAndSendWaitBoundsRun(t *testing.T) {
	const sessionID = "idle-compact-queue-hold-test"
	api := newConversationTurnQueueTestAPI(map[string]string{})
	ctx := context.Background()
	if _, _, err := api.enqueueConversationTurn(ctx, "person-1", sessionID, QueryRequest{Query: "hello", AgentMode: "multi-agent"}); err != nil {
		t.Fatal(err)
	}

	run := newIdleCompactRun()
	if !idleCompacts.start(sessionID, run, 10, time.Now()) {
		t.Fatal("could not register idle compaction")
	}
	t.Cleanup(func() { idleCompacts.end(sessionID, run) })

	canceled := make(chan struct{})
	if !run.begin(func() {
		close(canceled)
		run.finish(llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonFailed}, context.Canceled)
	}) {
		t.Fatal("could not begin idle compaction")
	}

	// Queue must stay held while any idle compaction is running (not just beforeQueued).
	api.kickConversationTurnQueue(sessionID)
	if !api.conversationTurnHasBacklog("person-1", sessionID) || api.conversationTurnDispatchPending(sessionID) {
		t.Fatal("queued turn should wait while idle compaction is running")
	}

	// Bounded send wait stops a stalled compaction after wait expires.
	api.ensureIdleCompactionSendWaitBound(sessionID, run, 50*time.Millisecond)
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("ensureIdleCompactionSendWaitBound did not cancel stalled compaction")
	}
}
