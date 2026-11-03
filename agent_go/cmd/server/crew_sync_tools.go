package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// "Reach it from your phone and the web" from chat (owner, 3 Nov): on goes online on the one saved sign-in, or on the
// network the person named when there are several; off removes the online copy. The same Mac actions as the switch.
func (api *StreamingAPI) pairedCrewReach(w http.ResponseWriter, r *http.Request, project string, on bool, network string) {
	claims := GetUserFromContext(r.Context())
	binding, err := crewroot.ReadSync(project)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	connection := ""
	if binding != nil {
		connection = binding.ConnectionID
	}
	if on && binding == nil {
		saved, err := readDesktopConnections(claims.UserID)
		if err != nil || len(saved) == 0 {
			externalError(w, 409, "sign_in_first", "Sign in to a network in the AgentWorks app first.")
			return
		}
		names := []string{}
		for _, item := range saved {
			label := desktopConnectionLabel(item)
			names = append(names, label)
			if len(saved) == 1 || strings.EqualFold(network, label) || strings.EqualFold(network, item.Name) || network == item.ID {
				connection = item.ID
			}
		}
		if connection == "" {
			externalError(w, 400, "choose_network", "Which network? "+strings.Join(names, ", ")+". Ask the person, then call reach_on with network.")
			return
		}
	}
	if !on && binding == nil {
		externalJSON(w, map[string]any{"reach": "off"})
		return
	}
	action := map[bool]string{true: "relay", false: "offline"}[on]
	request := r.Clone(r.Context())
	request.Method = http.MethodPost
	data, _ := json.Marshal(map[string]string{"action": action})
	request.Body = io.NopCloser(bytes.NewReader(data))
	request = mux.SetURLVars(request, map[string]string{"project": project, "connection": connection})
	api.runDesktopCrewSync(w, request, false)
}

// desktopConnectionLabel is how a saved sign-in is named to people: its server's host, such as agents.example.com.
func desktopConnectionLabel(item desktopConnection) string {
	if parsed, err := url.Parse(item.Server); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return item.Name
}

// Pairing delegates only these project-bound operations. It does not grant
// tools the desktop connection proxy, arbitrary remote routes or credentials.
func (api *StreamingAPI) pairedCrewSyncAction(w http.ResponseWriter, r *http.Request, id, action, network string) {
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
	if action == "reach_on" || action == "reach_off" {
		api.pairedCrewReach(w, r, roots.ID, action == "reach_on", network)
		return
	}
	if action != "server" {
		externalError(w, 400, "invalid_action", "Choose status, reach_on, reach_off or server (move online).")
		return
	}
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil || binding == nil || binding.Side != "local" {
		externalError(w, 409, "pair_first", "This Crew is not ready to move yet. Open it in the Mac app once while you are signed in.")
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
	if err := api.registerOpenServerProjectTool(reg, gate, claims, sessionID, root); err != nil {
		return err
	}
	gate.Declare("manage_crew_sync")
	return reg.RegisterCustomToolWithTimeout("manage_crew_sync", "Where this Crew lives and whether the owner can reach it from the phone and the web. status: on this Mac or online. reach_on: reach it from the phone and the web while the Mac is on (with several networks, pass network; if unsure, ask the person which one). reach_off: only in the app on this Mac; removes its empty online copy. server: move it online, so it works with the Mac off and can be shared with people (copies its files and your main conversation once). Only the owner, on this Mac. Say \"On this Mac\", \"Offline\" and \"Online\" to the person; never server, sync or relay.", map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "reach_on", "reach_off", "server"}}, "network": map[string]any{"type": "string", "description": "For reach_on with several networks: the network's name, as the person said it."}}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
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
		api.pairedCrewSyncAction(response, request, manifest.ID, externalArg(args, "action"), externalArg(args, "network"))
		if response.Code >= 400 {
			return "", fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
		}
		return strings.TrimSpace(response.Body.String()), nil
	}, 5*time.Minute, "workspace_advanced")
}
