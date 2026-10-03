package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

func (api *StreamingAPI) handleBrowserRecording(w http.ResponseWriter, r *http.Request) {
	session := mux.Vars(r)["session"]
	if strings.HasPrefix(session, "pw-") {
		api.handlePlaywrightRecording(w, r, session)
		return
	}
	authorized := false
	for _, item := range api.liveBrowserSessions(r) {
		if item["browser_session"] == session {
			authorized = true
			break
		}
	}
	if !authorized {
		http.Error(w, "Browser session not found", 404)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request) != nil || (request.Action != "start" && request.Action != "stop" && request.Action != "status") {
		http.Error(w, "Invalid recording action", 400)
		return
	}
	workspace := r.URL.Query().Get("workspace_path")
	physical, accessErr := api.browserWorkspaceAccess(r, workspace, r.URL.Query().Get("profile_id"), request.Action != "status")
	if accessErr != nil {
		http.Error(w, accessErr.Error(), 403)
		return
	}
	endpoint := strings.TrimRight(os.Getenv("WORKSPACE_API_URL"), "/")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8081"
	}
	if request.Action != "status" {
		release, ok := browser.TryTakeWorkspaceBrowserControl(session)
		if !ok {
			http.Error(w, "Return browser control before recording", 409)
			return
		}
		defer release()
	}
	payload, _ := json.Marshal(map[string]string{"action": request.Action, "workspace_path": physical})
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint+"/api/browser/live/"+session+"/recording", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, "Workspace unavailable", 502)
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	response, err := (&http.Client{Timeout: 100 * time.Second}).Do(upstream)
	if err != nil {
		http.Error(w, "Recording request could not be completed; check recording status before retrying", 502)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var state struct {
		Recording bool   `json:"recording"`
		Owner     string `json:"owner_session"`
	}
	if response.StatusCode == http.StatusOK && json.Unmarshal(body, &state) == nil {
		browser.GetSessionTracker().SetCapture(session, state.Recording, state.Owner, physical)
	}
	_, _ = w.Write(body)
}
