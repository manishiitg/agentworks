package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Pairing delegates only these project-bound operations. It does not grant
// tools the desktop connection proxy, arbitrary remote routes or credentials.
func (api *StreamingAPI) pairedCrewSyncAction(w http.ResponseWriter, r *http.Request, id, action string) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, id)
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	if action == "" || action == "status" {
		out, err := api.crewSync(r.Context(), claims, id, crewSyncRequest{Action: "status"})
		if err != nil {
			externalError(w, 409, "sync_unavailable", err.Error())
			return
		}
		externalJSON(w, out)
		return
	}
	if action != "update" && action != "server" && action != "local" {
		externalError(w, 400, "invalid_action", "Choose status, update, server or local.")
		return
	}
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil || binding == nil || binding.Side != "local" {
		externalError(w, 409, "pair_first", "Open the local Crew in the Mac app and choose its server in Project sync.")
		return
	}
	data, _ := json.Marshal(map[string]string{"action": action})
	request := r.Clone(r.Context())
	request.Method = http.MethodPost
	request.Body = io.NopCloser(bytes.NewReader(data))
	request = mux.SetURLVars(request, map[string]string{"project": roots.ID, "connection": binding.ConnectionID})
	api.runDesktopCrewSync(w, request, false)
}

func (api *StreamingAPI) registerCrewSyncChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root string) error {
	if !IsLocalMode() || readOnly || !websiteDeviceClaims(claims) {
		return nil
	}
	gate.Declare("manage_crew_sync")
	return reg.RegisterCustomToolWithTimeout("manage_crew_sync", "Inspect this Crew's server sync or explicitly update its server copy and move editing. Pair once in the Mac app's Project sync panel. action=status, update, server (Work on server), local (Work on this Mac). Uses only the owner's saved paired connection; no login or private chat transfer. Handoff requires existing project turns to finish first.", map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "update", "server", "local"}}}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller, err := api.interactiveLaptopOwner(ctx, claims, sessionID, root)
		if err != nil {
			return "", err
		}
		manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
		if err != nil {
			return "", err
		}
		request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
		response := httptest.NewRecorder()
		api.pairedCrewSyncAction(response, request, manifest.ID, externalArg(args, "action"))
		if response.Code >= 400 {
			return "", fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
		}
		return strings.TrimSpace(response.Body.String()), nil
	}, 5*time.Minute, "workspace_advanced")
}
