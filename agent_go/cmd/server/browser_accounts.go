package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

func (api *StreamingAPI) handleAccountBrowsers(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "Sign in to connect your browser.", 403)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var req struct {
			Action    string `json:"action"`
			BrowserID string `json:"browser_id"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&req) != nil {
			http.Error(w, "Invalid browser request", 400)
			return
		}
		// Project-scoped external agents receive metadata and use project
		// selection; they never receive the general account pairing credential.
		if claims.AccessToken != nil {
			http.Error(w, "Open Account → Browser connections in AgentWorks to connect or disconnect a browser.", 403)
			return
		}
		switch req.Action {
		case "pair":
			handoff := browserrelay.Default.BeginAccountPairing(claims.UserID, firstNonEmptyTrimmed(claims.Email, claims.Username, claims.UserID))
			json.NewEncoder(w).Encode(map[string]any{"handoff": handoff})
			return
		case "disconnect":
			if err := browserrelay.Default.DisconnectBrowser(claims.UserID, req.BrowserID); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		default:
			http.Error(w, "Unknown browser action", 400)
			return
		}
	} else if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	browsers := browserrelay.Default.Browsers(claims.UserID)
	connected := false
	for _, b := range browsers {
		connected = connected || b.Connected
	}
	json.NewEncoder(w).Encode(map[string]any{"browsers": browsers, "account_connected": connected,
		"latest_extension_version": browserrelay.LatestExtensionVersion(), "minimum_extension_version": browserrelay.MinimumExtensionVersion,
		"pairing_error": browserrelay.Default.PairingFailure(claims.UserID)})
}
