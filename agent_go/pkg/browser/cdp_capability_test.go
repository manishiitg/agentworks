package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCDPEnabledOnlyOnLocalInstallation(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	if !CDPEnabled() {
		t.Fatal("local installations support CDP")
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if CDPEnabled() {
		t.Fatal("server installations must disable CDP")
	}
}

func TestDisabledCDPAutoStatusIsHeadlessWithoutProbe(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "must not probe", http.StatusInternalServerError)
	}))
	defer server.Close()

	executor := NewExecutor(NewClient(server.URL), WithBrowserRuntimeConfig(NewBrowserRuntimeConfig("auto", []int{9222})))
	result, err := executor.HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "status",
		"session": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var status browserRuntimeStatus
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		t.Fatal(err)
	}
	if status.CDPSupported || status.EffectiveMode != "headless" || status.CDPDisabledReason == "" {
		t.Fatalf("unexpected disabled status: %+v", status)
	}
	if requests.Load() != 0 {
		t.Fatalf("disabled deployment made %d CDP probes", requests.Load())
	}
}

func TestDisabledCDPRejectsExplicitInvocation(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	executor := NewExecutor(NewClient("http://127.0.0.1:1"), WithCdpPort(9222))
	_, err := executor.HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "snapshot",
		"args":    []string{"--cdp", "http://localhost:9222"},
		"session": "test",
	})
	if err == nil || !strings.Contains(err.Error(), "CDP_DISABLED") {
		t.Fatalf("expected CDP_DISABLED, got %v", err)
	}
}
