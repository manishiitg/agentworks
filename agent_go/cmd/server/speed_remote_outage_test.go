package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

func resetDesktopProxyCircuitForTest(t *testing.T) {
	t.Helper()
	oldHeaderTimeout := desktopProxyResponseHeaderTimeout
	oldCooldown := desktopProxyCooldown
	oldMaxCooldown := desktopProxyMaxCooldown
	t.Cleanup(func() {
		desktopProxyResponseHeaderTimeout = oldHeaderTimeout
		desktopProxyCooldown = oldCooldown
		desktopProxyMaxCooldown = oldMaxCooldown
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
	desktopProxyCooldownMu.Lock()
	desktopProxyUnreach = map[string]time.Time{}
	desktopProxyFailures = map[string]int{}
	desktopProxyProbing = map[string]bool{}
	desktopProxyCooldownMu.Unlock()
}

// #4515: after cooldown expires on an unreachable connection, only one
// half-open probe request may hit upstream while concurrent callers fail fast,
// and repeated failures double the cooldown up to desktopProxyMaxCooldown.
func TestDesktopProxyHalfOpenSingleProbeAndBackoff(t *testing.T) {
	desktopTestEnvironment(t)
	resetDesktopProxyCircuitForTest(t)

	desktopProxyResponseHeaderTimeout = 80 * time.Millisecond
	desktopProxyCooldown = 60 * time.Millisecond
	desktopProxyMaxCooldown = 240 * time.Millisecond

	var hits atomic.Int32
	var shouldSucceed atomic.Bool
	probeStarted := make(chan struct{}, 1)
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if shouldSucceed.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if n == 2 {
			select {
			case probeStarted <- struct{}{}:
			default:
			}
			<-releaseUpstream
			return
		}
		time.Sleep(180 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	defer close(releaseUpstream)

	if err := writeDesktopConnections("owner", []desktopConnection{{ID: "conn-halfopen", Server: upstream.URL, Token: "tok"}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	makeReq := func() *http.Request {
		r := desktopTestRequest(http.MethodGet, "/api/connections/conn-halfopen/api/crew-teams", "", "owner")
		return mux.SetURLVars(r, map[string]string{"connection": "conn-halfopen", "path": "api/crew-teams"})
	}

	// 1. Initial failure trips cooldown (1x = 60ms).
	w1 := httptest.NewRecorder()
	api.handleDesktopConnectionProxy(w1, makeReq())
	if w1.Code != http.StatusBadGateway {
		t.Fatalf("first call status = %d, want 502", w1.Code)
	}

	// Wait for first cooldown to expire so connection enters half-open state.
	time.Sleep(75 * time.Millisecond)

	// 2. Launch probe + 4 concurrent callers: only the probe hits upstream.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		api.handleDesktopConnectionProxy(w, makeReq())
	}()

	select {
	case <-probeStarted:
	case <-time.After(time.Second):
		t.Fatal("half-open probe did not start")
	}

	start := time.Now()
	for i := 0; i < 4; i++ {
		w := httptest.NewRecorder()
		api.handleDesktopConnectionProxy(w, makeReq())
		if w.Code != http.StatusBadGateway {
			t.Fatalf("concurrent caller %d status = %d, want 502", i, w.Code)
		}
	}
	if elapsed := time.Since(start); elapsed > 40*time.Millisecond {
		t.Fatalf("concurrent callers during probe took %v, want fast fail (<40ms)", elapsed)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 (initial + single probe)", got)
	}

	// Wait for probe to time out -> failure count 2 -> backoff = 120ms.
	wg.Wait()
	desktopProxyCooldownMu.Lock()
	failures := desktopProxyFailures["conn-halfopen"]
	remaining := time.Until(desktopProxyUnreach["conn-halfopen"])
	desktopProxyCooldownMu.Unlock()
	if failures != 2 {
		t.Fatalf("failures = %d, want 2", failures)
	}
	if remaining < 70*time.Millisecond {
		t.Fatalf("backoff remaining = %v, want > 70ms (2x 60ms)", remaining)
	}

	// 3. Recovery clears backoff and failure count.
	shouldSucceed.Store(true)
	time.Sleep(remaining + 15*time.Millisecond)
	wOK := httptest.NewRecorder()
	api.handleDesktopConnectionProxy(wOK, makeReq())
	if wOK.Code != http.StatusOK {
		t.Fatalf("recovery call status = %d, want 200", wOK.Code)
	}
	desktopProxyCooldownMu.Lock()
	failuresAfter := desktopProxyFailures["conn-halfopen"]
	desktopProxyCooldownMu.Unlock()
	if failuresAfter != 0 {
		t.Fatalf("failures after recovery = %d, want 0", failuresAfter)
	}
}

// #4515: serverVaultGet shares the per-connection unreachable cooldown so a
// timed-out secret fetch prevents the subsequent company-keys fetch from
// burning another full timeout.
func TestServerVaultGetSharesUnreachableCooldown(t *testing.T) {
	resetDesktopProxyCircuitForTest(t)
	oldVaultTimeout := serverVaultRequestTimeout
	desktopProxyCooldown = 2 * time.Second
	serverVaultRequestTimeout = 80 * time.Millisecond
	t.Cleanup(func() {
		serverVaultRequestTimeout = oldVaultTimeout
	})

	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	conn := desktopConnection{ID: "conn-vault-1", Server: upstream.URL, Token: "tok"}

	// First call times out and trips shared cooldown.
	ctx1, cancel1 := context.WithTimeout(context.Background(), serverVaultRequestTimeout)
	defer cancel1()
	_, res1 := serverVaultGet(ctx1, "owner", conn, "/api/vault/me/secret/VLT_TEST")
	if res1 != "unreachable" {
		t.Fatalf("first serverVaultGet result = %q, want unreachable", res1)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits after first call = %d, want 1", hits.Load())
	}

	// Second call (e.g. company-keys) fails fast without hitting upstream again.
	start := time.Now()
	ctx2, cancel2 := context.WithTimeout(context.Background(), serverVaultRequestTimeout)
	defer cancel2()
	_, res2 := serverVaultGet(ctx2, "owner", conn, "/api/vault/me/company-keys")
	if res2 != "unreachable" {
		t.Fatalf("second serverVaultGet result = %q, want unreachable", res2)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("second serverVaultGet took %v, want fast fail (<30ms)", elapsed)
	}
	if hits.Load() != 1 {
		t.Fatalf("second serverVaultGet should not hit upstream: hits = %d", hits.Load())
	}
}

// #4515: remote GET /api/agent-message-identity fails fast during cooldown,
// trips cooldown on remote timeout, and caches successful responses briefly.
func TestAgentMessageIdentityRemoteCooldownAndCache(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	resetDesktopProxyCircuitForTest(t)

	oldTimeout := agentMessageIdentityRemoteTimeout
	oldTTL := agentMessageIdentityCacheTTL
	desktopProxyCooldown = 2 * time.Second
	agentMessageIdentityRemoteTimeout = 100 * time.Millisecond
	agentMessageIdentityCacheTTL = 5 * time.Second
	t.Cleanup(func() {
		agentMessageIdentityRemoteTimeout = oldTimeout
		agentMessageIdentityCacheTTL = oldTTL
		agentMessageIdentityCacheMu.Lock()
		agentMessageIdentityCache = map[string]agentMessageIdentityCacheEntry{}
		agentMessageIdentityCacheMu.Unlock()
	})
	agentMessageIdentityCacheMu.Lock()
	agentMessageIdentityCache = map[string]agentMessageIdentityCacheEntry{}
	agentMessageIdentityCacheMu.Unlock()

	var hits atomic.Int32
	var hang atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if hang.Load() {
			time.Sleep(250 * time.Millisecond)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"label":   "Remote Agent",
			"address": "agent:remote",
		})
	}))
	defer upstream.Close()

	if err := writeDesktopConnections(GetDefaultUserID(), []desktopConnection{{ID: "conn-id-1", Server: upstream.URL, Token: "tok"}}); err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{}
	call := func(msgID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/agent-message-identity?direction=incoming&inbox_id=network%3Aconn-id-1%3Ainbox-1&message_id="+msgID, nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: GetDefaultUserID()}))
		rec := httptest.NewRecorder()
		api.handleAgentMessageIdentity(rec, req)
		return rec
	}

	// 1. Healthy remote call succeeds and is cached for repeat lookups.
	rec1 := call("msg-1")
	if rec1.Code != http.StatusOK {
		t.Fatalf("first identity call status = %d, want 200 (%s)", rec1.Code, rec1.Body.String())
	}
	if rec1.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec1.Header().Get("Cache-Control"))
	}
	rec2 := call("msg-1")
	if rec2.Code != http.StatusOK {
		t.Fatalf("cached identity call status = %d, want 200", rec2.Code)
	}
	if hits.Load() != 1 {
		t.Fatalf("cached identity call hit upstream: hits = %d, want 1", hits.Load())
	}

	// 2. Remote timeout on an uncached message trips cooldown; next message fails fast.
	hang.Store(true)
	rec3 := call("msg-2")
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("timed-out identity call status = %d, want 404", rec3.Code)
	}
	if !desktopProxyInCooldown("conn-id-1") {
		t.Fatalf("expected conn-id-1 to be in cooldown after remote timeout")
	}

	start := time.Now()
	rec4 := call("msg-3")
	if rec4.Code != http.StatusNotFound {
		t.Fatalf("cooldown identity call status = %d, want 404", rec4.Code)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Millisecond {
		t.Fatalf("cooldown identity call took %v, want fast fail (<30ms)", elapsed)
	}
	if hits.Load() != 2 {
		t.Fatalf("cooldown identity call should not hit upstream: hits = %d, want 2", hits.Load())
	}
}
