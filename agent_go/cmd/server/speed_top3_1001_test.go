package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// #4394: non-streaming desktop connection proxy requests must bound
// ResponseHeaderTimeout and fail fast during cooldown, while streaming
// requests (/events, text/event-stream) remain exempt.
func TestDesktopConnectionProxyBoundsTimeoutAndCooldown(t *testing.T) {
	desktopTestEnvironment(t)
	oldHeaderTimeout := desktopProxyResponseHeaderTimeout
	oldCooldown := desktopProxyCooldown
	desktopProxyResponseHeaderTimeout = 100 * time.Millisecond
	desktopProxyCooldown = 2 * time.Second
	t.Cleanup(func() {
		desktopProxyResponseHeaderTimeout = oldHeaderTimeout
		desktopProxyCooldown = oldCooldown
		desktopProxyTransportMu.Lock()
		desktopProxyTransport = nil
		desktopProxyStreamTrans = nil
		desktopProxyTransportMu.Unlock()
		desktopProxyCooldownMu.Lock()
		desktopProxyUnreach = map[string]time.Time{}
		desktopProxyFailures = map[string]int{}
		desktopProxyProbing = map[string]bool{}
		desktopProxyCooldownMu.Unlock()
	})

	var nonStreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/events" {
			time.Sleep(180 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok-stream"))
			return
		}
		nonStreamHits.Add(1)
		time.Sleep(350 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	if err := writeDesktopConnections("owner", []desktopConnection{{ID: "conn-1", Server: upstream.URL, Token: "tok"}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	makeReq := func(path string) *http.Request {
		r := desktopTestRequest(http.MethodGet, "/api/connections/conn-1/"+path, "", "owner")
		return mux.SetURLVars(r, map[string]string{"connection": "conn-1", "path": path})
	}

	// First non-streaming call hits ResponseHeaderTimeout and returns 502.
	w1 := httptest.NewRecorder()
	api.handleDesktopConnectionProxy(w1, makeReq("api/crew-teams"))
	if w1.Code != http.StatusBadGateway {
		t.Fatalf("first timed-out call status = %d, want 502", w1.Code)
	}
	if nonStreamHits.Load() != 1 {
		t.Fatalf("nonStreamHits = %d, want 1", nonStreamHits.Load())
	}

	// Second non-streaming call during cooldown fails fast without hitting upstream.
	start := time.Now()
	w2 := httptest.NewRecorder()
	api.handleDesktopConnectionProxy(w2, makeReq("api/crew-teams"))
	if w2.Code != http.StatusBadGateway {
		t.Fatalf("cooldown call status = %d, want 502", w2.Code)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cooldown call took %v, want fast fail (<50ms)", elapsed)
	}
	if nonStreamHits.Load() != 1 {
		t.Fatalf("cooldown call should not hit upstream: hits = %d", nonStreamHits.Load())
	}

	// Streaming call (/api/events) bypasses ResponseHeaderTimeout and cooldown.
	w3 := httptest.NewRecorder()
	api.handleDesktopConnectionProxy(w3, makeReq("api/events"))
	if w3.Code != http.StatusOK {
		t.Fatalf("streaming call status = %d, want 200", w3.Code)
	}
}

// #4394: mcpToolStatusFromSet resolves connected vs available in O(1) from a
// single vaultServerSetFor lookup.
func TestMCPToolStatusFromSet(t *testing.T) {
	connected := map[string]struct{}{
		"vault_c-abc": {},
	}
	if got := mcpToolStatusFromSet("vault_c-abc", connected); got.Connection != connectionConnected || got.Status != "not_loaded" {
		t.Fatalf("connected server status = %+v, want connected/not_loaded", got)
	}
	if got := mcpToolStatusFromSet("other", connected); got.Connection != connectionAvailable || got.Status != "not_connected" {
		t.Fatalf("unconnected server status = %+v, want available/not_connected", got)
	}
}

// #4394: fillProviderAccountConfigured must not block normal GET requests on
// uncached CLI probes, must wait inline only when withProviderAccountConfiguredWait
// is set (?refresh=true), and must cache probe failures briefly so a failing
// probe does not re-run on every request.
func TestProviderAccountConfiguredNonBlockingByDefaultAndCachesFailure(t *testing.T) {
	oldProbe := providerAccountStatusProbe
	oldWait := providerAccountConfiguredWait
	providerAccountConfiguredWait = 1 * time.Second
	t.Cleanup(func() {
		providerAccountStatusProbe = oldProbe
		providerAccountConfiguredWait = oldWait
		providerAccountConfiguredCache.Lock()
		providerAccountConfiguredCache.entries = map[string]providerAccountConfiguredEntry{}
		providerAccountConfiguredCache.inFlight = map[string]bool{}
		providerAccountConfiguredCache.Unlock()
	})

	providerAccountConfiguredCache.Lock()
	providerAccountConfiguredCache.entries = map[string]providerAccountConfiguredEntry{}
	providerAccountConfiguredCache.inFlight = map[string]bool{}
	providerAccountConfiguredCache.Unlock()

	probeDone := make(chan struct{})
	var probeCalls atomic.Int32
	providerAccountStatusProbe = func(_ context.Context, _ *StreamingAPI, id string) (providerAccountStatus, bool) {
		probeCalls.Add(1)
		if id == "fail-acc" {
			return providerAccountStatus{}, false
		}
		time.Sleep(200 * time.Millisecond)
		close(probeDone)
		return providerAccountStatus{State: "signed_in", Identity: "user@example.com"}, true
	}

	api := &StreamingAPI{}
	records := []storedProviderConnection{
		{ProviderConnection: ProviderConnection{ID: "cli-acc", Provider: "codex-cli", AuthMethod: "cli_login"}},
	}
	views := []providerAccountView{
		{ProviderConnection: records[0].ProviderConnection, Relation: "own"},
	}

	// Normal GET returns immediately without waiting for the 200ms CLI probe.
	start := time.Now()
	api.fillProviderAccountConfigured(context.Background(), views, records)
	if elapsed := time.Since(start); elapsed > 80*time.Millisecond {
		t.Fatalf("normal fillProviderAccountConfigured blocked for %v", elapsed)
	}
	// Background probe completes and warms the cache.
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("background probe did not run")
	}
	time.Sleep(20 * time.Millisecond)
	views2 := []providerAccountView{{ProviderConnection: records[0].ProviderConnection, Relation: "own"}}
	api.fillProviderAccountConfigured(context.Background(), views2, records)
	if views2[0].Configured == nil || !*views2[0].Configured || views2[0].Identity != "user@example.com" {
		t.Fatalf("cached view = %+v, want configured=true identity=user@example.com", views2[0])
	}

	// Explicit ?refresh=true waits inline and caches failed probes so a second
	// call does not re-probe immediately.
	failRecs := []storedProviderConnection{
		{ProviderConnection: ProviderConnection{ID: "fail-acc", Provider: "codex-cli", AuthMethod: "cli_login"}},
	}
	failViews := []providerAccountView{{ProviderConnection: failRecs[0].ProviderConnection, Relation: "own"}}
	api.fillProviderAccountConfigured(withProviderAccountConfiguredWait(context.Background()), failViews, failRecs)
	callsAfterFirst := probeCalls.Load()
	api.fillProviderAccountConfigured(withProviderAccountConfiguredWait(context.Background()), failViews, failRecs)
	if probeCalls.Load() != callsAfterFirst {
		t.Fatalf("failed probe was not cached: calls went %d -> %d", callsAfterFirst, probeCalls.Load())
	}
}
