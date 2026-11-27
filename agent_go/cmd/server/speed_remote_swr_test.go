package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// #4448: remote desktop inventory must serve stale cached results immediately
// while refreshing in the background, bound cold upstream timeouts, and enter
// connection cooldown after a timeout so polls fail fast.
func TestDesktopConnectionInventorySWRAndCooldown(t *testing.T) {
	desktopTestEnvironment(t)
	origTimeout := desktopInventoryTimeout
	origCooldown := desktopProxyCooldown
	desktopInventoryTimeout = 150 * time.Millisecond
	desktopProxyCooldown = 5 * time.Second
	t.Cleanup(func() {
		desktopInventoryTimeout = origTimeout
		desktopProxyCooldown = origCooldown
		desktopInventoryCacheMu.Lock()
		desktopInventoryCache = map[string]desktopInventoryCacheEntry{}
		desktopInventoryRefreshing = map[string]bool{}
		desktopInventoryCacheMu.Unlock()
		clearDesktopProxyUnreachable("saved-swr")
		clearDesktopProxyUnreachable("saved-cold")
	})

	const owner = "owner"
	var mode atomic.Int32 // 0 = fast v1, 1 = block until released then v2
	release := make(chan struct{})
	refreshHit := make(chan struct{}, 2)

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 1 {
			select {
			case refreshHit <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/workspaces") {
			if mode.Load() == 1 {
				_, _ = io.WriteString(w, `{"workspaces":[{"id":"crew-v2","name":"Crew V2"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"workspaces":[{"id":"crew-v1","name":"Crew V1"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"workflows":[]}`)
	}))
	defer remote.Close()

	slowRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slowRemote.Close()

	if err := writeDesktopConnections(owner, []desktopConnection{
		{ID: "saved-swr", Server: remote.URL, Token: "tok-1"},
		{ID: "saved-cold", Server: slowRemote.URL, Token: "tok-2"},
	}); err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{}
	callInventory := func(connID string) (int, []desktopProject, string, time.Duration) {
		t.Helper()
		r := desktopTestRequest("GET", "/api/desktop-connections/"+connID+"/inventory", "", owner)
		r = mux.SetURLVars(r, map[string]string{"connection": connID})
		w := httptest.NewRecorder()
		start := time.Now()
		api.handleDesktopConnectionInventory(w, r)
		dur := time.Since(start)
		var resp struct {
			Projects []desktopProject `json:"projects"`
			Status   string           `json:"status"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp.Projects, resp.Status, dur
	}

	// 1. Cold read populates cache.
	code, projects, status, _ := callInventory("saved-swr")
	if code != 200 || status != "connected" || len(projects) != 1 || projects[0].ID != "crew-v1" {
		t.Fatalf("cold read: code=%d status=%q projects=%+v", code, status, projects)
	}

	// 2. Mark entry stale and make upstream slow: read must return cached v1
	// immediately and trigger background refresh.
	cacheKey := owner + "\x00saved-swr\x00" + remote.URL
	desktopInventoryCacheMu.Lock()
	entry := desktopInventoryCache[cacheKey]
	entry.freshUntil = time.Now().Add(-time.Second)
	desktopInventoryCache[cacheKey] = entry
	desktopInventoryCacheMu.Unlock()

	mode.Store(1)
	code, projects, status, dur := callInventory("saved-swr")
	if dur > 100*time.Millisecond {
		t.Fatalf("stale inventory blocked for %v", dur)
	}
	if code != 200 || status != "connected" || len(projects) != 1 || projects[0].ID != "crew-v1" {
		t.Fatalf("stale read: code=%d status=%q projects=%+v", code, status, projects)
	}
	<-refreshHit
	close(release)

	// Wait for background refresh to update cache to crew-v2.
	deadline := time.Now().Add(2 * time.Second)
	updated := false
	for time.Now().Before(deadline) {
		_, p, _, _ := callInventory("saved-swr")
		if len(p) == 1 && p[0].ID == "crew-v2" {
			updated = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !updated {
		t.Fatalf("background inventory refresh did not update cache to crew-v2")
	}

	// 3. Cold miss on stalled server times out at desktopInventoryTimeout and
	// trips cooldown so the immediate next call fails fast.
	code, _, status, dur = callInventory("saved-cold")
	if code != 200 || status != "unavailable" || dur > 500*time.Millisecond {
		t.Fatalf("cold timeout: code=%d status=%q dur=%v", code, status, dur)
	}
	code, _, status, dur = callInventory("saved-cold")
	if code != 200 || status != "unavailable" || dur > 50*time.Millisecond {
		t.Fatalf("cooldown call: code=%d status=%q dur=%v", code, status, dur)
	}
}

// #4448: crewAccessRemoteRead must serve stale cached Team data immediately
// while refreshing in the background, and apply short failure backoff on cold
// misses so crewAccessSummaryFor + crewAccessTreeFor never double-block.
func TestCrewAccessRemoteReadSWRAndFailBackoff(t *testing.T) {
	origFresh := crewAccessRemoteFreshTTL
	origFail := crewAccessRemoteFailTTL
	origTimeout := crewAccessRemoteTimeout
	crewAccessRemoteFreshTTL = 50 * time.Millisecond
	crewAccessRemoteFailTTL = 5 * time.Second
	crewAccessRemoteTimeout = 120 * time.Millisecond
	t.Cleanup(func() {
		crewAccessRemoteFreshTTL = origFresh
		crewAccessRemoteFailTTL = origFail
		crewAccessRemoteTimeout = origTimeout
		dropCrewAccessCache("proj-swr", "peer-swr")
		dropCrewAccessCache("proj-fail", "peer-fail")
	})

	release := make(chan struct{})
	refreshHit := make(chan struct{}, 2)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case refreshHit <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"on":true,"owner_id":"owner"}`)
	}))
	defer remote.Close()

	slowRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slowRemote.Close()

	api := &StreamingAPI{}
	// 1. Seed a stale cached entry; crewAccessRemoteRead must return it immediately
	// without waiting for the slow upstream.
	key := "proj-swr\x00peer-swr"
	crewAccessRemoteCache.Store(key, crewAccessRemote{
		at:   time.Now().Add(-time.Minute),
		team: &crewTeam{On: true, OwnerID: "cached-owner"},
	})
	start := time.Now()
	got := api.crewAccessRemoteRead(context.Background(), "owner", desktopConnection{ID: "c1", Server: remote.URL, Token: "tok"}, "proj-swr", "peer-swr")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("stale crewAccessRemoteRead blocked for %v", elapsed)
	}
	if got.team == nil || got.team.OwnerID != "cached-owner" {
		t.Fatalf("stale team = %+v, want cached-owner", got.team)
	}
	<-refreshHit
	close(release)

	// 2. Cold miss against a stalled server times out once and caches failure
	// backoff so the immediately-following read returns without blocking again.
	cSlow := desktopConnection{ID: "c2", Server: slowRemote.URL, Token: "tok"}
	start = time.Now()
	_ = api.crewAccessRemoteRead(context.Background(), "owner", cSlow, "proj-fail", "peer-fail")
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("cold timeout took %v", elapsed)
	}
	start = time.Now()
	_ = api.crewAccessRemoteRead(context.Background(), "owner", cSlow, "proj-fail", "peer-fail")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("second call after cold failure blocked for %v (expected fail backoff)", elapsed)
	}
}

