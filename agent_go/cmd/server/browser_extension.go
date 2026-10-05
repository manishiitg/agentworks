package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

const browserExtensionConnectPath = "/api/browser/extension/connect"

func (api *StreamingAPI) handleBrowserExtension(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_path")
	if _, err := api.browserWorkspaceAccess(r, workspace, r.URL.Query().Get("profile_id"), true); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	user := GetUserFromContext(r.Context()).UserID
	scope := browserSessionForWorkspace(user, workspace)
	if scope == "" {
		http.Error(w, "Open a project or workflow browser", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req) != nil {
			http.Error(w, "Invalid browser request", 400)
			return
		}
		switch req.Action {
		case "pair":
			token, err := browserrelay.Default.Pair(user, scope, workspace)
			if err != nil {
				http.Error(w, "Cannot start Chrome bridge", 503)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"token": token, "expires_in": int(browserrelay.PairLifetime.Seconds())})
			return
		case "disconnect":
			if err := browserrelay.Default.Disconnect(user, scope); err != nil {
				http.Error(w, "Cannot save browser selection", 503)
				return
			}
		default:
			http.Error(w, "Unknown action", 400)
			return
		}
	}
	json.NewEncoder(w).Encode(browserrelay.Default.Status(user, scope))
}
func (api *StreamingAPI) handleBrowserExtensionConnect(w http.ResponseWriter, r *http.Request) {
	browserrelay.Default.ServeExtension(w, r)
}
func (api *StreamingAPI) handleBrowserExtensionDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="AgentWorks-Chrome-Bridge.zip"`)
	w.Write(browserrelay.ExtensionZip)
}
