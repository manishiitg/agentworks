// Static / public HTTP route handlers: health check, public file and folder
// serving (browse + download), capabilities, and CDP availability check.
// Relocated verbatim from server.go.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Health check endpoint
func (api *StreamingAPI) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Get current tracing provider
	tracingProvider := os.Getenv("TRACING_PROVIDER")
	if tracingProvider == "" {
		tracingProvider = "noop"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"time":    time.Now(),
		"version": llmtypes.VERSION,
		// Read by the server A deploy before it swaps releases: a restart while
		// a turn is running returns 502 to the user mid-message (server A,
		// 2026-09-03, twice in one afternoon). active_sessions counts turns
		// the tracker still considers running; in_flight_requests counts
		// non-GET API requests currently being served.
		"drain": api.drainStatus(),
		"config": map[string]interface{}{"provider": api.config.Provider,
			"model":            api.config.ModelID,
			"temperature":      api.config.Temperature,
			"max_turns":        api.config.MaxTurns,
			"tracing_provider": tracingProvider,
		},
	})
}

// Existing Share file and Share folder URLs keep their route contract.
func (api *StreamingAPI) handlePublicFile(w http.ResponseWriter, r *http.Request) {
	api.servePublicAsset(w, r, "read")
}
func (api *StreamingAPI) handlePublicFolder(w http.ResponseWriter, r *http.Request) {
	api.servePublicAsset(w, r, "list")
}
func (api *StreamingAPI) handlePublicFolderDownload(w http.ResponseWriter, r *http.Request) {
	api.servePublicAsset(w, r, "archive")
}

// publicWorkspaceUserID selects the durable workspace owner for public file
// routes. In a single-user deployment, the gateway's JWT subject is an
// internal product label (for example "video-studio"), not another workspace
// owner. Every public asset must therefore resolve to DEFAULT_USER_ID. The
// uid query parameter is not an access grant; personal files stay with the
// authenticated owner. Workflow links follow workflow sharing permissions.
func publicWorkspaceUserID(r *http.Request) string {
	if !IsMultiUserMode() {
		return GetDefaultUserID()
	}
	return GetUserIDFromContext(r.Context())
}

// API Key Validation endpoint - validates API keys for supported providers.
// Capabilities endpoint
func (api *StreamingAPI) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Get current tracing provider
	tracingProvider := os.Getenv("TRACING_PROVIDER")
	if tracingProvider == "" {
		tracingProvider = "noop"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"providers":   append([]string(nil), supportedLLMProviders...),
		"streaming":   true,
		"sse":         true,
		"agent_modes": []string{"multi-agent", "workflow"},
		"tracing": map[string]interface{}{
			"enabled":  tracingProvider != "noop",
			"provider": tracingProvider,
		},
		"workspace":     map[string]interface{}{},
		"servers":       []string{},
		"local_mode":    IsLocalMode(),
		"runtime_debug": runtimeDiagnosticsEnabled(),
		// Deployment-owned IANA timezone used by UI timestamps. It is distinct
		// from the host timezone and follows daylight-saving rules.
		"display_time_zone": strings.TrimSpace(os.Getenv("DISPLAY_TIME_ZONE")),
		// Live-attach (control-mode) terminal WebSocket transport. True when tmux
		// is new enough for control mode (the manager is constructed). Lets the
		// frontend render the selected live tmux terminal over
		// /api/terminals/{id}/stream instead of the snapshot/replay polling.
		// See docs/refactor/terminal_live_attach_transport.md.
		"terminal_live_attach": runtimeDiagnosticsEnabled() && api.liveAttach != nil,
		// Streaming microphone dictation (pkg/voicestt, voicestt.Status).
		// `available` is a build-time fact: false in a CGO_ENABLED=0 build,
		// where the mic must not be offered at all. `ready` flips once the
		// model has loaded, which on a first run means a ~690MB download after
		// startup. AgentWorks' own composer gates its mic on `available`;
		// product composers keep gating on their profile's
		// runtime.capabilities.voice instead.
		"voice": voiceManager.Status(),
	})
}

// handleCdpCheck checks if Chrome DevTools is reachable on localhost.
func (api *StreamingAPI) handleCdpCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !browser.CDPEnabled() {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"supported": false,
			"error":     "CDP is disabled for this server deployment",
		})
		return
	}

	portStr := r.URL.Query().Get("port")
	if portStr == "" {
		portStr = "9222"
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"error":     "invalid port number",
		})
		return
	}

	result, err := checkLocalChromeCdpVersion(port)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"error":     fmt.Sprintf("Cannot reach Chrome CDP /json/version on port %d: %v", port, err),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

func checkLocalChromeCdpVersion(port int) (map[string]interface{}, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", endpoint, resp.StatusCode)
	}

	var payload struct {
		Browser              string `json:"Browser"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.Browser) == "" && strings.TrimSpace(payload.WebSocketDebuggerURL) == "" {
		return nil, fmt.Errorf("%s did not return Chrome DevTools metadata", endpoint)
	}

	return map[string]interface{}{
		"connected": true,
		"browser":   payload.Browser,
		"endpoint":  endpoint,
	}, nil
}

// drainStatus reports what a deploy should wait for before restarting.
func (api *StreamingAPI) drainStatus() map[string]interface{} {
	// The tracker retains finished sessions for 24h; only ones still in the
	// running lifecycle state hold a turn a restart would cut.
	api.activeSessionsMux.RLock()
	active := 0
	for _, session := range api.activeSessions {
		if session != nil && normalizeSessionLifecycleStatus(session.Status) == sessionLifecycleRunning {
			active++
		}
	}
	api.activeSessionsMux.RUnlock()
	// A steered coding-agent turn returns its HTTP request immediately and
	// runs the model in the background, so neither the tracker nor the
	// request count sees it; the runtime coordinator does.
	generating := api.runtimeCoordinator.BusyCount()
	if generating > active {
		active = generating
	}
	inFlight := atomic.LoadInt64(&apiRequestsInFlight)
	return map[string]interface{}{
		"active_sessions":    active,
		"in_flight_requests": inFlight,
		"idle":               active == 0 && inFlight == 0,
	}
}