func TestCrewAccessRemoteParallelFollowUpsAndMarkStale(t *testing.T) {
	origFresh := crewAccessRemoteFreshTTL
	origTimeout := crewAccessRemoteTimeout
	crewAccessRemoteFreshTTL = time.Minute
	crewAccessRemoteTimeout = 2 * time.Second
	t.Cleanup(func() {
		crewAccessRemoteFreshTTL = origFresh
		crewAccessRemoteTimeout = origTimeout
		dropCrewAccessCache("proj-par", "peer-par")
	})

	var followUpCalls atomic.Int32
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	const perCallDelay = 90 * time.Millisecond

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/peer-par"):
			_, _ = io.WriteString(w, `{"on":true,"owner_id":"owner","folder_access":true,"folder":{"id":"f1"}}`)
		case strings.HasSuffix(r.URL.Path, "/agents"):
			followUpCalls.Add(1)
			cur := inFlight.Add(1)
			for prev := maxInFlight.Load(); cur > prev; prev = maxInFlight.Load() {
				if maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(perCallDelay)
			inFlight.Add(-1)
			_, _ = io.WriteString(w, `{"people":[{"user_id":"u1","name":"Alice"}]}`)
		case strings.HasSuffix(r.URL.Path, "/activity-posts"):
			followUpCalls.Add(1)
			cur := inFlight.Add(1)
			for prev := maxInFlight.Load(); cur > prev; prev = maxInFlight.Load() {
				if maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(perCallDelay)
			inFlight.Add(-1)
			_, _ = io.WriteString(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/clashes"):
			followUpCalls.Add(1)
			cur := inFlight.Add(1)
			for prev := maxInFlight.Load(); cur > prev; prev = maxInFlight.Load() {
				if maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(perCallDelay)
			inFlight.Add(-1)
			_, _ = io.WriteString(w, `{"clashes":[]}`)
		case strings.HasSuffix(r.URL.Path, "/events"):
			followUpCalls.Add(1)
			cur := inFlight.Add(1)
			for prev := maxInFlight.Load(); cur > prev; prev = maxInFlight.Load() {
				if maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(perCallDelay)
			inFlight.Add(-1)
			_, _ = io.WriteString(w, `{"events":[{"time":"2026-11-28T01:00:00Z"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()

	api := &StreamingAPI{}
	conn := desktopConnection{ID: "c-par", Server: remote.URL, Token: "tok"}

	start := time.Now()
	got := api.crewAccessRemoteRead(context.Background(), "owner", conn, "proj-par", "peer-par")
	elapsed := time.Since(start)
	if !got.peopleRead || len(got.people) != 1 || got.people[0].Name != "Alice" {
		t.Fatalf("unexpected remote result: %+v", got)
	}
	if got.last.IsZero() {
		t.Fatalf("expected events timestamp to be populated")
	}
	if maxInFlight.Load() < 2 {
		t.Fatalf("follow-up remote reads ran serially (maxInFlight=%d, elapsed=%v); expected parallel fan-out", maxInFlight.Load(), elapsed)
	}
	if elapsed >= 3*perCallDelay {
		t.Fatalf("parallel follow-up reads took %v, expected < %v", elapsed, 3*perCallDelay)
	}

	// markCrewAccessCacheStale preserves last-good data and triggers an immediate
	// background refresh without blocking the caller.
	beforeCalls := followUpCalls.Load()
	markCrewAccessCacheStale("proj-par", "peer-par")
	start = time.Now()
	stale := api.crewAccessRemoteRead(context.Background(), "owner", conn, "proj-par", "peer-par")
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("read after markCrewAccessCacheStale blocked for %v", d)
	}
	if !stale.peopleRead || len(stale.people) != 1 || stale.people[0].Name != "Alice" {
		t.Fatalf("markCrewAccessCacheStale lost last-good people: %+v", stale)
	}
	deadline := time.Now().Add(time.Second)
	for followUpCalls.Load() <= beforeCalls && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if followUpCalls.Load() <= beforeCalls {
		t.Fatalf("expected background refresh after markCrewAccessCacheStale")
	}
}
